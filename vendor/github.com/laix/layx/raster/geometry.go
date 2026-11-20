package raster

import (
	"math"

	"github.com/laix/layx/render/paint"
)

// point is a position in device pixels.
type point struct{ X, Y float32 }

// pixArea is a half-open rectangle of pixels: [X0,X1) x [Y0,Y1).
type pixArea struct{ X0, Y0, X1, Y1 int }

func (a pixArea) empty() bool { return a.X0 >= a.X1 || a.Y0 >= a.Y1 }
func (a pixArea) width() int  { return a.X1 - a.X0 }
func (a pixArea) height() int { return a.Y1 - a.Y0 }

func intersectArea(a, b pixArea) pixArea {
	if a.X0 < b.X0 {
		a.X0 = b.X0
	}
	if a.Y0 < b.Y0 {
		a.Y0 = b.Y0
	}
	if a.X1 > b.X1 {
		a.X1 = b.X1
	}
	if a.Y1 > b.Y1 {
		a.Y1 = b.Y1
	}
	if a.empty() {
		return pixArea{}
	}
	return a
}

// areaOf returns the pixels a device-space rectangle can touch, clamped to a
// w x h surface. Rectangles with no area (including NaN geometry produced by a
// degenerate transform) yield an empty area.
func areaOf(r paint.Rect, w, h int) pixArea {
	if !(r.Width > 0) || !(r.Height > 0) {
		return pixArea{}
	}
	x0 := math.Floor(float64(r.X))
	y0 := math.Floor(float64(r.Y))
	x1 := math.Ceil(float64(r.X + r.Width))
	y1 := math.Ceil(float64(r.Y + r.Height))
	if isNaN(x0) || isNaN(y0) || isNaN(x1) || isNaN(y1) {
		return pixArea{}
	}
	return pixArea{
		X0: clampPixel(x0, w),
		Y0: clampPixel(y0, h),
		X1: clampPixel(x1, w),
		Y1: clampPixel(y1, h),
	}
}

func clampPixel(v float64, max int) int {
	if v <= 0 {
		return 0
	}
	if v >= float64(max) {
		return max
	}
	return int(v)
}

func isNaN(v float64) bool { return v != v }

func apply(m paint.Affine, p point) point {
	return point{
		X: m.A*p.X + m.C*p.Y + m.E,
		Y: m.B*p.X + m.D*p.Y + m.F,
	}
}

// combine returns a then b: the transform that applies b in a's space.
func combine(a, b paint.Affine) paint.Affine {
	return paint.Affine{
		A: a.A*b.A + a.C*b.B,
		B: a.B*b.A + a.D*b.B,
		C: a.A*b.C + a.C*b.D,
		D: a.B*b.C + a.D*b.D,
		E: a.A*b.E + a.C*b.F + a.E,
		F: a.B*b.E + a.D*b.F + a.F,
	}
}

func invert(m paint.Affine) (paint.Affine, bool) {
	det := m.A*m.D - m.B*m.C
	if det == 0 || isNaN(float64(det)) || math.IsInf(float64(det), 0) {
		return paint.Identity(), false
	}
	inv := 1 / det
	return paint.Affine{
		A: m.D * inv,
		B: -m.B * inv,
		C: -m.C * inv,
		D: m.A * inv,
		E: (m.C*m.F - m.D*m.E) * inv,
		F: (m.B*m.E - m.A*m.F) * inv,
	}, true
}

// transformRect maps the four corners of r through m, clockwise from the
// top-left in user space.
func transformRect(m paint.Affine, r paint.Rect) [4]point {
	x0, y0 := r.X, r.Y
	x1, y1 := r.X+r.Width, r.Y+r.Height
	return [4]point{
		apply(m, point{x0, y0}),
		apply(m, point{x1, y0}),
		apply(m, point{x1, y1}),
		apply(m, point{x0, y1}),
	}
}

// quadBounds returns the bounding box of a transformed rectangle.
func quadBounds(q [4]point) (x0, y0, x1, y1 float32) {
	x0, x1 = q[0].X, q[0].X
	y0, y1 = q[0].Y, q[0].Y
	for _, p := range q[1:] {
		x0 = minf(x0, p.X)
		x1 = maxf(x1, p.X)
		y0 = minf(y0, p.Y)
		y1 = maxf(y1, p.Y)
	}
	return x0, y0, x1, y1
}

