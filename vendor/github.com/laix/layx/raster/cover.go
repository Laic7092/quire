package raster

import (
	"math"

	"github.com/laix/layx/render/paint"
)

// edge is one side of a contour, already in device space. dir is +1 for a
// downward edge and -1 for an upward one; the non-zero winding rule only needs
// consistent signs.
type edge struct {
	x0, y0, x1, y1 float32
	// ymin and ymax are y0 and y1 sorted; dxdy is dx/dy, so a row's start x
	// needs one multiply instead of a division.
	ymin, ymax float32
	dxdy       float32
	dir        int8
}

// shapeEdges flattens every contour of s into edges. Horizontal edges are
// skipped: an edge that does not cross a row deposits nothing in it.
func shapeEdges(s *shape, edges []edge) []edge {
	for i := range s.starts {
		start, end := s.contourRange(i)
		if end-start < 3 {
			continue
		}
		for j := start; j < end; j++ {
			k := j + 1
			if k == end {
				k = start
			}
			a, b := s.pts[j], s.pts[k]
			if a.Y == b.Y || !finitePoint(a) || !finitePoint(b) {
				continue
			}
			e := edge{x0: a.X, y0: a.Y, x1: b.X, y1: b.Y, dir: 1}
			if b.Y < a.Y {
				e.dir = -1
			}
			e.ymin, e.ymax = a.Y, b.Y
			if e.ymin > e.ymax {
				e.ymin, e.ymax = e.ymax, e.ymin
			}
			e.dxdy = (b.X - a.X) / (b.Y - a.Y)
			edges = append(edges, e)
		}
	}
	return edges
}

// coverageScratch is the reusable working set of one rasterizer: alpha is the
// coverage of the last path, and cells holds the signed areas it is summed
// from, one width+2 row per pixel row, already in units of 255 so the sum can
// be stored as a byte without a scale factor.
//
// A row of cells is width+2: cell 0 collects every crossing left of the
// surface, so the winding entering the left edge still counts, cell 1..width
// are the pixels, and the last cell is a sink for crossings right of the
// surface, which no pixel inside it is left of.
type coverageScratch struct {
	alpha []uint8
	cells []float32
}

// rasterizeCoverage fills s.alpha with the fraction of each pixel covered by
// the non-zero winding of edges, one byte per pixel in row-major order.
//
// It integrates instead of sampling. Every edge deposits, for each pixel row it
// crosses, a signed area spread over the pixels that row's segment spans: the
// sum over a row is the row overlap dy times the edge's direction, and each
// pixel's part is the area of the segment's trapezoid inside it. Rows are then
// summed left to right, which turns those areas into the winding number at each
// pixel's left edge; its magnitude clamped to one is the pixel's coverage.
// Horizontal antialiasing is therefore exact, vertical antialiasing falls out of
// the row overlap, and nothing is supersampled.
//
// The work is proportional to the rows each edge crosses plus the surface once,
// not to the rows times the surface: a glyph's hundreds of edges touch only a
// few rows each.
//
// tone, when not nil, corrects each pixel's coverage on the way out (see Tone):
// the ink's weight is decided once per glyph instead of by a pass over the
// finished bitmap.
func rasterizeCoverage(edges []edge, area pixArea, s *coverageScratch, tone *Tone) []uint8 {
	width, height := area.width(), area.height()
	n := width * height
	if n <= 0 {
		s.alpha = s.alpha[:0]
		return nil
	}
	if cap(s.alpha) < n {
		s.alpha = make([]uint8, n)
	}
	alpha := s.alpha[:n]
	s.alpha = alpha
	if len(edges) == 0 {
		clear(alpha)
		return alpha
	}

	// base is the index of column area.X0-1 in a row, the carry cell.
	stride := width + 2
	if cap(s.cells) < stride*height {
		s.cells = make([]float32, stride*height)
	}
	cells := s.cells[:stride*height]
	s.cells = cells
	clear(cells)
	base := area.X0 - 1
	sink := stride - 1

	for i := range edges {
		e := &edges[i]

		y0 := clampRow(int(math.Floor(float64(e.ymin))), area.Y0, area.Y1)
		y1 := clampRow(int(math.Ceil(float64(e.ymax))), area.Y0, area.Y1)
		if y0 >= y1 {
			continue
		}

		// x is the edge's x where it enters the first visible row, which is the
		// edge's own start on the top row and the row's top after that.
		start := float32(y0)
		if start < e.ymin {
			start = e.ymin
		}
		x := e.x0 + (start-e.y0)*e.dxdy
		// Areas are 255 times the covered fraction, so a pixel's winding sum is
		// already the byte the coverage wants.
		dir := float32(e.dir) * 255
		line := cells[(y0-area.Y0)*stride:]

		for row := y0; row < y1; row++ {
			top := float32(row)
			dy := minf(top+1, e.ymax) - maxf(top, e.ymin)
			xNext := x + dy*e.dxdy
			deposit(line, base, sink, dy*dir, x, xNext)
			x = xNext
			line = line[stride:]
		}
	}

	// One row of this shape is the winding at each pixel's left edge, so the
	// row's cells are summed in place. Converting to bytes here, rather than
	// into a float coverage buffer for the caller to walk again, is one pass
	// over the pixels instead of two, and the tone rides along in the same pass.
	var lut *[256]uint8
	if tone != nil {
		lut = &tone.lut
	}
	for row := 0; row < height; row++ {
		line := cells[row*stride : row*stride+stride]
		out := alpha[row*width : row*width+width]
		acc := line[0]
		for x, v := range line[1 : 1+width] {
			acc += v
			// The magnitude of the winding, without a branch: clear the sign
			// bit. max(acc, -acc) is the same value but the builtin keeps the
			// NaN check, which costs a branch.
			a := math.Float32frombits(math.Float32bits(acc) & 0x7fffffff)
			if a > 255 {
				a = 255
			}
			v := uint8(a + 0.5)
			if lut != nil {
				v = lut[v]
			}
			out[x] = v
		}
	}
	return alpha
}

