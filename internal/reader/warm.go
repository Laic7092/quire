package reader

import (
	"math"

	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// WarmTile rasterizes the coverage of the glyphs a tile draws, and nothing
// else: no shapes, no images, no tile pixels, no builder. It is how a scrolling
// window keeps the frame's cold glyphs off the frame — a thread the frame does
// not wait for walks the tiles the column is heading for and fills the coverage
// cache the window's painter shares (see glyphcover.go), so the frame that
// bakes those tiles finds every glyph there and only copies it.
//
// The glyphs are keyed by the place their origin falls on the device grid,
// which depends on where the tile's top edge lands in the canvas it is drawn
// into, so the warmer takes what the canvas path gives it: strip is that edge,
// as drawTileCanvas places it, and zoom the device pixels a DIP is drawn at.
func (u *UIPainter) WarmTile(tile *render.Tile, strip, zoom float32) {
	if u == nil || tile == nil || !(zoom > 0) {
		return
	}
	prevZoom, prevDPR, prevCull, prevWarm := u.zoom, u.dpr, u.noCull, u.warming
	prevX, prevY := u.dx, u.dy
	// Warming draws nothing, so nothing below the clip can be skipped: every
	// run of the tile is warmed, whether the frame would show it or not.
	u.zoom, u.dpr, u.noCull, u.warming = zoom, zoom, true, true
	u.dx, u.dy = 0, strip
	tile.Draw(warmPainter{u})
	u.zoom, u.dpr, u.noCull, u.warming = prevZoom, prevDPR, prevCull, prevWarm
	u.dx, u.dy = prevX, prevY
}

// warmPainter replays a display list for its glyphs alone. Everything that
// would draw, and everything that would move where the next op lands, does
// nothing: a tile's display list draws in its own coordinates, and the glyphs
// are what the frame will need.
type warmPainter struct{ u *UIPainter }

func (w warmPainter) Save()                                                   {}
func (w warmPainter) Restore()                                                {}
func (w warmPainter) ClipRect(paint.Rect)                                     {}
func (w warmPainter) Transform(paint.Affine)                                  {}
func (w warmPainter) FillRect(paint.Rect, paint.Color)                        {}
func (w warmPainter) StrokeRect(paint.Rect, paint.Color, float32)             {}
func (w warmPainter) FillRoundRect(paint.Rect, float32, paint.Color)          {}
func (w warmPainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) {}
func (w warmPainter) DrawGlyphs(run paint.GlyphRun)                           { w.u.warmGlyphs(run) }

// warmGlyphs rasterizes what one run's glyphs will need: the same face, size,
// ratio, phase, ink correction and synthetic bold that the frame's atlas keys
// them by, so the frame finds them where it looks. A glyph the shared cache
// holds, or has already found to be blank, costs a lookup.
func (u *UIPainter) warmGlyphs(run paint.GlyphRun) {
	if run.Font == nil || len(run.Glyphs) == 0 || run.Color.Transparent() || run.Size <= 0 {
		return
	}
	run.Y = u.baseline(run.Y)
	gf := u.glyphFace(run.Font)
	if gf == nil {
		return
	}
	upem := float32(gf.face.Upem())
	if upem <= 0 {
		return
	}
	dpr := u.scale()
	if !(dpr > 0) {
		dpr = 1
	}
	size := float32(math.Round(float64(run.Size)*2)) / 2
	bold := run.EmulateBold && !fakeBoldOff
	tone := u.tonesOf().toneFor(run.Color, run.Size, bold)
	ox := float64(u.dx+run.X) * float64(dpr)
	oy := float64(u.dy+run.Y) * float64(dpr)
	a := u.atlas()
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		_, px := glyphPhase(ox + float64(g.X)*float64(dpr))
		_, py := glyphPhase(oy + float64(g.Y)*float64(dpr))
		key := glyphKey{face: gf, gid: g.ID, size: size, dpr: dpr, phase: uint8(px | py<<2), tone: tone, bold: bold}
		if _, ok := a.index[key]; ok {
			continue
		}
		a.cover(gf, g.ID, key, size, dpr, px, py, upem, tone, bold)
	}
}