// axisAlignedRect reports whether q is an axis-aligned rectangle and returns
// it. The tolerance is in device pixels.
func axisAlignedRect(q [4]point) (paint.Rect, bool) {
	const eps = 1e-3
	if absf(q[0].Y-q[1].Y) > eps || absf(q[2].Y-q[3].Y) > eps ||
		absf(q[0].X-q[3].X) > eps || absf(q[1].X-q[2].X) > eps {
		return paint.Rect{}, false
	}
	x0, y0, x1, y1 := quadBounds(q)
	return paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, true
}

func isIntegral(v float32) bool {
	d := v - float32(math.Round(float64(v)))
	return d > -1e-3 && d < 1e-3
}

// spanOverlap returns the overlap of [a0,a1) and [b0,b1).
func spanOverlap(a0, a1, b0, b1 float32) float32 {
	if b0 >= a1 || b1 <= a0 {
		return 0
	}
	lo := maxf(a0, b0)
	hi := minf(a1, b1)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func absf(a float32) float32 {
	if a < 0 {
		return -a
	}
	return a
}

func finitePoint(p point) bool {
	fx, fy := float64(p.X), float64(p.Y)
	return !isNaN(fx) && !isNaN(fy) && !math.IsInf(fx, 0) && !math.IsInf(fy, 0)
}

// shape is a set of contours in device space. A contour starts at a point
// index in starts and ends at the next start (or the end of pts); it is
// implicitly closed. Keeping points and starts in flat slices lets a glyph be
// flattened without per-contour allocations.
type shape struct {
	pts    []point
	starts []int
}

func (s *shape) reset() {
	s.pts = s.pts[:0]
	s.starts = s.starts[:0]
}

func (s *shape) moveTo(p point) {
	s.starts = append(s.starts, len(s.pts))
	s.pts = append(s.pts, p)
}

func (s *shape) lineTo(p point) {
	s.pts = append(s.pts, p)
}

func (s *shape) contourRange(i int) (start, end int) {
	start = s.starts[i]
	if i+1 < len(s.starts) {
		return start, s.starts[i+1]
	}
	return start, len(s.pts)
}

func (s *shape) addShape(o *shape) {
	off := len(s.pts)
	for _, start := range o.starts {
		s.starts = append(s.starts, off+start)
	}
	s.pts = append(s.pts, o.pts...)
}

// reverse reverses contour i in place, turning a hole into a hole of the
// opposite winding when the shape is filled with the non-zero rule.
func (s *shape) reverse(i int) {
	start, end := s.contourRange(i)
	for a, b := start, end-1; a < b; a, b = a+1, b-1 {
		s.pts[a], s.pts[b] = s.pts[b], s.pts[a]
	}
}

func (s *shape) bounds() (paint.Rect, bool) {
	if len(s.pts) == 0 {
		return paint.Rect{}, false
	}
	x0, y0 := s.pts[0].X, s.pts[0].Y
	x1, y1 := x0, y0
	for _, p := range s.pts[1:] {
		if p.X < x0 {
			x0 = p.X
		}
		if p.X > x1 {
			x1 = p.X
		}
		if p.Y < y0 {
			y0 = p.Y
		}
		if p.Y > y1 {
			y1 = p.Y
		}
	}
	if !finitePoint(point{x0, y0}) || !finitePoint(point{x1, y1}) {
		return paint.Rect{}, false
	}
	return paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, true
}

func signedArea(pts []point) float64 {
	var sum float64
	for i := range pts {
		j := i + 1
		if j == len(pts) {
			j = 0
		}
		sum += float64(pts[i].X)*float64(pts[j].Y) - float64(pts[j].X)*float64(pts[i].Y)
	}
	return sum / 2
}

func addQuadShape(s *shape, q [4]point) {
	s.moveTo(q[0])
	s.lineTo(q[1])
	s.lineTo(q[2])
	s.lineTo(q[3])
}

// builder appends a path to a shape. Coordinates are given in the source space
// (user space for boxes, font units for glyphs) and xf maps them to device
// space; curves are flattened after the transform so the tolerance is in
// device pixels.
type builder struct {
	s   *shape
	xf  func(x, y float32) point
	cur point
	has bool
}

func newBuilder(s *shape, xf func(x, y float32) point) *builder {
	return &builder{s: s, xf: xf}
}

func (b *builder) moveTo(x, y float32) {
	b.cur = b.xf(x, y)
	b.s.moveTo(b.cur)
	b.has = true
}

func (b *builder) lineTo(x, y float32) {
	if !b.has {
		b.moveTo(x, y)
		return
	}
	b.cur = b.xf(x, y)
	b.s.lineTo(b.cur)
}

func (b *builder) quadTo(cx, cy, x, y float32) {
	if !b.has {
		b.moveTo(cx, cy)
	}
	c := b.xf(cx, cy)
	p := b.xf(x, y)
	flattenQuad(b.s, b.cur, c, p, 0)
	b.cur = p
}

func (b *builder) cubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	if !b.has {
		b.moveTo(c1x, c1y)
	}
	c1 := b.xf(c1x, c1y)
	c2 := b.xf(c2x, c2y)
	p := b.xf(x, y)
	flattenCube(b.s, b.cur, c1, c2, p, 0)
	b.cur = p
}

