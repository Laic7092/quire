package raster

import (
	"image"
	"image/color"
	"math"

	"github.com/laix/layx/render/paint"
)

// DrawImage implements paint.Painter.
//
// The destination rectangle is mapped through the current transform, then each
// covered device pixel is mapped back into source space and sampled
// bilinearly. Only linear affine transforms are supported, which is all
// paint.Affine can express.
func (r *Rasterizer) DrawImage(id paint.ImageID, dst paint.Rect, _ paint.ImageOptions) {
	src := r.images[id]
	if src == nil || dst.Empty() || dst.Width <= 0 || dst.Height <= 0 {
		return
	}
	invM, ok := invert(r.state.m)
	if !ok {
		return
	}
	q := transformRect(r.state.m, dst)
	x0, y0, x1, y1 := quadBounds(q)
	area := intersectArea(
		areaOf(paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, r.w, r.h),
		r.state.clip.area,
	)
	if area.empty() {
		return
	}

	sbounds := src.Bounds()
	sw, sh := sbounds.Dx(), sbounds.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}

	for y := area.Y0; y < area.Y1; y++ {
		rowBase := y * r.w
		for x := area.X0; x < area.X1; x++ {
			inv := apply(invM, point{float32(x) + 0.5, float32(y) + 0.5})
			u := (inv.X - dst.X) / dst.Width
			v := (inv.Y - dst.Y) / dst.Height
			if u < 0 || u >= 1 || v < 0 || v >= 1 {
				continue
			}
			a := float32(1)
			if m := r.state.clip.mask; m != nil {
				a = float32(m[rowBase+x]) / 255
				if a == 0 {
					continue
				}
			}
			sr, sg, sb, sa := sampleBilinear(src, sbounds, u*float32(sw), v*float32(sh))
			r.blendPremul(x, y, sr, sg, sb, sa*a)
		}
	}
}

// sampleBilinear samples src at (u,v), where u and v are continuous
// coordinates with 0 at the left/top edge of the image. The returned color is
// premultiplied so transparent texels do not bleed dark fringes.
func sampleBilinear(src image.Image, b image.Rectangle, u, v float32) (r, g, bl, a float32) {
	fx := u - 0.5
	fy := v - 0.5
	x0 := int(math.Floor(float64(fx)))
	y0 := int(math.Floor(float64(fy)))
	tx := fx - float32(x0)
	ty := fy - float32(y0)

	x1 := clampIndex(x0+1, b.Dx())
	y1 := clampIndex(y0+1, b.Dy())
	x0 = clampIndex(x0, b.Dx())
	y0 = clampIndex(y0, b.Dy())

	r00, g00, b00, a00 := premulAt(src, b.Min.X+x0, b.Min.Y+y0)
	r10, g10, b10, a10 := premulAt(src, b.Min.X+x1, b.Min.Y+y0)
	r01, g01, b01, a01 := premulAt(src, b.Min.X+x0, b.Min.Y+y1)
	r11, g11, b11, a11 := premulAt(src, b.Min.X+x1, b.Min.Y+y1)

	topR := r00 + (r10-r00)*tx
	topG := g00 + (g10-g00)*tx
	topB := b00 + (b10-b00)*tx
	topA := a00 + (a10-a00)*tx
	botR := r01 + (r11-r01)*tx
	botG := g01 + (g11-g01)*tx
	botB := b01 + (b11-b01)*tx
	botA := a01 + (a11-a01)*tx

	return topR + (botR-topR)*ty,
		topG + (botG-topG)*ty,
		topB + (botB-topB)*ty,
		topA + (botA-topA)*ty
}

func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func premulAt(src image.Image, x, y int) (r, g, b, a float32) {
	c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
	a = float32(c.A) / 255
	r = float32(c.R) / 255 * a
	g = float32(c.G) / 255 * a
	b = float32(c.B) / 255 * a
	return r, g, b, a
}

// blendPremul composites a premultiplied source color with 0..1 alpha.
func (r *Rasterizer) blendPremul(x, y int, sr, sg, sb, sa float32) {
	if sa <= 0 {
		return
	}
	if sa > 1 {
		sa = 1
	}
	inv := 1 - sa
	i := r.img.PixOffset(x+r.ox, y+r.oy)
	p := r.img.Pix[i : i+4]
	p[0] = uint8(clamp01(sr+float32(p[0])/255*inv)*255 + 0.5)
	p[1] = uint8(clamp01(sg+float32(p[1])/255*inv)*255 + 0.5)
	p[2] = uint8(clamp01(sb+float32(p[2])/255*inv)*255 + 0.5)
	p[3] = uint8(clamp01(sa+float32(p[3])/255*inv)*255 + 0.5)
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
