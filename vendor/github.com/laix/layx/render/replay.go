package render

import "github.com/laix/layx/render/paint"

// visibleRect reports the optional visible region of p. A painter that does not
// implement paint.Clipped is never culled, so the zero value of the old
// Painter interface keeps its exact behaviour.
func visibleRect(p paint.Painter) (paint.Rect, bool) {
	c, ok := p.(paint.Clipped)
	if !ok {
		return paint.Rect{}, false
	}
	return c.Visible(), true
}

// replayOp reports whether op can affect pixels inside visible. Ops with no
// footprint (a zero-area fill, or a list built by an older path) are left to
// the painter rather than guessed at.
func replayOp(op *drawOp, visible paint.Rect) bool {
	if op.rect.Empty() {
		return true
	}
	_, hit := intersectRect(op.rect, visible)
	return hit
}

// glyphOpRect is the line box padded by one line height. Glyph ink can leave
// the line box (line-height < 1, hanging punctuation, italic overhang), so the
// culling rectangle is deliberately looser than the box the line occupies.
func glyphOpRect(x, y, w, h float32) paint.Rect {
	pad := h
	if pad < 1 {
		pad = 1
	}
	return paint.Rect{X: x - pad, Y: y - pad, Width: w + 2*pad, Height: h + 2*pad}
}
