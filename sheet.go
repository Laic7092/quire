package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
	"github.com/laix/quire/internal/reader"
)

// The sheet is the paper the book is printed on. One page at a time on a narrow
// window, two pages side by side — an open book — once there is room, which is
// what Apple Books does on a wide window.
const (
	// turnDuration is how long a page takes to give way: short enough to feel
	// immediate, long enough to read as motion.
	turnDuration = 240 * time.Millisecond
	// turnFadeDuration is how long a fade takes: shorter than a slide, because a
	// dissolve has no motion to read, only two printings changing places.
	turnFadeDuration = 190 * time.Millisecond
	// turnFlickDuration is how long each turn of a held key lasts. A repeat
	// arrives every 30 ms or so, far faster than a reading turn; keeping the
	// short slide means the sheet can finish every one of them, so a held key
	// flicks through the book instead of restarting one slide it never lands.
	turnFlickDuration = 90 * time.Millisecond
	// coverHold is how long an opening book's cover rests before it gives way to
	// the place the book resumes at: the dissolve has to read as the book
	// opening, not as a flicker of its cover.
	coverHold = 420 * time.Millisecond
	// coverFadeDuration is how long that dissolve takes: the fade's pace, since
	// it is the same kind of change, two printings swapping places.
	coverFadeDuration = 260 * time.Millisecond
	// coverPad is the margin the cover keeps inside the page area, as Books
	// shows a cover on the sheet.
	coverPad = 28
)

// coverIntro is the cover giving way to the place a resumed book opens at: hold
// is how long it rests before it starts to go, and fade how long the dissolve
// takes.
type coverIntro struct {
	at   time.Time
	hold time.Duration
	fade time.Duration
}

// turnState is a page turn on its way across the sheet: from and to are the
// starts of the spreads leaving and arriving, dir which way it goes, and dur
// how long this one takes (zero for the reading pace).
type turnState struct {
	from, to int
	dir      int
	at       time.Time
	dur      time.Duration
}

// duration is how long this turn takes: the reading pace unless the key that
// asked for it is being held.
func (t *turnState) duration() time.Duration {
	if t.dur > 0 {
		return t.dur
	}
	return turnDuration
}

