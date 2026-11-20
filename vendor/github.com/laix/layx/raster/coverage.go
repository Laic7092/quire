package raster

// Coverage is a reusable rasterizer for one path in device space. It is the
// low-level coverage engine behind Rasterizer's fills and glyphs, exposed for
// hosts that keep their own glyph cache or bitmap atlas and only need the
// anti-aliased mask, not a whole paint.Painter.
//
// The path is filled with the non-zero winding rule. Contours are implicitly
// closed: a MoveTo starts a new one, and every contour is closed when Alpha is
// called. Curves are flattened in device space with the same tolerance as the
// rest of the package.
//
// A Coverage is not safe for concurrent use, and Alpha's result is only valid
// until the next call on the same Coverage.
type Coverage struct {
	w, h    int
	shape   shape
	edges   []edge
	scratch coverageScratch
	tone    *Tone
}

// NewCoverage returns an empty coverage rasterizer. Reset sizes it before the
// first path.
func NewCoverage() *Coverage { return &Coverage{} }

// Reset clears the path and sizes the coverage buffer to w by h pixels. A
// non-positive size makes Alpha return nil.
func (c *Coverage) Reset(w, h int) {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	c.w, c.h = w, h
	c.shape.reset()
}

// MoveTo starts a new contour at (x, y) in device pixels.
func (c *Coverage) MoveTo(x, y float32) {
	c.shape.moveTo(point{x, y})
}

// LineTo appends a line to (x, y). Without a preceding MoveTo it starts a
// contour there.
func (c *Coverage) LineTo(x, y float32) {
	if len(c.shape.pts) == 0 {
		c.MoveTo(x, y)
		return
	}
	c.shape.lineTo(point{x, y})
}

// QuadTo appends a quadratic Bezier with control (cx, cy) ending at (x, y).
func (c *Coverage) QuadTo(cx, cy, x, y float32) {
	if len(c.shape.pts) == 0 {
		c.MoveTo(cx, cy)
	}
	cur := c.shape.pts[len(c.shape.pts)-1]
	flattenQuad(&c.shape, cur, point{cx, cy}, point{x, y}, 0)
}

// CubeTo appends a cubic Bezier with controls (c1x, c1y) and (c2x, c2y) ending
// at (x, y).
func (c *Coverage) CubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	if len(c.shape.pts) == 0 {
		c.MoveTo(c1x, c1y)
	}
	cur := c.shape.pts[len(c.shape.pts)-1]
	flattenCube(&c.shape, cur, point{c1x, c1y}, point{c2x, c2y}, point{x, y}, 0)
}

// SetTone corrects the coverage Alpha returns with t, so a host's own glyph
// atlas prints the same ink weight DrawGlyphs does. A nil tone draws the
// outlines' own coverage. The setting is kept across Reset; a host that caches
// bitmaps per ink sets it before each Alpha.
func (c *Coverage) SetTone(t *Tone) { c.tone = t }

// ClosePath closes the current contour. Every contour is closed when Alpha
// runs anyway, so this exists for callers written against a path rasterizer
// that needs the explicit call.
func (c *Coverage) ClosePath() {}

// Alpha returns the coverage of the path, one byte per pixel, row-major, w
// bytes to a row. The slice is owned by the Coverage and stays valid until the
// next call on it. Pixels outside the path are zero.
func (c *Coverage) Alpha() []uint8 {
	if c.w <= 0 || c.h <= 0 {
		return nil
	}
	c.edges = shapeEdges(&c.shape, c.edges[:0])
	return rasterizeCoverage(c.edges, pixArea{X0: 0, Y0: 0, X1: c.w, Y1: c.h}, &c.scratch, c.tone)
}
