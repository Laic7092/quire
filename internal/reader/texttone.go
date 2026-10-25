package reader

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/laix/layx/render/paint"
)

// Text reaches the screen here through a glyph atlas, not through the toolkit's
// own glyph pipeline, and the two do not print the same page. The toolkit's
// Windows path blends a glyph's coverage with DirectWrite's enhanced contrast;
// its macOS and Linux paths draw masks the system's own printer has already
// smoothed and darkened. A coverage straight from a geometric rasterizer has
// none of that, so the antialiased edge of every stroke — the fringe that is
// most of what the eye reads as weight — lands lighter than the system would
// print it, and the same book reads thinner and greyer than it does in a
// browser.
//
// So the correction is baked into the atlas. A glyph is rasterized once per
// face, size and subpixel phase anyway, and a table lookup a pixel at
// rasterizing time costs nothing at draw time.
//
// What it adds is what the system printers add, and no more. The contrast curve
// is DirectWrite's: it grows as the ink darkens, which is the enhanced contrast
// a browser's text is drawn with. On top of it the edges are grown by a
// fraction of a pixel, which is CoreText's font smoothing and FreeType's light
// hinting by other means: it is what keeps a stem one pixel wide from reading as
// two half-lit ones.
//
// Direct2D's gamma ratios are deliberately not applied. Measured against this
// reader's own page they cancel the contrast curve almost exactly — at half
// coverage the contrast takes 0.5 to 0.6 and the 1.8 gamma correction brings it
// back to 0.49 — so they would add a table of magic numbers without changing a
// pixel. The weight a reader compares is the growth.

// toneLUT is the corrected coverage of each coverage a rasterizer can produce,
// for one ink at one size.
type toneLUT [256]uint8

const (
	// textContrast is the contrast DirectWrite's grayscale mode enhances with,
	// the more the darker the ink (see toneLUTFor).
	textContrast = 0.5
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

// toneLUTFor builds the correction for ink c in band: DirectWrite's contrast
// curve, and then, unless the run is a synthetic bold that carries its own
// growth, a growth of the edges — the pixels between paper and ink — so the
// strokes keep their weight and their joins stay round.
func toneLUTFor(c paint.Color, band uint8, grown bool) *toneLUT {
	ink := [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
	luma := 0.30*ink[0] + 0.59*ink[1] + 0.11*ink[2]
	// Dark ink gets the full contrast; pale ink, as a night paper's, none.
	k := textContrast * math.Min(math.Max(3-4*luma, 0), 1)
	darken := stemDarkening(band)
	if grown {
		darken = 0
	}
	lut := new(toneLUT)
	for i := range lut {
		a := float64(i) / 255
		v := a * (k + 1) / (a*k + 1)
		// The growth peaks where the coverage is half, which is the edge of a
		// stroke, and vanishes at the paper and inside the ink: what a dilated
		// edge does, without moving the glyph.
		v = math.Min(v+darken*(1-math.Abs(2*v-1)), 1)
		lut[i] = uint8(math.Round(v * 255))
	}
	return lut
}

// toneFor returns the correction for ink c at size, building it once per ink,
// band and weight: a run's colour is one of the handful a document bakes, so the
// table is made once and every glyph of that ink is rasterized with it. grown
// says the run is a synthetic bold, which takes the contrast without the growth
// (see toneKey).
func (u *UIPainter) toneFor(c paint.Color, size float32, grown bool) *toneLUT {
	if textToneOff || c.A == 0 {
		return nil
	}
	key := toneKey{r: c.R, g: c.G, b: c.B, band: sizeBand(size), grown: grown}
	if lut, ok := u.tones[key]; ok {
		return lut
	}
	if len(u.tones) >= toneCacheMax {
		// A book that names a colour a paragraph would otherwise fill the atlas
		// with a bitmap of every glyph for each of them: the nearest ink's
		// correction is within a percent or two of the exact one, and it keeps
		// the atlas to the inks the document actually reads in.
		if near := u.nearestTone(c, key.band, key.grown); near != nil {
			return near
		}
		return nil
	}
	if u.tones == nil {
		u.tones = make(map[toneKey]*toneLUT)
	}
	lut := toneLUTFor(c, key.band, key.grown)
	u.tones[key] = lut
	return lut
}

// nearestTone is the correction of the closest ink in the cache to c, by luma,
// or nil when the cache holds nothing of that band and weight.
func (u *UIPainter) nearestTone(c paint.Color, band uint8, grown bool) *toneLUT {
	want := inkLuma(c)
	best, bestGap := (*toneLUT)(nil), 2.0
	for key, lut := range u.tones {
		if key.band != band || key.grown != grown {
			continue
		}
		if gap := math.Abs(inkLuma(paint.Color{R: key.r, G: key.g, B: key.b}) - want); gap < bestGap {
			best, bestGap = lut, gap
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