// deposit spreads d, the signed vertical overlap of a segment running from x to
// xNext inside one pixel row, over the pixels of that row. d is measured in
// coverage units of 255, matching the cells.
func deposit(line []float32, base, sink int, d, x, xNext float32) {
	x0, x1 := x, xNext
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	x0i := int(math.Floor(float64(x0)))
	x0Floor := float32(x0i)
	x1i := int(math.Ceil(float64(x1)))
	x1Ceil := float32(x1i)

	if x1i <= x0i+1 {
		// The segment stays within one pixel column: its area is split at its
		// mean x.
		xmf := 0.5*(x+xNext) - x0Floor
		line[cellIndex(x0i, base, sink)] += d - d*xmf
		line[cellIndex(x0i+1, base, sink)] += d * xmf
		return
	}

	// A trapezoid over several pixels: each one's part is the area between the
	// segment and the bottom edge of the row.
	sc := 1 / (x1 - x0)
	x0f := x0 - x0Floor
	a0 := 0.5 * sc * (1 - x0f) * (1 - x0f)
	x1f := x1 - x1Ceil + 1
	am := 0.5 * sc * x1f * x1f

	line[cellIndex(x0i, base, sink)] += d * a0
	if x1i == x0i+2 {
		line[cellIndex(x0i+1, base, sink)] += d * (1 - a0 - am)
	} else {
		a1 := sc * (1.5 - x0f)
		line[cellIndex(x0i+1, base, sink)] += d * (a1 - a0)

		// The pixels in between all get the same share. The loop is clipped to
		// the row, so a segment running far outside the surface still only
		// costs its visible part.
		dTimesS := d * sc
		from := base
		if x0i+2 > from {
			from = x0i + 2
		}
		to := sink
		if x1i-1 < to {
			to = x1i - 1
		}
		for xi := from; xi < to; xi++ {
			line[xi-base] += dTimesS
		}

		a2 := a1 + sc*float32(x1i-x0i-3)
		line[cellIndex(x1i-1, base, sink)] += d * (1 - a2 - am)
	}
	line[cellIndex(x1i, base, sink)] += d * am
}

// cellIndex maps a pixel column to its cell in a row: 0 for the carry, the
// pixel itself in between, and the row's sink for anything right of the
// surface.
func cellIndex(xi, base, sink int) int {
	j := xi - base
	if j < 0 {
		return 0
	}
	if j > sink {
		return sink
	}
	return j
}

