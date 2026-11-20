package raster

import (
	"math"

	"github.com/laix/layx/render/paint"
)

// ClipRect implements paint.Painter.
//
// An integer, axis-aligned rectangle (what layx emits for a page or tile) only
// narrows the visible bounds, which is exact and free. Anything else - a
// fractional edge, a scale, a rotation - becomes a per-pixel coverage mask, so
// the clip is anti-aliased like the geometry it clips.
func (r *Rasterizer) ClipRect(rc paint.Rect) {
	if rc.Empty() {
		r.state.clip = clipState{}
		return
	}
	q := transformRect(r.state.m, rc)
	if axis, ok := axisAlignedRect(q); ok &&
		isIntegral(axis.X) && isIntegral(axis.Y) &&
		isIntegral(axis.X+axis.Width) && isIntegral(axis.Y+axis.Height) {
		x1 := int(math.Round(float64(axis.X + axis.Width)))
		y1 := int(math.Round(float64(axis.Y + axis.Height)))
		snap := pixArea{
			X0: int(math.Round(float64(axis.X))),
			Y0: int(math.Round(float64(axis.Y))),
			X1: x1,
			Y1: y1,
		}
		r.state.clip.area = intersectArea(r.state.clip.area, snap)
		return
	}

	// Build a mask. The previous mask is never mutated: a Save/Restore may
	// still refer to it.
	x0, y0, x1, y1 := quadBounds(q)
	area := intersectArea(
		areaOf(paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, r.w, r.h),
		r.state.clip.area,
	)
	if area.empty() {
		r.state.clip = clipState{}
		return
	}

	var s shape
	addQuadShape(&s, q)
	cov := r.coverage(r.edgesFor(&s), area, nil)

	mask := make([]uint8, r.w*r.h)
	old := r.state.clip.mask
	width := area.width()
	for y := area.Y0; y < area.Y1; y++ {
		rowOff := (y - area.Y0) * width
		base := y * r.w
		for x := area.X0; x < area.X1; x++ {
			v := uint32(cov[rowOff+x-area.X0])
			if v == 0 {
				continue
			}
			if old != nil {
				v = v * uint32(old[base+x]) / 255
			}
			if v == 0 {
				continue
			}
			mask[base+x] = uint8(v)
		}
	}
	r.state.clip = clipState{area: area, mask: mask}
}

// Visible implements paint.Clipped.
//
// The result is in the current user space, not device space, because the
// display list it culls is in user space. It is deliberately one pixel
// generous: anti-aliasing can put ink just outside a geometric rectangle, and
// culling an op that would have painted is much worse than drawing one extra.
func (r *Rasterizer) Visible() paint.Rect {
	area := r.state.clip.area
	if area.empty() {
		return paint.Rect{}
	}
	inv, ok := invert(r.state.m)
	if !ok {
		return paint.Rect{}
	}
	device := paint.Rect{
		X:      float32(area.X0) - 1,
		Y:      float32(area.Y0) - 1,
		Width:  float32(area.width()) + 2,
		Height: float32(area.height()) + 2,
	}
	x0, y0, x1, y1 := quadBounds(transformRect(inv, device))
	return paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}
