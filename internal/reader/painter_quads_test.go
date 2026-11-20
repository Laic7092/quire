package reader

import (
	"context"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// selectionQuads is a drag across the top of page 0 of the two-chapter book.
func selectionQuads(t *testing.T) (*render.Layout, *render.Page, []render.SelectionQuad) {
	t.Helper()
	doc, fonts := selectionBook(t)
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}
	from, ok := page.HitTest(paint.Point{X: Margin + 2, Y: Margin + 6})
	if !ok {
		t.Fatal("nothing to select at the top of page 0")
	}
	to, ok := page.HitTest(paint.Point{X: 300, Y: Margin + 120})
	if !ok {
		t.Fatal("nothing to select lower on page 0")
	}
	var sel Selection
	sel.Start(from)
	sel.Extend(to)
	quads := sel.Quads(layout)
	if len(quads) == 0 {
		t.Fatal("the drag selected no rectangles")
	}
	return layout, page, quads
}

// paint is a page or a column of marks over a white sheet: what the reader draws
// once the text is down.
func paintMarks(t *testing.T, w, h int, fn func(p *ui.Painter)) *image.RGBA {
	t.Helper()
	return ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(0xff, 0xff, 0xff)).Draw(func(p *ui.Painter, r ui.Rect) {
			fn(p)
		})
	}, w, h, 1)
}

func isMark(img *image.RGBA, x, y int) bool {
	if !(image.Point{X: x, Y: y}.In(img.Bounds())) {
		return false
	}
	c := img.RGBAAt(x, y)
	return c.R > 0x80 && c.G < 0x80 && c.B < 0x80
}

// A page's selection quads land on the lines they cover: the middle of every
// rectangle is painted, and the page below them is not. They also are cut to the
// box the page was drawn in, so a page drawn smaller than its layout never paints
// on the desk around it.
func TestFillQuadsLandsOnThePage(t *testing.T) {
	_, page, quads := selectionQuads(t)
	size := page.Size()
	w, h := int(size.Width), int(size.Height)
	up := NewUIPainter(nil, nil)
	mark := paint.Color{R: 0xff, A: 0xff}
	img := paintMarks(t, w, h, func(p *ui.Painter) {
		up.FillQuads(p, ui.Rect{W: size.Width, H: size.Height}, 0, quads, mark)
	})
	for _, q := range quads {
		if q.Page != 0 {
			continue
		}
		x, y := int(q.Rect.X+q.Rect.Width/2), int(q.Rect.Y+q.Rect.Height/2)
		if !isMark(img, x, y) {
			t.Errorf("the middle (%d,%d) of rect %+v is not painted", x, y, q.Rect)
		}
	}
	// The foot of the page is below the drag and stays paper.
	if isMark(img, w/2, h-4) {
		t.Error("the foot of the page is painted although nothing is selected there")
	}
	// A quad belongs to its page: the same rectangles drawn for another page
	// paint nothing.
	if other := paintMarks(t, w, h, func(p *ui.Painter) {
		up.FillQuads(p, ui.Rect{W: size.Width, H: size.Height}, 1, quads, mark)
	}); isMark(other, w/2, int(quads[0].Rect.Y)) {
		t.Error("a page's quads painted on another page")
	}

	// Drawn in a narrower box, the marks stop at the box.
	const boxW = 200
	cut := paintMarks(t, w, h, func(p *ui.Painter) {
		up.FillQuads(p, ui.Rect{W: boxW, H: size.Height}, 0, quads, mark)
	})
	for _, q := range quads {
		if q.Page != 0 || q.Rect.X < boxW {
			continue
		}
		x, y := min(int(q.Rect.X+2), w-1), int(q.Rect.Y+q.Rect.Height/2)
		if isMark(cut, x, y) {
			t.Errorf("rect %+v painted at x=%d outside the %d DIP box", q.Rect, x, boxW)
		}
	}
	if q := quads[0]; q.Rect.X+2 < boxW {
		if !isMark(cut, int(q.Rect.X+2), int(q.Rect.Y+q.Rect.Height/2)) {
			t.Errorf("the part of rect %+v inside the box is not painted", q.Rect)
		}
	}
}

