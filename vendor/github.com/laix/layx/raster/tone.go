package raster

import (
	"math"

	"github.com/laix/layx/render/paint"
)

// Tone is an ink correction for glyph coverage: the table from the coverage a
// geometric rasterizer produces to the coverage a system text printer prints for
// the same ink at the same size.
//
// A geometric rasterizer draws the outline and nothing else. System printers add
// two things on top of it: an enhanced contrast, which grows as the ink darkens
// (DirectWrite's grayscale mode), and a small growth of the stroke edges
// (CoreText's font smoothing, FreeType's light stem darkening). An antialiased
// edge is most of what the eye reads as weight, so without them text of the same
// outlines reads thinner and greyer than the same text in a system viewer.
//
// The correction depends on the ink, because contrast follows luma, and on the
// size band, because a body's stem needs more growth than a heading's. Those are
// the host's policy; the curve is here so a page drawn glyph by glyph through
// DrawGlyphs and a host's own glyph atlas drawn through Coverage are corrected
// the same way.
type Tone struct {
	lut [256]uint8
}

// ToneOptions are the inputs of a correction beyond the ink itself.
type ToneOptions struct {
	// Grow is how much coverage a stroke's edge gains, where the coverage is
	// half. Zero keeps the edges where the outlines put them.
	Grow float64
	// SyntheticBold says the glyphs are already drawn wider than their outlines,
	// because the face has no bold and the host fakes one. Such a run takes the
	// contrast and not the growth: an emboldened body grown twice closes its
	// counters.
	SyntheticBold bool
}

// toneContrast is the contrast DirectWrite's grayscale mode enhances with: it
// grows as the ink darkens, which is the enhanced contrast a browser's text is
// drawn with.
const toneContrast = 0.5

// NewTone builds the correction for ink.
//
// Direct2D's gamma ratios are deliberately not applied. Measured against this
// package's own output they cancel the contrast curve almost exactly — at half
// coverage the contrast takes 0.5 to 0.6 and a 1.8 gamma brings it back to
// 0.49 — so they would add a table of magic numbers without changing a pixel.
// The weight a reader compares is the growth.
func NewTone(ink paint.Color, opt ToneOptions) *Tone {
	r := float64(ink.R) / 255
	g := float64(ink.G) / 255
	b := float64(ink.B) / 255
	luma := 0.30*r + 0.59*g + 0.11*b
	// Dark ink gets the full contrast; pale ink, as a night paper's, none.
	k := toneContrast * math.Min(math.Max(3-4*luma, 0), 1)
	grow := opt.Grow
	if opt.SyntheticBold {
		grow = 0
	}
	t := new(Tone)
	for i := range t.lut {
		a := float64(i) / 255
		v := a * (k + 1) / (a*k + 1)
		// The growth peaks where the coverage is half, which is the edge of a
		// stroke, and vanishes at the paper and inside the ink: what a dilated
		// edge does, without moving the glyph.
		v = math.Min(v+grow*(1-math.Abs(2*v-1)), 1)
		t.lut[i] = uint8(math.Round(v * 255))
	}
	return t
}

// Coverage returns the corrected coverage of a.
func (t *Tone) Coverage(a uint8) uint8 {
	if t == nil {
		return a
	}
	return t.lut[a]
}

// Apply maps coverage in place. Paper (0) and ink interiors (255) are fixed
// points, so only the edges move.
func (t *Tone) Apply(cov []uint8) {
	if t == nil {
		return
	}
	for i, v := range cov {
		cov[i] = t.lut[v]
	}
}
