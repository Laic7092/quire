package reader

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/laic7092/layx/raster"
	"github.com/laic7092/layx/render/paint"
)

// Text reaches the screen here through a glyph atlas and reaches an exported PNG
// through layx/raster's DrawGlyphs, not through the toolkit's own glyph pipeline,
// and the toolkit's pipeline does not print the same page. The toolkit's
// Windows path blends a glyph's coverage with DirectWrite's enhanced contrast;
// its macOS and Linux paths draw masks the system's own printer has already
// smoothed and darkened. A coverage straight from a geometric rasterizer has
// none of that, so the antialiased edge of every stroke — the fringe that is
// most of what the eye reads as weight — lands lighter than the system would
// print it, and the same book reads thinner and greyer than it does in a
// browser.
//
// What is applied is what the system printers apply, and no more, and the curve
// itself lives in the rasterizer: see `raster.Tone`, which both the screen's
// glyph atlas and the headless page renderer feed their coverage through, so a
// page on screen and the same page in a PNG carry the same ink. What is left
// here is the policy around it — which inks a document bakes, which band of
// sizes a run is set at, whether it is a synthetic bold — and the cache that
// keeps a document's handful of inks from being rebuilt per glyph.

const (
	// toneCacheMax is how many corrections a painter keeps. A document bakes a
	// handful of inks — its text, links, accents — so the cache only ever holds
	// a few, and a runaway one falls back to the nearest ink rather than
	// rasterizing every glyph for every colour a book could name.
	toneCacheMax = 32
)

// textToneOff, set by LAYX_TEXT_TONE=0, draws the coverage as the rasterizer
// made it: the switch that shows what the correction is worth.
var textToneOff = os.Getenv("LAYX_TEXT_TONE") == "0"

// textGrow, set by LAYX_TEXT_GROW, is how far an edge grows in coverage where
// the coverage is half: the one number that sets how heavy the type reads.
var textGrow = envFloat("LAYX_TEXT_GROW", 0.10)

// textStemOff, set by LAYX_TEXT_STEM=0, keeps the contrast but leaves the edges
// of the glyphs where the outlines put them.
var textStemOff = os.Getenv("LAYX_TEXT_STEM") == "0"

// fakeBoldOff, set by LAYX_FAKE_BOLD=0, draws a run that asks for a weight the
// face does not have as the regular it fell back to, as it was before.
var fakeBoldOff = os.Getenv("LAYX_FAKE_BOLD") == "0"

// toneKey names one correction: the ink it is for, the band of sizes the type is
// set at, since stem darkening is the part that depends on the size, and whether
// the run is drawn in a weight its face does not have. A run grown into a
// synthetic bold is already heavier than its outline, so it takes the contrast
// and not the growth: a bold body's strokes would close their counters with
// both.
type toneKey struct {
	r, g, b uint8
	band    uint8
	grown   bool
}

// sizeBand buckets a size into the bands the stem darkening changes over: body
// type, small print, subheads, and display type, which wants none of it.
func sizeBand(size float32) uint8 {
	switch {
	case size < 14:
		return 0
	case size < 20:
		return 1
	case size < 28:
		return 2
	}
	return 3
}

// stemDarkening is how far an edge grows, in coverage, for a band: a stem one
// device pixel wide set at half coverage on each side reads as grey mush where
// the same stem with its edges grown reads as a stroke. Display type has stems
// of several pixels and needs less of it.
func stemDarkening(band uint8) float64 {
	if textStemOff {
		return 0
	}
	switch band {
	case 0:
		return textGrow * 1.4
	case 1:
		return textGrow
	case 2:
		return textGrow * 0.5
	}
	return 0
}

// toneCache is the corrections of one painter, keyed by toneKey. A painter is
// drawn on one goroutine, so no lock: two paths (the screen's atlas and the
// headless renderer) each keep their own.
type toneCache struct {
	tones map[toneKey]*raster.Tone
}

// toneFor returns the correction for ink c at size, building it once per ink,
// band and weight: a run's colour is one of the handful a document bakes, so the
// table is made once and every glyph of that ink is rasterized with it. grown
// says the run is a synthetic bold, which takes the contrast without the growth
// (see toneKey).
func (tc *toneCache) toneFor(c paint.Color, size float32, grown bool) *raster.Tone {
	if textToneOff || c.A == 0 {
		return nil
	}
	key := toneKey{r: c.R, g: c.G, b: c.B, band: sizeBand(size), grown: grown}
	if tone, ok := tc.tones[key]; ok {
		return tone
	}
	if len(tc.tones) >= toneCacheMax {
		// A book that names a colour a paragraph would otherwise fill the atlas
		// with a bitmap of every glyph for each of them: the nearest ink's
		// correction is within a percent or two of the exact one, and it keeps
		// the atlas to the inks the document actually reads in.
		if near := tc.nearestTone(c, key.band, key.grown); near != nil {
			return near
		}
		return nil
	}
	if tc.tones == nil {
		tc.tones = make(map[toneKey]*raster.Tone)
	}
	tone := raster.NewTone(c, raster.ToneOptions{
		Grow:          stemDarkening(key.band),
		SyntheticBold: key.grown,
	})
	tc.tones[key] = tone
	return tone
}

// nearestTone is the correction of the closest ink in the cache to c, by luma,
// or nil when the cache holds nothing of that band and weight.
func (tc *toneCache) nearestTone(c paint.Color, band uint8, grown bool) *raster.Tone {
	want := inkLuma(c)
	best, bestGap := (*raster.Tone)(nil), 2.0
	for key, tone := range tc.tones {
		if key.band != band || key.grown != grown {
			continue
		}
		if gap := math.Abs(inkLuma(paint.Color{R: key.r, G: key.g, B: key.b}) - want); gap < bestGap {
			best, bestGap = tone, gap
		}
	}
	return best
}

// inkLuma is how light a colour reads, on the weights the contrast follows.
func inkLuma(c paint.Color) float64 {
	return (0.30*float64(c.R) + 0.59*float64(c.G) + 0.11*float64(c.B)) / 255
}

// envFloat reads a number from the environment, falling back to def: a knob a
// reader tunes by eye, which costs nothing when it is unset.
func envFloat(name string, def float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return def
	}
	return v
}