// The selection rectangles cover the type they name. The sheet paints the page
// first and the marks over it, so a rectangle derived from the layout's line
// boxes has to hold the rows of ink the page put there: a rectangle that is
// off by the ascent leaves the tops of the glyphs bare and paints the leading
// under them instead.
func TestQuadsCoverTheType(t *testing.T) {
	_, page, quads := selectionQuads(t)
	size := page.Size()
	w, h := int(size.Width), int(size.Height)
	paper := Current().Paper
	painter := NewUIPainter(nil, nil)
	img := paintMarks(t, w, h, func(p *ui.Painter) {
		painter.DrawPage(p, page, ui.Rect{W: size.Width, H: size.Height})
	})
	// The page's rows of ink, one span per line of type: the leading between
	// two lines is paper, so a span is one line's glyphs.
	type span struct{ y0, y1 int }
	var spans []span
	cur := span{y0: -1}
	for y := 0; y < h; y++ {
		dirty := false
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			if c.R != paper.R || c.G != paper.G || c.B != paper.B {
				dirty = true
				break
			}
		}
		if dirty {
			if cur.y0 < 0 {
				cur = span{y0: y}
			}
			cur.y1 = y
			continue
		}
		if cur.y0 >= 0 {
			spans = append(spans, cur)
			cur = span{y0: -1}
		}
	}
	if cur.y0 >= 0 {
		spans = append(spans, cur)
	}
	if len(spans) == 0 {
		t.Fatal("the page painted no type")
	}

	// A span that overlaps a rectangle is one of the lines that rectangle
	// names: all of it has to be inside, and every rectangle has to name one.
	const slop = 2
	covered := make([]bool, len(quads))
	for _, s := range spans {
		for i, q := range quads {
			if q.Page != 0 || q.Rect.Empty() {
				continue
			}
			top, bottom := int(q.Rect.Y), int(q.Rect.Y+q.Rect.Height)
			if s.y1 < top || s.y0 > bottom {
				continue
			}
			if s.y0 < top-slop || s.y1 > bottom+slop {
				t.Errorf("type rows %d..%d are not inside rect y %d..%d", s.y0, s.y1, top, bottom)
				continue
			}
			covered[i] = true
		}
	}
	for i, q := range quads {
		if q.Page == 0 && !q.Rect.Empty() && !covered[i] {
			t.Errorf("rect %+v covers no type", q.Rect)
		}
	}
}

// The scroll path takes content coordinates and the column's own scroll: a
// rectangle lands on its line of the column as it is shown, and nothing of it
// paints outside the viewport.
func TestFillScrollQuadsFollowsTheColumn(t *testing.T) {
	layout, _, _ := benchScrollColumn(t)
	const scrollY float32 = 4000
	view := ui.Rect{W: 620, H: 700}
	from, ok := layout.LocationAt(scrollY + 30)
	if !ok {
		t.Fatal("no location at the top of the viewport")
	}
	to, ok := layout.LocationAt(scrollY + 300)
	if !ok {
		t.Fatal("no location lower in the viewport")
	}
	quads := layout.Selection(from, to)
	if len(quads) == 0 {
		t.Fatal("the selection has no rectangles")
	}
	up := NewUIPainter(nil, nil)
	mark := paint.Color{R: 0xff, A: 0xff}
	// The same quads twice: once as the column stands, and once scrolled a
	// viewport further, which puts them above it.
	drawn := paintMarks(t, 620, 700, func(p *ui.Painter) {
		p.Clip(view, 0, func() {
			up.FillScrollQuads(p, view, quads, mark, scrollY)
		})
	})
	painted := 0
	for _, q := range quads {
		x := int(view.X + q.Content.X + 4)
		y := int(view.Y + q.Content.Y - scrollY + q.Content.Height/2)
		if y < 0 || y >= 700 || x < 0 || x >= 620 {
			continue
		}
		painted++
		if !isMark(drawn, x, y) {
			t.Errorf("rect %+v is not painted at (%d,%d) for a column at %g", q.Content, x, y, scrollY)
		}
	}
	if painted == 0 {
		t.Fatal("no rectangle of the selection is inside the viewport")
	}
	scrolled := paintMarks(t, 620, 700, func(p *ui.Painter) {
		p.Clip(view, 0, func() {
			up.FillScrollQuads(p, view, quads, mark, scrollY+700)
		})
	})
	for i := 0; i < 700; i += 7 {
		if isMark(scrolled, 100, i) {
			t.Fatalf("a rectangle painted at y=%d although the column moved it away", i)
		}
	}
}
