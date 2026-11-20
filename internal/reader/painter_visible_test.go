package reader

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render/paint"
)

// The painter implements render's optional culling: the replay skips an op whose
// rectangle misses what can show, before the painter is handed it.
var _ paint.Clipped = (*UIPainter)(nil)

// Visible reports the clip in the display list's own coordinates: a page or a
// tile is drawn offset by where its top left lands, and the ops it replays are in
// its own box, so the rectangle shifted back is the part of the list that can
// reach the screen.
func TestVisibleIsTheClipInTheListsOwnCoordinates(t *testing.T) {
	up := &UIPainter{}
	var page, tile, whole paint.Rect
	ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			up.P = p
			// A page drawn at the box's own corner: the whole page shows.
			up.dx, up.dy = 40, 25
			page = up.Visible()
			// A tile whose top edge lands above the viewport, cut to what is
			// left of the viewport's rectangle.
			p.Clip(ui.Rect{X: 10, Y: 60, W: 400, H: 150}, 0, func() {
				up.dx, up.dy = 10, 200
				tile = up.Visible()
			})
			// A painter asked not to cull — a test painting a column whole —
			// reports everything visible, so nothing is skipped.
			up.noCull = true
			up.dx, up.dy = 40, 25
			whole = up.Visible()
			up.noCull = false
		})
	}, 400, 300, 1)

	if want := (paint.Rect{X: -40, Y: -25, Width: 400, Height: 300}); page != want {
		t.Errorf("a page at (40, 25) reports %v, want %v", page, want)
	}
	if want := (paint.Rect{X: 0, Y: -140, Width: 390, Height: 150}); tile != want {
		t.Errorf("a tile at (10, 200) cut to the viewport reports %v, want %v", tile, want)
	}
	if !(whole.X <= -1e5 && whole.Y <= -1e5 && whole.X+whole.Width >= 1e5 && whole.Y+whole.Height >= 1e5) {
		t.Errorf("a painter asked not to cull reports %v, which does not hold the list", whole)
	}
}
