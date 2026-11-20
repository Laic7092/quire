// Package raster is a pure-Go software rasterizer for layx display lists.
//
// A Rasterizer implements [paint.Painter] and paints into an image.RGBA. It is
// the reference drawing backend for tests, snapshot images, e-ink drivers and
// any host that does not want to run a GPU: layx owns shaping and layout, this
// package owns coverage and blending.
//
// Coordinates are device pixels with the origin at the top-left of the
// surface. Glyphs are rasterized from the font's own outlines; paint.Font.Data
// is parsed with go-text/typesetting and cached per face. Fills and glyphs are
// anti-aliased with eight vertical samples and exact horizontal span coverage.
// Clipping is kept as a coverage mask, so a transformed or rotated clip keeps
// its soft edge.
//
// The package deliberately stops at "pixels in an image.RGBA": it has no font
// fallback, no shaping, no colour-font or SVG glyph support and no gamma
// correction, and a Rasterizer is not safe for concurrent use. Colors are
// blended in premultiplied sRGB space.
package raster

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/go-text/typesetting/font"
	"github.com/laix/layx/render/paint"
)

// Rasterizer draws a paint display list into an image.RGBA.
//
// The zero value is not usable; construct one with New or NewFromImage.
type Rasterizer struct {
	img    *image.RGBA
	ox, oy int
	w, h   int

	state state
	stack []state

	faces    map[faceKey]faceEntry
	outlines map[outlineKey][]font.Segment
	images   map[paint.ImageID]image.Image

	scratch coverageScratch
	edges   []edge
	path    shape

	// ToneFor, when set, supplies the ink correction for a glyph run: the
	// coverage of each glyph is mapped through it before it is blended, so the
	// text is printed with the weight a system printer gives it (see Tone). nil
	// draws the outlines' own coverage.
	//
	// The host owns the policy — which ink, which band of sizes, whether the run
	// is a synthetic bold — and returns the same Tone for every run that shares
	// them; building one per run is a table of 256 and answers for the whole run.
	ToneFor func(run paint.GlyphRun) *Tone
}

type state struct {
	m    paint.Affine
	clip clipState
}

type clipState struct {
	// area is the bounding box of pixels that may still be visible, in surface
	// coordinates.
	area pixArea
	// mask is the per-pixel clip coverage over the whole surface. nil means
	// area is fully visible (the common integer rectangle clip).
	mask []uint8
}

var (
	_ paint.Painter = (*Rasterizer)(nil)
	_ paint.Clipped = (*Rasterizer)(nil)
)

// New returns a rasterizer drawing into a new transparent RGBA surface of the
// given size.
func New(width, height int) *Rasterizer {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	return NewFromImage(image.NewRGBA(image.Rect(0, 0, width, height)))
}

// NewFromImage returns a rasterizer drawing into img. img may have a non-zero
// bounds origin; drawing coordinates are still relative to the top-left corner
// of the image.
//
// The rasterizer does not own img: the caller may keep it and write it out.
// A nil image is treated as an empty surface.
func NewFromImage(img *image.RGBA) *Rasterizer {
	if img == nil {
		img = image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	b := img.Bounds()
	r := &Rasterizer{
		img: img,
		ox:  b.Min.X,
		oy:  b.Min.Y,
		w:   b.Dx(),
		h:   b.Dy(),
	}
	r.state.m = paint.Identity()
	r.state.clip.area = pixArea{X0: 0, Y0: 0, X1: r.w, Y1: r.h}
	return r
}

// Image returns the surface the rasterizer draws into.
func (r *Rasterizer) Image() *image.RGBA { return r.img }

// Size returns the surface size in pixels.
func (r *Rasterizer) Size() (width, height int) { return r.w, r.h }

// Clear fills the whole surface with c, ignoring the current transform and
// clip. A transparent color produces a transparent surface.
func (r *Rasterizer) Clear(c paint.Color) {
	if r.w == 0 || r.h == 0 {
		return
	}
	uniform := &image.Uniform{C: color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A}}
	draw.Draw(r.img, r.img.Bounds(), uniform, image.Point{}, draw.Src)
}

