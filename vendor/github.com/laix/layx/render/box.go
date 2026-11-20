package render

import (
	"sort"

	"github.com/laix/layx/render/paint"
)

// blockRect is one block's border box in chapter content coordinates. It is
// collected while stacking, when the block's final vertical extent is known.
// Only blocks that actually paint something are recorded, so an unstyled
// document produces an empty list and the default display list is unchanged.
type blockRect struct {
	style         Style
	x, y          float32
	width, height float32
}

// borderWidth normalises one border edge. Negative widths are treated as zero
// so a malformed style cannot expand the content box.
func borderWidth(b Border) float32 {
	if b.Width > 0 {
		return b.Width
	}
	return 0
}

func borderWidths(st Style) (top, right, bottom, left float32) {
	return borderWidth(st.BorderTop), borderWidth(st.BorderRight),
		borderWidth(st.BorderBottom), borderWidth(st.BorderLeft)
}

// boxPaints reports whether a block records a blockRect. A width-only border
// with a transparent color still shrinks layout, but painting it would add an
// empty operation, so it is not recorded.
func boxPaints(st Style) bool {
	if !st.Background.Transparent() {
		return true
	}
	if borderWidth(st.BorderTop) > 0 && !st.BorderTop.Color.Transparent() {
		return true
	}
	if borderWidth(st.BorderRight) > 0 && !st.BorderRight.Color.Transparent() {
		return true
	}
	if borderWidth(st.BorderBottom) > 0 && !st.BorderBottom.Color.Transparent() {
		return true
	}
	if borderWidth(st.BorderLeft) > 0 && !st.BorderLeft.Color.Transparent() {
		return true
	}
	return false
}

func intersectRect(a, b paint.Rect) (paint.Rect, bool) {
	x0 := maxF32(a.X, b.X)
	y0 := maxF32(a.Y, b.Y)
	x1 := minF32(a.X+a.Width, b.X+b.Width)
	y1 := minF32(a.Y+a.Height, b.Y+b.Height)
	if x1 <= x0 || y1 <= y0 {
		return paint.Rect{}, false
	}
	return paint.Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, true
}

func minF32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

const boxEdgeEps = 0.01

// appendBoxOps draws backgrounds and borders for the boxes visible in clip.
//
// The box rectangle is intersected with the current page or tile content
// rectangle. An edge whose outer line lies outside that intersection was cut
// by the page/tile boundary and is skipped: left and right borders therefore
// repeat on every fragment, while top and bottom borders appear only on the
// first and last fragment. Boxes are sorted by top edge (and by descending
// height for equal tops), so parents are painted before their children.
//
// offX/offY translate chapter content coordinates into page/tile coordinates.
func appendBoxOps(ops []drawOp, boxes []blockRect, clip paint.Rect, offX, offY float32) []drawOp {
	if len(boxes) == 0 || clip.Empty() {
		return ops
	}
	// Boxes nest, so their bottom edges are not monotonic even though their top
	// edges are: a tall parent is followed by short children that end above the
	// clip. Searching for the first box whose bottom edge reaches the clip
	// would therefore skip the parent on any page where a child ends above it,
	// losing the parent's background and side borders. Scan from the top and
	// stop below the clip instead.
	for i := 0; i < len(boxes); i++ {
		b := &boxes[i]
		if offY+b.y >= clip.Y+clip.Height {
			break
		}
		r := paint.Rect{X: offX + b.x, Y: offY + b.y, Width: b.width, Height: b.height}
		inter, ok := intersectRect(r, clip)
		if !ok {
			continue
		}
		if bg := b.style.Background; !bg.Transparent() {
			if radius := b.style.CornerRadius; radius > 0 && sameRect(r, inter) {
				ops = append(ops, drawOp{
					kind:   opRoundFill,
					rect:   r,
					radius: minF32(radius, minF32(r.Width, r.Height)/2),
					color:  bg,
				})
			} else {
				ops = append(ops, drawOp{kind: opFill, rect: inter, color: bg})
			}
		}
		top, right, bottom, left := borderWidths(b.style)
		if top > 0 && !b.style.BorderTop.Color.Transparent() && r.Y >= clip.Y-boxEdgeEps {
			ops = append(ops, drawOp{kind: opFill, color: b.style.BorderTop.Color, rect: paint.Rect{
				X: inter.X, Y: inter.Y, Width: inter.Width, Height: top,
			}})
		}
		if bottom > 0 && !b.style.BorderBottom.Color.Transparent() &&
			r.Y+r.Height <= clip.Y+clip.Height+boxEdgeEps {
			ops = append(ops, drawOp{kind: opFill, color: b.style.BorderBottom.Color, rect: paint.Rect{
				X: inter.X, Y: inter.Y + inter.Height - bottom, Width: inter.Width, Height: bottom,
			}})
		}
		if left > 0 && !b.style.BorderLeft.Color.Transparent() && r.X >= clip.X-boxEdgeEps {
			ops = append(ops, drawOp{kind: opFill, color: b.style.BorderLeft.Color, rect: paint.Rect{
				X: inter.X, Y: inter.Y, Width: left, Height: inter.Height,
			}})
		}
		if right > 0 && !b.style.BorderRight.Color.Transparent() &&
			r.X+r.Width <= clip.X+clip.Width+boxEdgeEps {
			ops = append(ops, drawOp{kind: opFill, color: b.style.BorderRight.Color, rect: paint.Rect{
				X: inter.X + inter.Width - right, Y: inter.Y, Width: right, Height: inter.Height,
			}})
		}
	}
	return ops
}

func sameRect(a, b paint.Rect) bool {
	return a == b
}

// sortBlockRects orders boxes for painting: top edge first, and for equal top
// edges the taller (parent) box before the shorter (child) one.
func sortBlockRects(boxes []blockRect) {
	if len(boxes) < 2 {
		return
	}
	sort.SliceStable(boxes, func(i, j int) bool {
		if boxes[i].y != boxes[j].y {
			return boxes[i].y < boxes[j].y
		}
		return boxes[i].height > boxes[j].height
	})
}