// pagesOnSheet is how many pages the sheet shows: two when the reader asked for
// a spread and the page area is wide enough for two readable ones. A scrolling
// book has one column whatever the width.
func (a *app) pagesOnSheet() int {
	if a.layScroll {
		return 1
	}
	if a.opts.Spread && a.areaW > 2*pagePadding && splitSheet(a.areaW-2*pagePadding) {
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

// clampPage keeps a page inside a book of n pages, and a book with no pages at
// its first one: a location the reader jumps to may name any page, and an empty
// book must not leave the folio at page -1.
func clampPage(page, n int) int {
	if n <= 0 {
		return 0
	}
	return min(max(page, 0), n-1)
}

// splitSheet reports whether avail — the width the pages may use, the area less
// its two margins — holds two pages of at least reader.SpreadMinWidth. Both the
// sheet's width and the count of pages on it come from this one answer, so a
// spread never shows two pages narrower than a page may be.
func splitSheet(avail float32) bool { return avail >= 2*reader.SpreadMinWidth }

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
	if !opts.Scroll && opts.Spread && splitSheet(avail) {
		pages = 2
	}
	box := min(avail, pageMaxWidth*float32(pages))
	return box / float32(pages), h
}

// pagesFrom returns the page numbers drawn from start of l: one, or the pair of
// a spread when the layout has a second page to show. l is the layout being
// drawn — the resize preview while the whole book catches up, else the published
// one — and start is a page of it (see sheetPlan).
func (a *app) pagesFrom(l *render.Layout, start int) []int {
	if l == nil {
		return nil
	}
	pages := []int{start}
	if a.pagesOnSheet() == 2 && start+1 < l.PageCount() {
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

// beginCoverIntro starts the cover dissolving into the place a resumed book
// opens at. The book is laid out by now, so its cover is drawn from this frame
// on: it holds for coverHold, long enough to read as the book opening rather
// than a flicker, and then fades over coverFadeDuration. A reader who asked for
// no motion opens straight at the place.
func (a *app) beginCoverIntro() {
	if !a.showCover || a.coverIntro != nil {
		return
	}
	if a.instant {
		a.dismissCover()
		return
	}
	a.coverIntro = &coverIntro{at: a.clock(), hold: coverHold, fade: coverFadeDuration}
}

// coverAlpha is how strongly the cover shows now, and false once it has given
// way. It is 1 for a cover that is not dissolving, so a cover the reader has not
// moved past keeps showing.
func (a *app) coverAlpha() (float32, bool) {
	c := a.coverIntro
	if c == nil {
		return 1, false
	}
	elapsed := a.clock().Sub(c.at)
	if elapsed < c.hold {
		return 1, true
	}
	p := float32(elapsed-c.hold) / float32(c.fade)
	if p >= 1 {
		return 0, false
	}
	return 1 - easeInOut(p), true
}

// dismissCover takes the cover away now, with the dissolve it may be in, as the
// reader turning, scrolling or jumping into the book does.
func (a *app) dismissCover() {
	a.showCover = false
	a.coverIntro = nil
}

// drawCover paints the cover over the sheet: alone while the book opens, and
// dissolving into the place the book resumes at. It reports whether it drew the
// frame, in which case the caller can draw no page.
func (a *app) drawCover(p *ui.Painter, r ui.Rect) bool {
	if a.paint == nil {
		return false
	}
	alpha, moving := a.coverAlpha()
	if a.coverIntro != nil && !moving {
		// The dissolve has landed: the cover is gone for good, and the frame
		// asked for here draws the reader's page, its folio and its menu.
		a.dismissCover()
		a.services.Invalidate()
		return false
	}
	if !moving {
		// The cover stands alone until the reader moves on.
		return a.paint.DrawCover(p, a.cover, r, coverPad)
	}
	// The cover rests, then gives way: the frames have to keep coming.
	p.AnimationFrame()
	if alpha >= 1 || a.layout == nil {
		return a.paint.DrawCover(p, a.cover, r, coverPad)
	}
	// The page the book opens at is drawn under the cover, and the cover fades
	// over it: the dissolve reads as the book opening where it was left.
	a.drawPaper(p, r)
	if a.scrolling() {
		a.drawScroll(p, r)
	} else {
		a.drawPages(p, r, a.page, 0)
		a.drawSpine(p, r)
	}
	a.paint.SetFade(alpha)
	defer a.paint.SetFade(1)
	return a.paint.DrawCover(p, a.cover, r, coverPad)
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
	p := float32(a.clock().Sub(t.at)) / float32(t.duration())
	if p >= 1 {
		return 1, false
	}
	return p, true
}

// easeInOut is the curve a fade follows: slow at both ends, quickest in the
// middle, so the change is spread over the whole turn instead of being spent on
// its first frames.
func easeInOut(p float32) float32 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	return p * p * (3 - 2*p)
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
//
// A sheet that moves — a glide of the column, a page turn — is drawn by frames
// that build nothing: the turn's place is a function of the clock already, and
// the column's is stepped here, on the frame's own clock, when the build pass of
// this frame did not do it. Those frames ask for the next one themselves
// (Painter.AnimationFrame, which paints the elements of this one again), and the
// frame that lands asks for a build, whose pass writes the place down and draws
// the folio over it. So the view is built once for a whole gesture instead of
// once for every frame of it.
// fitGlyphBudget sizes the glyph atlas for what this window reads in: its type,
// its ratio and the room a screen of its text takes. A window that never moved
// nor changed size does the arithmetic once.
func (a *app) fitGlyphBudget(p *ui.Painter, r ui.Rect) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	key := [5]float32{a.opts.FontSize, a.opts.LineHeight, r.W, r.H, p.Scale()}
	if key == a.atlasKey {
		return
	}
	a.atlasKey = key
	reader.SetGlyphAtlasBudget(reader.GlyphBudgetFor(key[0], key[1], key[2], key[3], key[4]))
}

func (a *app) drawSheet(p *ui.Painter, r ui.Rect) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	// Whether this frame built the view: the build pass stepped the column and
	// named the frame for the log, and a frame that builds nothing does both
	// here. It is read and cleared once, so a second paint of the same frame
	// neither steps twice nor counts twice.
	built := a.frameBuilt
	a.frameBuilt = false
	// The glyph atlas holds a screen's worth of the window in front, so the
	// screen does not scroll past its own characters and raster them again.
	a.fitGlyphBudget(p, r)
	if a.hud.on {
		// The monitor measures the frame around this callback: what the reader
		// painted and whether the frame built the view at all (see perfhud.go).
		hud := &a.hud
		hud.tiles = 0
		start := time.Now()
		defer func() { hud.frameDone(built, time.Since(start), hud.tiles) }()
	}
	if perf != nil {
		// The frame is measured around this callback, and the log learns how the
		// frame is drawn once: on a GPU or on MyGo's own rasterizer.
		perf.firstPaint(p.Scale(), p.Accelerated())
		if !built {
			// A frame that only paints: the log begins it here, with nothing built,
			// so a glide's frames read as the paint they are.
			perf.frameBegin()
			perf.frameScene(a.frameScene())
		}
		perf.paintBegin()
		defer func() { perf.paintEnd(a.paint.GlyphStats()) }()
	}
	// The glyphs the painter rasterized while drawing are uploaded after the
	// pages are drawn, so this frame shows them. It runs before paintEnd, which
	// is why the frame's upload count is the one this draw callback caused.
	defer a.paint.FlushGlyphs()
	if a.showCover && a.paint != nil {
		if a.drawCover(p, r) {
			return
		}
		// There is no cover to draw. Before the book is laid out the bitmap is
		// still on its way and the frame shows the reading state, so the cover
		// stays armed; once the dissolve is armed the image would have been
		// decoded, so the book itself is the front.
		if a.coverIntro != nil {
			a.dismissCover()
			a.services.Invalidate()
		}
	}
	if a.layout == nil || a.paint == nil {
		return
	}
	if a.scrolling() {
		if !built {
			// The column moves on this frame's clock, as it does in the build pass
			// of a frame that builds; both share the glide's state, so a gesture
			// that mixes the two does not change its motion. What the frame leaves
			// to the build is the publishing of the place (stepColumn).
			//
			// A frame the engine paints again for its own reasons — the atlas
			// making room — steps again, and stepColumn's own account of the time
			// since the last step is what keeps that from moving the column twice.
			a.stepColumn()
		}
		if a.gliding() {
			// A frame of a scroll asks for the display's next refresh: what it
			// draws is a few tiles and no glyph work, so it fits a refresh with
			// the room a display gives one, and asking for fewer would show the
			// motion at that rate instead of the display's.
			//
			// Nothing is built while the column moves: the folio keeps the place
			// the gesture started from until it lands, which is the frame below.
			// A build is the one frame of a gesture that no longer fits — a
			// screenful of elements laid out again — and one every few frames is
			// a stutter every few frames.
			p.AnimationFrame()
		} else if !built {
			// The glide landed in a frame that built nothing: ask for the one that
			// writes the place down, tells the panels where the reader is and draws
			// the folio over the new place. Without it the folio would keep the
			// percentage the gesture started from until something else happened.
			a.services.Invalidate()
		}
		a.drawScroll(p, r)
		return
	}
	progress, moving := a.turnProgress()
	if !moving {
		a.drawPages(p, r, a.page, 0)
		a.drawSpine(p, r)
		if a.turning != nil && !built {
			// The slide landed between two builds: a build settles it, and starts
			// the turn a held key asked for while it was on its way.
			a.services.Invalidate()
		}
		return
	}
	// The turn is on its way: keep the frames coming until it lands, and count
	// them, since most of them build nothing (see perf.turnFrame).
	perf.turnFrame()
	p.AnimationFrame()
	from, to := a.turning.from, a.page
	e := easeOut(progress)
	p.Clip(r, 0, func() {
		if a.opts.Turn == reader.TurnFade {
			// The fade has its own curve: easing it again with easeOut would
			// spend the turn's whole change on its first frames.
			a.drawFade(p, r, from, to, progress)
			return
		}
		a.drawSlide(p, r, from, to, e)
	})
}

// drawSlide moves the pages as one strip: the spread leaving goes all the way
// off and the one arriving follows it, so the two never overlap and the sheet
// reads as a page sliding under the window, as Books pages on a touch screen.
//
// The strip travels in whole device pixels, as the scrolling column does and for
// the same reason: a glyph is rasterized once per place its origin falls in a
// pixel, so a strip at a fraction of one swaps every glyph of both spreads for a
// near copy of itself on every frame of the turn — the type shimmers, and the
// atlas grows by several hundred bitmaps a turn to do it. Whole pixels keep one
// phase for the whole slide, which is the phase the spread has at rest: the turn
// costs no rasterizing at all.
func (a *app) drawSlide(p *ui.Painter, r ui.Rect, from, to int, e float32) {
	scale := float32(0)
	if p != nil {
		scale = p.Scale()
	}
	if a.turning.dir >= 0 {
		a.drawPages(p, r, from, snapDevices(-e*r.W, scale))
		a.drawPages(p, r, to, snapDevices((1-e)*r.W, scale))
		return
	}
	a.drawPages(p, r, to, snapDevices(-(1-e)*r.W, scale))
	a.drawPages(p, r, from, snapDevices(e*r.W, scale))
}

// drawFade dissolves the spread leaving into the one arriving in place: the
// pages do not move, one printing gives way to the next as if the same sheet
// were printed again, and only the text changes.
//
// The sheet is printed once, opaque, and the two spreads are painted over it:
// the one leaving fading out, the one arriving fading in, on an S curve. The two
// ramps overlap by half the turn, so the sheet is never blank — a fade that
// passes through the paper reads as the screen flashing, not as a page — and the
// ink never sits at half strength on both printings for long.
func (a *app) drawFade(p *ui.Painter, r ui.Rect, from, to int, progress float32) {
	leaving, arriving := fadeAlpha(progress)
	a.drawPaper(p, r)
	a.paint.SetFade(leaving)
	a.drawPages(p, r, from, 0)
	a.paint.SetFade(arriving)
	defer a.paint.SetFade(1)
	a.drawPages(p, r, to, 0)
}

// fadeAlpha is how strongly each printing shows at progress: the one leaving is
// gone by four fifths of the turn, the one arriving starts at a fifth, and the
// two are on the sheet together only in between.
func fadeAlpha(progress float32) (leaving, arriving float32) {
	progress = clampf(progress, 0, 1)
	const out, in = 0.8, 0.2
	return easeInOut(1 - min(progress/out, 1)), easeInOut(max((progress-in)/(1-in), 0))
}

// drawPaper prints the sheet itself, opaque, in the paper's colour.
func (a *app) drawPaper(p *ui.Painter, r ui.Rect) {
	a.paint.FillRect(paint.Rect{X: r.X, Y: r.Y, Width: r.W, Height: r.H}, a.palette().Paper)
}

// drawPages paints the pages that start at start, shifted by dx.
func (a *app) drawPages(p *ui.Painter, r ui.Rect, start int, dx float32) {
	l, from := a.sheetPlan(start)
	pages := a.pagesFrom(l, from)
	if len(pages) == 0 {
		return
	}
	w := r.W / float32(a.pagesOnSheet())
	for i, page := range pages {
		// The page is drawn at the size it was laid out at, centred in its half
		// of the sheet: a layout built for another sheet — the published one
		// while the whole book catches up, or the preview of the size being
		// dragged to — sits at its own measure instead of spilling over the
		// next page.
		dw := w
		pg := l.Page(page)
		if pg != nil {
			if size := pg.Size(); size.Width > 0 && size.Width < w {
				dw = size.Width
			}
		}
		box := ui.Rect{X: r.X + float32(i)*w + (w-dw)/2 + dx, Y: r.Y, W: dw, H: r.H}
		if pg != nil {
			a.paint.DrawPage(p, pg, box)
		}
		a.drawMarks(p, box, l, page)
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