// flattenTol is the maximum deviation of a flattened curve from the true
// curve, in device pixels, and flattenTol2 its square, so the subdivision test
// can compare squared distances and skip the square roots.
const flattenTol = 0.2

const flattenTol2 = flattenTol * flattenTol

const flattenDepth = 16

func flattenQuad(s *shape, p0, c, p1 point, depth int) {
	if depth >= flattenDepth || withinTol(c, p0, p1) {
		s.lineTo(p1)
		return
	}
	p01 := midpoint(p0, c)
	p12 := midpoint(c, p1)
	mid := midpoint(p01, p12)
	flattenQuad(s, p0, p01, mid, depth+1)
	flattenQuad(s, mid, p12, p1, depth+1)
}

func flattenCube(s *shape, p0, c1, c2, p1 point, depth int) {
	if depth >= flattenDepth {
		s.lineTo(p1)
		return
	}
	if withinTol(c1, p0, p1) && withinTol(c2, p0, p1) {
		s.lineTo(p1)
		return
	}
	p01 := midpoint(p0, c1)
	p12 := midpoint(c1, c2)
	p23 := midpoint(c2, p1)
	p012 := midpoint(p01, p12)
	p123 := midpoint(p12, p23)
	mid := midpoint(p012, p123)
	flattenCube(s, p0, p01, p012, mid, depth+1)
	flattenCube(s, mid, p123, p23, p1, depth+1)
}

func midpoint(a, b point) point {
	return point{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
}

// withinTol reports whether p is within flattenTol of the segment a-b. The
// distance is |cross| / |b-a|, so the test squares both sides instead of taking
// two square roots: cross^2 <= tol^2 * |b-a|^2.
func withinTol(p, a, b point) bool {
	dx, dy := b.X-a.X, b.Y-a.Y
	px, py := p.X-a.X, p.Y-a.Y
	len2 := dx*dx + dy*dy
	if len2 == 0 {
		return px*px+py*py <= flattenTol2
	}
	cross := px*dy - py*dx
	return cross*cross <= flattenTol2*len2
}

// roundRectCorner is the cubic control-point ratio for a quarter circle.
const roundRectCorner = 0.5522847498307936

// addRoundRect appends a rounded rectangle in user space. The arcs are cubics,
// so they flatten correctly under any transform.
func addRoundRect(b *builder, r paint.Rect, radius float32) {
	x, y := r.X, r.Y
	w, h := r.Width, r.Height
	k := radius * roundRectCorner

	b.moveTo(x+radius, y)
	b.lineTo(x+w-radius, y)
	b.cubeTo(x+w-radius+k, y, x+w, y+radius-k, x+w, y+radius)
	b.lineTo(x+w, y+h-radius)
	b.cubeTo(x+w, y+h-radius+k, x+w-radius+k, y+h, x+w-radius, y+h)
	b.lineTo(x+radius, y+h)
	b.cubeTo(x+radius-k, y+h, x, y+h-radius+k, x, y+h-radius)
	b.lineTo(x, y+radius)
	b.cubeTo(x, y+radius-k, x+radius-k, y, x+radius, y)
}