// Reset clears the surface to transparent and restores the identity transform,
// the full-surface clip and an empty save stack. Registered images and parsed
// fonts are kept.
func (r *Rasterizer) Reset() {
	r.state = state{m: paint.Identity()}
	r.state.clip.area = pixArea{X0: 0, Y0: 0, X1: r.w, Y1: r.h}
	r.stack = r.stack[:0]
	r.Clear(paint.Color{})
}

// SetImage registers src under id, replacing any previous mapping. A nil image
// removes the mapping. DrawImage ignores unknown ids.
func (r *Rasterizer) SetImage(id paint.ImageID, src image.Image) {
	if src == nil {
		delete(r.images, id)
		return
	}
	if r.images == nil {
		r.images = make(map[paint.ImageID]image.Image)
	}
	r.images[id] = src
}

// Save implements paint.Painter.
func (r *Rasterizer) Save() {
	r.stack = append(r.stack, r.state)
}

// Restore implements paint.Painter.
func (r *Rasterizer) Restore() {
	if n := len(r.stack); n > 0 {
		r.state = r.stack[n-1]
		r.stack = r.stack[:n-1]
	}
}

// Transform implements paint.Painter. Transforms compose with the current one.
func (r *Rasterizer) Transform(m paint.Affine) {
	r.state.m = combine(r.state.m, m)
}

// FillRect implements paint.Painter.
func (r *Rasterizer) FillRect(rc paint.Rect, c paint.Color) {
	if rc.Empty() || c.A == 0 {
		return
	}
	q := transformRect(r.state.m, rc)
	if axis, ok := axisAlignedRect(q); ok {
		r.fillDeviceRect(axis, c)
		return
	}
	var s shape
	addQuadShape(&s, q)
	r.fillShape(&s, c, nil)
}

// StrokeRect implements paint.Painter. The stroke is centered on the rectangle
// edges; it is drawn as one ring so overlapping edges are not blended twice.
func (r *Rasterizer) StrokeRect(rc paint.Rect, c paint.Color, width float32) {
	if rc.Empty() || c.A == 0 || width <= 0 {
		return
	}
	half := width / 2
	outer := paint.Rect{
		X:      rc.X - half,
		Y:      rc.Y - half,
		Width:  rc.Width + width,
		Height: rc.Height + width,
	}
	var s shape
	addQuadShape(&s, transformRect(r.state.m, outer))

	if rc.Width > width && rc.Height > width {
		inner := paint.Rect{
			X:      rc.X + half,
			Y:      rc.Y + half,
			Width:  rc.Width - width,
			Height: rc.Height - width,
		}
		var hole shape
		addQuadShape(&hole, transformRect(r.state.m, inner))
		// The hole must wind opposite to the outer contour. The transform may
		// mirror the geometry, so compare the signed areas instead of assuming
		// a fixed contour order.
		if signedArea(s.pts)*signedArea(hole.pts) > 0 {
			hole.reverse(0)
		}
		s.addShape(&hole)
	}
	r.fillShape(&s, c, nil)
}

// FillRoundRect implements paint.Painter. The radius is clamped to half the
// smaller side, matching the Canvas 2D path used by the browser backend.
func (r *Rasterizer) FillRoundRect(rc paint.Rect, radius float32, c paint.Color) {
	if rc.Empty() || c.A == 0 {
		return
	}
	radius = minf(radius, rc.Width/2)
	radius = minf(radius, rc.Height/2)
	if radius <= 0 {
		r.FillRect(rc, c)
		return
	}
	var s shape
	b := newBuilder(&s, func(x, y float32) point {
		return apply(r.state.m, point{x, y})
	})
	addRoundRect(b, rc, radius)
	r.fillShape(&s, c, nil)
}
