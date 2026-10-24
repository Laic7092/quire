package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render/paint"
)

// The sheet is the paper the book is printed on. One page at a time on a narrow
// window, two pages side by side — an open book — once there is room, which is
// what Apple Books does on a wide window.
const (
	// turnDuration is how long a page takes to slide away: short enough to feel
	// immediate, long enough to read as motion.
	turnDuration = 240 * time.Millisecond
	// edgeShadow is the band of shade the arriving page casts on the one it
	// covers, in DIPs, and pageShadow how far the page's own shadow reaches.
	edgeShadow = 16
	// spine is the width of the line where the two pages of a spread meet.
	spine = 1.5
)

// turnState is a page turn on its way across the sheet: from and to are the
// starts of the spreads leaving and arriving, dir which way it goes.
type turnState struct {
	from, to int
	dir      int
	at       time.Time
}

// pagesOnSheet is how many pages the sheet shows: two when the reader asked for
// a spread and the page area is wide enough for two readable ones.
func (a *app) pagesOnSheet() int {
	if a.opts.Spread && a.areaW >= 2*reader.SpreadMinWidth {
		return 2
	}
	return 1
}

// pageStep is how many pages one turn moves.
func (a *app) pageStep() int { return a.pagesOnSheet() }

// snapPage keeps a page at the start of its spread, so an open book always shows
// the same pair and a turn moves by the pair, not by one page.
func (a *app) snapPage(page int) int {
	if a.pageStep() == 2 {
		return page &^ 1
	}
	return page
}

// sheetSize is the size one page is laid out at inside a box of w×h: half the
// width when two share it.
func (a *app) sheetSize(w, h float32) (float32, float32) {
	if a.pagesOnSheet() == 2 {
		return w / 2, h
	}
	return w, h
}

// pageAtX maps a point across the box to the page shown under it: the left half
// of a spread is the first page, the right half the second.
func (a *app) pageAtX(x, w float32) int {
	page := a.page
	if a.pagesOnSheet() == 2 && x >= w/2 && a.layout != nil && page+1 < a.layout.PageCount() {
		return page + 1
	}
	return page
}

// pagesFrom returns the page numbers drawn from start: one, or the pair of a
// spread when the book has a second page to show.
func (a *app) pagesFrom(start int) []int {
	if a.layout == nil {
		return nil
	}
	pages := []int{start}
	if a.pagesOnSheet() == 2 && start+1 < a.layout.PageCount() {
		pages = append(pages, start+1)
	}
	return pages
}

// clock is the time the turn is measured against: the real one, unless a test
// pins it.
func (a *app) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// turnProgress is how far the turn in flight has come, from 0 to 1, and false
// when the sheet is still.
func (a *app) turnProgress() (float32, bool) {
	t := a.turning
	if t == nil {
		return 0, false
	}
	if a.instant {
		return 1, false
	}
	p := float32(a.clock().Sub(t.at)) / float32(turnDuration)
	if p >= 1 {
		return 1, false
	}
	return p, true
}

// easeOut is the curve a turn follows: quick off the mark, settling at the end,
// as a page slides under a finger.
func easeOut(p float32) float32 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	q := 1 - p
	return 1 - q*q*q
}

