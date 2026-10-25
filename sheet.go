package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
)

// The sheet is the paper the book is printed on. One page at a time on a narrow
// window, two pages side by side — an open book — once there is room, which is
// what Apple Books does on a wide window.
const (
	// turnDuration is how long a page takes to give way: short enough to feel
	// immediate, long enough to read as motion.
	turnDuration = 240 * time.Millisecond
)

// turnState is a page turn on its way across the sheet: from and to are the
// starts of the spreads leaving and arriving, dir which way it goes.
type turnState struct {
	from, to int
	dir      int
	at       time.Time
}

// pagesOnSheet is how many pages the sheet shows: two when the reader asked for
// a spread and the page area is wide enough for two readable ones. A scrolling
// book has one column whatever the width.
func (a *app) pagesOnSheet() int {
	if a.layScroll {
		return 1
	}
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

// sheetSize is the size one page is laid out at for the layout opts ask for: a
// column, half a spread, or a whole single page, inside the page area the last
// frame measured. w is the box the sheet fills today, which is the box the
// settings are about to change: the width is recomputed from the area whenever
// the mode switch resizes it, so a scrolling column is never laid out at a
// spread's width, nor the other way round.
func (a *app) sheetSize(w, h float32, opts reader.Options) (float32, float32) {
	avail := w
	if a.areaW > 2*pagePadding {
		avail = a.areaW - 2*pagePadding
	}
	pages := 1
	if !opts.Scroll && opts.Spread && a.areaW >= 2*reader.SpreadMinWidth {
		pages = 2
	}
	box := min(avail, pageMaxWidth*float32(pages))
	return box / float32(pages), h
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
// turn in flight moving one spread out and the next in.
//
// It runs inside the box's Draw callback, so it only replays display lists it
// already has and never lays anything out.
func (a *app) drawSheet(p *ui.Painter, r ui.Rect) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	// The glyphs the painter rasterized while drawing are uploaded after the
	// pages are drawn, so this frame shows them.
	defer a.paint.FlushGlyphs()
	if a.showCover && a.paint != nil && a.paint.DrawCover(p, a.cover, r, 28) {
		return
	}
	if a.layout == nil || a.paint == nil {
		return
	}
	if a.scrolling() {
		a.drawScroll(p, r)
		return
	}
	progress, moving := a.turnProgress()
	if !moving {
		a.drawPages(p, r, a.page, 0)
		a.drawSpine(p, r)
		return
	}
	from, to := a.turning.from, a.page
	e := easeOut(progress)
	p.Clip(r, 0, func() {
		if a.opts.Turn == reader.TurnFade {
			a.drawFade(p, r, from, to, e)
			return
		}
		a.drawSlide(p, r, from, to, e)
	})
}

// drawSlide moves the pages as one strip: the spread leaving goes all the way
// off and the one arriving follows it, so the two never overlap and the sheet
// reads as a page sliding under the window, as Books pages on a touch screen.
//
// The sheet is the paper, and both spreads are printed on the same one, so the
// turn paints no edge and no shade: the text alone travels, off one side and on
// the other, across one continuous page.
func (a *app) drawSlide(p *ui.Painter, r ui.Rect, from, to int, e float32) {
	if a.turning.dir >= 0 {
		a.drawPages(p, r, from, -e*r.W)
		a.drawPages(p, r, to, (1-e)*r.W)
		return
	}
	a.drawPages(p, r, to, -(1-e)*r.W)
	a.drawPages(p, r, from, e*r.W)
}

// drawFade dissolves the spread leaving into the one arriving in place: the
// pages do not move, one printing gives way to the next as if the same sheet
// were printed again, and only the text changes. The leaving spread is painted
// whole; the arriving one is painted over it at the turn's progress, so half way
// through each shows the other through it.
func (a *app) drawFade(p *ui.Painter, r ui.Rect, from, to int, e float32) {
	a.drawPages(p, r, from, 0)
	a.paint.SetFade(e)
	defer a.paint.SetFade(1)
	a.drawPages(p, r, to, 0)
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
	// Books' open book has no printed fold: the two pages are one sheet, and the
	// gutter carries no shade of its own.
	return
}