func clampRow(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// edgesFor flattens s into the rasterizer's scratch slice.
func (r *Rasterizer) edgesFor(s *shape) []edge {
	r.edges = shapeEdges(s, r.edges[:0])
	return r.edges
}

// coverage rasterizes edges into the rasterizer's scratch slice, corrected for
// the ink's tone when tone is not nil.
func (r *Rasterizer) coverage(edges []edge, area pixArea, tone *Tone) []uint8 {
	return rasterizeCoverage(edges, area, &r.scratch, tone)
}

// fillShape rasterizes s with the non-zero winding rule and blends it into the
// surface through the current clip.
func (r *Rasterizer) fillShape(s *shape, c paint.Color, tone *Tone) {
	if c.A == 0 || len(s.starts) == 0 {
		return
	}
	b, ok := s.bounds()
	if !ok {
		return
	}
	area := intersectArea(areaOf(b, r.w, r.h), r.state.clip.area)
	if area.empty() {
		return
	}
	r.blendCoverage(area, r.coverage(r.edgesFor(s), area, tone), c)
}

// fillDeviceRect fills the axis-aligned rect, whose edges may fall anywhere in
// the pixel grid. A pixel's coverage is the area of the overlap of the pixel and
// the rect, which is the product of the row's overlap and the column's — so a
// row is one span of full columns plus at most two partial ones, and the span's
// coverage is the same for every pixel in it. That matters: a page's paper is
// one such rect covering the whole surface, and walking it a float multiply and
// three divides at a time is most of the cost of drawing a page of text.
func (r *Rasterizer) fillDeviceRect(rect paint.Rect, c paint.Color) {
	area := intersectArea(areaOf(rect, r.w, r.h), r.state.clip.area)
	if area.empty() {
		return
	}
	// The columns every row crosses fully, clipped to the area: the pixels
	// outside them are the fractional edges, which are blended one at a time.
	fx0 := int(math.Ceil(float64(rect.X)))
	fx1 := int(math.Floor(float64(rect.X + rect.Width)))
	x0, x1 := area.X0, area.X0
	if fx1 > fx0 {
		x0 = min(max(fx0, area.X0), area.X1)
		x1 = min(max(fx1, area.X0), area.X1)
	}
	xEnd, yEnd := rect.X+rect.Width, rect.Y+rect.Height
	mask := r.state.clip.mask
	for y := area.Y0; y < area.Y1; y++ {
		fy := spanOverlap(float32(y), float32(y+1), rect.Y, yEnd)
		if fy <= 0 {
			continue
		}
		if a := uint32(fy*255 + 0.5); x1 > x0 && a != 0 {
			if mask == nil {
				r.blendSpan(y, x0, x1, c, a)
			} else {
				r.blendSpanMasked(y, x0, x1, c, a, mask)
			}
		}
		// The partial columns: left of the span, then right of it.
		for _, span := range [2][2]int{{area.X0, x0}, {x1, area.X1}} {
			for x := span[0]; x < span[1]; x++ {
				fx := spanOverlap(float32(x), float32(x+1), rect.X, xEnd)
				a := uint32(fx*fy*255 + 0.5)
				if a == 0 {
					continue
				}
				if mask != nil {
					a = a * uint32(mask[y*r.w+x]) / 255
					if a == 0 {
						continue
					}
				}
				r.blend(x, y, c, a)
			}
		}
	}
}

// solidPattern is the 64-byte tile a solid span is written from: the pixel
// repeated, which turns filling a rectangle into the same memmove loop the
// runtime uses to clear memory — the widest store the CPU has, sixteen pixels at
// a time. Writing four bytes a pixel is six times slower.
type solidPattern [64]byte

// patternFor returns the tile for c, opaque, premultiplied (which for an opaque
// colour is the colour itself).
func patternFor(c paint.Color) solidPattern {
	var p solidPattern
	for i := 0; i < len(p); i += 4 {
		p[i], p[i+1], p[i+2], p[i+3] = c.R, c.G, c.B, 0xff
	}
	return p
}

// fillPattern writes pat over p, whose length is a multiple of four.
func fillPattern(p []byte, pat *solidPattern) {
	n := len(p) &^ 63
	for i := 0; i < n; i += 64 {
		copy(p[i:i+64], pat[:])
	}
	n += len(p[n:]) &^ 15
	for i := len(p) &^ 63; i < n; i += 16 {
		copy(p[i:i+16], pat[:16])
	}
	for i := n; i+4 <= len(p); i += 4 {
		copy(p[i:i+4], pat[:4])
	}
}

// blendSpan composites c with the same coverage a over row y, x0 to x1.
func (r *Rasterizer) blendSpan(y, x0, x1 int, c paint.Color, a uint32) {
	sa := a * uint32(c.A) / 255
	if sa == 0 {
		return
	}
	if sa == 255 {
		// Opaque: the pixels are the colour, whatever was under them.
		pat := patternFor(c)
		i := r.img.PixOffset(x0+r.ox, y+r.oy)
		fillPattern(r.img.Pix[i:i+(x1-x0)*4], &pat)
		return
	}
	inv := 255 - sa
	br := uint32(c.R)*sa + 127
	bg := uint32(c.G)*sa + 127
	bb := uint32(c.B)*sa + 127
	ba := 255*sa + 127
	i := r.img.PixOffset(x0+r.ox, y+r.oy)
	p := r.img.Pix[i : i+(x1-x0)*4]
	for k := 0; k < len(p); k += 4 {
		p[k] = uint8(div255(br + uint32(p[k])*inv))
		p[k+1] = uint8(div255(bg + uint32(p[k+1])*inv))
		p[k+2] = uint8(div255(bb + uint32(p[k+2])*inv))
		p[k+3] = uint8(div255(ba + uint32(p[k+3])*inv))
	}
}

// blendSpanMasked is blendSpan under a coverage mask, which gives every pixel
// its own coverage: rare (only inside a rotated or fractional clip) and slower
// by the mask lookup.
func (r *Rasterizer) blendSpanMasked(y, x0, x1 int, c paint.Color, a uint32, mask []uint8) {
	for x := x0; x < x1; x++ {
		if m := a * uint32(mask[y*r.w+x]) / 255; m != 0 {
			r.blend(x, y, c, m)
		}
	}
}

// div255 is x/255 rounded, exact for x up to 255*255+127: the two shifts are
// enough because 255 is one below a power of two, which is also why the four
// divides this replaces were worth replacing.
func div255(x uint32) uint32 {
	x += 128
	return (x + (x >> 8)) >> 8
}

// blendCoverage blends color c through a coverage buffer for area. The
// coverage of a glyph is what most of the blending in a page of text is, so the
// row's pixel offset and the ink's premultiplied terms are hoisted out of the
// pixel loop, and the coverage scale is skipped entirely for an opaque ink.
func (r *Rasterizer) blendCoverage(area pixArea, cov []uint8, c paint.Color) {
	width := area.width()
	mask := r.state.clip.mask
	ca := uint32(c.A)
	cr, cg, cb := uint32(c.R), uint32(c.G), uint32(c.B)
	opaque := ca == 255
	for y := area.Y0; y < area.Y1; y++ {
		rowOff := (y - area.Y0) * width
		var mrow []uint8
		if mask != nil {
			mrow = mask[y*r.w+area.X0 : y*r.w+area.X1]
		}
		i := r.img.PixOffset(area.X0+r.ox, y+r.oy)
		p := r.img.Pix[i : i+width*4 : i+width*4]
		for k, cv := range cov[rowOff : rowOff+width] {
			if cv == 0 {
				continue
			}
			sa := uint32(cv)
			if !opaque || mrow != nil {
				if mrow != nil {
					sa = sa * uint32(mrow[k]) / 255
					if sa == 0 {
						continue
					}
				}
				if !opaque {
					sa = sa * ca / 255
					if sa == 0 {
						continue
					}
				}
			}
			inv := 255 - sa
			q := p[k*4 : k*4+4]
			q[0] = uint8(div255(cr*sa + uint32(q[0])*inv))
			q[1] = uint8(div255(cg*sa + uint32(q[1])*inv))
			q[2] = uint8(div255(cb*sa + uint32(q[2])*inv))
			q[3] = uint8(div255(255*sa + uint32(q[3])*inv))
		}
	}
}

// blend composites a non-premultiplied color with 0..255 coverage a onto the
// premultiplied surface.
func (r *Rasterizer) blend(x, y int, c paint.Color, a uint32) {
	sa := a * uint32(c.A) / 255
	if sa == 0 {
		return
	}
	inv := 255 - sa
	i := r.img.PixOffset(x+r.ox, y+r.oy)
	p := r.img.Pix[i : i+4]

	p[0] = uint8(div255(uint32(c.R)*sa + uint32(p[0])*inv))
	p[1] = uint8(div255(uint32(c.G)*sa + uint32(p[1])*inv))
	p[2] = uint8(div255(uint32(c.B)*sa + uint32(p[2])*inv))
	p[3] = uint8(div255(255*sa + uint32(p[3])*inv))
}