// drawSheet paints into r: the cover, or the page or pages of the book, with the
// turn in flight sliding one spread out and the next in.
//
// It runs inside the box's Draw callback, so it only replays display lists it
// already has and never lays anything out.
func (a *app) drawSheet(p *ui.Painter, r ui.Rect) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	if a.showCover && a.paint != nil && a.paint.DrawCover(p, a.cover, r, 28) {
		return
	}
	if a.layout == nil || a.paint == nil {
		return
	}
	progress, moving := a.turnProgress()
	if !moving {
		a.drawPages(p, r, a.page, 0)
		a.drawSpine(p, r)
		return
	}
	e := easeOut(progress)
	from, to := a.turning.from, a.page
	// The pages move as one strip: the spread leaving goes all the way off and
	// the one arriving follows it, so the two never overlap and the sheet reads
	// as a page sliding under the window, as Books pages on a touch screen.
	p.Clip(r, 0, func() {
		if a.turning.dir >= 0 {
			a.drawPages(p, r, from, -e*r.W)
			a.drawPages(p, r, to, (1-e)*r.W)
			a.drawEdgeShadow(p, r, (1-e)*r.W, 1, e*r.W)
			return
		}
		a.drawPages(p, r, to, -(1-e)*r.W)
		a.drawPages(p, r, from, e*r.W)
		a.drawEdgeShadow(p, r, e*r.W, -1, r.W-e*r.W)
	})
}

// drawPages paints the pages that start at start, shifted by dx.
func (a *app) drawPages(p *ui.Painter, r ui.Rect, start int, dx float32) {
	pages := a.pagesFrom(start)
	if len(pages) == 0 {
		return
	}
	w := r.W / float32(a.pagesOnSheet())
	for i, page := range pages {
		// The page is drawn at the size it was laid out at, centred in its
		// half of the sheet: while a resize is still being laid out the text
		// sits where it was instead of spilling over the next page.
		dw := w
		if a.layW > 0 && a.layW < w {
			dw = a.layW
		}
		box := ui.Rect{X: r.X + float32(i)*w + (w-dw)/2 + dx, Y: r.Y, W: dw, H: r.H}
		if pg := a.layout.Page(page); pg != nil {
			a.paint.DrawPage(p, pg, box)
		}
		a.drawMarks(p, box, page)
	}
	// The spine only belongs to a spread at rest: while it slides the two pages
	// are not a book.
	if dx == 0 {
		a.drawSpine(p, r)
	}
}

// drawSpine draws the line where the two pages of an open book meet, with the
// shade each catches from the fold.
func (a *app) drawSpine(p *ui.Painter, r ui.Rect) {
	if a.pagesOnSheet() != 2 {
		return
	}
	pal := reader.Current()
	x := r.X + r.W/2
	for i := 0; i < 3; i++ {
		w := float32(2 * (3 - i))
		a := uint8(4 + i*4)
		p.Fill(ui.Rect{X: x - w, Y: r.Y, W: w, H: r.H}, reader.UIColor(paintAlpha(pal.Ink, a)), 0)
		p.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, reader.UIColor(paintAlpha(pal.Ink, a)), 0)
	}
	p.Fill(ui.Rect{X: x - spine/2, Y: r.Y, W: spine, H: r.H},
		reader.UIColor(paintAlpha(pal.Ink, 0x24)), 0)
}

// drawEdgeShadow is the shade the arriving page casts on the one it covers: at
// the edge it leads with, deepest against the paper and gone a few bands out.
// side is +1 when the shade falls to the left of x, -1 when to its right.
func (a *app) drawEdgeShadow(p *ui.Painter, r ui.Rect, x float32, side int, width float32) {
	if width <= 0 || x <= r.X || x >= r.X+r.W {
		return
	}
	pal := reader.Current()
	const bands = 8
	band := width / bands
	for i := 0; i < bands; i++ {
		k := 1 - float32(i)/float32(bands)
		alpha := uint8(float32(0x26) * k * k)
		x0 := x - float32(i+1)*band
		if side < 0 {
			x0 = x + float32(i)*band
		}
		x1 := x0 + band
		x0 = max(x0, r.X)
		x1 = min(x1, r.X+r.W)
		if x1 <= x0 {
			continue
		}
		p.Fill(ui.Rect{X: x0, Y: r.Y, W: x1 - x0, H: r.H},
			reader.UIColor(paintAlpha(pal.Ink, alpha)), 0)
	}
}

// paintAlpha is c with another alpha, for the shades the paper casts.
func paintAlpha(c paint.Color, a uint8) paint.Color {
	c.A = a
	return c
}
