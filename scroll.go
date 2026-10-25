package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// The reading page can be turned or scrolled. Scrolling builds the layout with
// render.ModeScroll: the chapters stack into one flow with no page slots, and
// the engine slices that flow into tiles one viewport tall. The reader keeps a
// content offset — scrollY is the flow's y at the top of the column — and draws
// the tiles the viewport covers. Positions, selection rectangles and search
// hits then live in the same content coordinates in both modes, which is why
// the panels, the store and the search index need no second code path.

// scrollInset is the room the column keeps between its text and the bars above
// and below it, in DIPs: the same head the paged sheet gives its first line.
const scrollInset = reader.PageTop

// scrollGlideDuration is how long the column takes to reach the place a wheel
// notch asked for, so a mouse scrolls smoothly the way a touchpad does.
const scrollGlideDuration = 140 * time.Millisecond

// scrolling reports whether the layout on screen is a scrolling column. It
// follows the published layout, not the setting, so the frame between changing
// the setting and the new layout keeps drawing what it has.
func (a *app) scrolling() bool { return a.layScroll }

// scrollViewport is how much of the flow the column shows, in DIPs.
func (a *app) scrollViewport() float32 {
	if a.boxH <= 2*scrollInset {
		return 1
	}
	return a.boxH - 2*scrollInset
}

// maxScroll is the largest content offset the viewport can take: 0 when the
// whole book already fits in the column.
func (a *app) maxScroll() float32 {
	if a.layout == nil {
		return 0
	}
	return max(0, a.layout.ContentHeight()-a.scrollViewport())
}

// scrollTo sends the column to content y, clamped to the book. The column glides
// there over scrollGlideDuration, so repeated wheel notches stack on the target
// rather than on the frame being drawn.
func (a *app) scrollTo(y float32) {
	if a.layout == nil {
		return
	}
	// Any scrolling is a turn of the page as far as the cover is concerned,
	// and it drops a place a pending mode switch meant to restore.
	a.showCover = false
	a.jumpAt = false
	y = clampf(y, 0, a.maxScroll())
	if y == a.scrollGoal {
		return
	}
	// A column already on its way is sent somewhere else: the glide before this
	// one is cut short, which is what a wheel's notch rate does to a reader.
	if a.gliding() {
		perf.glide()
	}
	a.scrollFrom, a.scrollGoal = a.scrollY, y
	a.scrollAt = a.clock()
	if a.instant {
		a.scrollAt = time.Time{}
		a.scrollY = a.scrollGoal
	}
	a.scrollAhead()
	if a.instant {
		a.afterScroll()
	}
}

// scrollBy moves the column by dy DIPs, as one notch of a wheel or touchpad
// does, on top of whatever the notches before it have still to travel.
func (a *app) scrollBy(dy float32) { a.scrollTo(a.scrollGoal + dy) }

// gliding reports whether the column is on its way to a place it was sent to.
func (a *app) gliding() bool { return !a.scrollAt.IsZero() }

// stopScroll lands the column where it is drawn, as a press on the text does:
// the lines must not slide under a pointer that is selecting them.
func (a *app) stopScroll() {
	if a.gliding() {
		a.scrollAt = time.Time{}
		a.scrollGoal = a.scrollY
	}
}

// stepScroll advances the glide by the time since it started. It runs once per
// frame, in the build pass, so the draw callback still only replays the tiles
// the viewport now covers.
func (a *app) stepScroll() {
	if a.scrollAt.IsZero() {
		return
	}
	p := float32(a.clock().Sub(a.scrollAt)) / float32(scrollGlideDuration)
	if p >= 1 {
		a.scrollAt = time.Time{}
		a.scrollY = a.scrollGoal
		a.afterScroll()
		return
	}
	e := easeOut(p)
	a.scrollY = a.scrollFrom + (a.scrollGoal-a.scrollFrom)*e
	a.scrollAhead()
	a.afterScroll()
}

// scrollLines moves the column by lines lines of type.
func (a *app) scrollLines(lines float32) {
	line := a.opts.FontSize * a.opts.LineHeight
	a.scrollBy(lines * line)
}

// scrollScreens moves the column by screens screens, keeping a slice of the
// last one in view so the reader can see the two meet.
func (a *app) scrollScreens(screens int) {
	a.scrollBy(float32(screens) * a.scrollViewport() * 0.9)
}

// scrollAhead builds the tiles around the viewport. It runs between frames, so
// the draw callback only replays display lists that already exist.
func (a *app) scrollAhead() {
	if a.layout == nil || a.tileH <= 0 {
		return
	}
	first := int(a.scrollY / a.tileH)
	last := int((a.scrollY + a.scrollViewport()) / a.tileH)
	a.layout.Tiles(first-1, last+2)
}

// afterScroll publishes the new place to the panels and the store. The panels
// only care about the chapter, so their rows are rebuilt when it changes rather
// than on every wheel event; the store's write is debounced on its own.
func (a *app) afterScroll() {
	if a.pan == nil || a.layout == nil {
		return
	}
	// Writing the place down happens on the UI thread inside the build pass, so
	// the log counts it apart from the rest of the frame.
	if perf != nil {
		start := time.Now()
		defer func() { perf.record(time.Since(start)) }()
	}
	loc, ok := a.currentLoc()
	if !ok {
		return
	}
	if loc.Chapter != a.scrollChapter {
		a.scrollChapter = loc.Chapter
		a.pan.RefreshAt(a.book, a.layout, loc)
	}
	a.pan.RecordAt(a.layout, loc)
}

// currentLoc is the reading position in the layout's own coordinates: the top
// of the scrolling column, or the first line of the page shown.
func (a *app) currentLoc() (render.Location, bool) {
	if a.layout == nil {
		return render.Location{}, false
	}
	if a.scrolling() {
		return a.layout.LocationAt(clampf(a.scrollY, 0, a.layout.ContentHeight()))
	}
	if loc, ok := a.layout.LocationOfPage(a.page); ok {
		return loc, true
	}
	return render.Location{}, false
}

// currentChapter is the chapter the reader is in, numbered from zero.
func (a *app) currentChapter() int {
	if loc, ok := a.currentLoc(); ok {
		return max(loc.Chapter, 0)
	}
	return 0
}

// scrollProgress is how far down the column the reader is, from 0 to 1: the
// position of the viewport in the whole travel, so it starts at 0 and ends at 1.
func (a *app) scrollProgress() float64 {
	travel := a.maxScroll()
	if travel <= 0 {
		return 0
	}
	return float64(clampf(a.scrollY, 0, travel) / travel)
}

// drawScroll paints the tiles of the scrolling column that the viewport shows.
// It runs inside the box's Draw callback, so it only replays display lists it
// already has.
func (a *app) drawScroll(p *ui.Painter, r ui.Rect) {
	if a.layout == nil || a.paint == nil || a.tileH <= 0 {
		return
	}
	view := ui.Rect{X: r.X, Y: r.Y + scrollInset, W: r.W, H: a.scrollViewport()}
	if view.H < 1 {
		return
	}
	y := clampf(a.scrollY, 0, a.maxScroll())
	first := max(int(y/a.tileH)-1, 0)
	last := int((y + view.H) / a.tileH)
	if n := a.layout.TileCount(); last >= n {
		last = n - 1
	}
	// Paint from the bottom up. Every tile starts with its own opaque sheet, and
	// a line belongs to the tile that holds its top edge, so a line near a tile
	// boundary overhangs into the next tile's area: drawing the lower tile
	// first lets that overhang land on top of its paper instead of being
	// covered by it.
	for i := last; i >= first; i-- {
		tile := a.layout.Tile(i)
		if tile == nil {
			continue
		}
		box := ui.Rect{X: view.X, Y: view.Y + tile.Top() - y, W: view.W, H: tile.Height()}
		// The performance log keeps how long each tile took: a tile built on the
		// frame it is shown is what a scroll stutter looks like.
		perf.tileAt(i, func() { a.paint.DrawTile(p, tile, box, view) })
	}
	a.drawScrollMarks(p, view, y)
}

// drawScrollMarks paints the search hits and the selection over the column. It
// only fills rectangles it already has: the veils are rebuilt when the layout
// or the results change, never while drawing.
func (a *app) drawScrollMarks(p *ui.Painter, view ui.Rect, y float32) {
	pal := reader.Current()
	on := -1
	if a.pan != nil {
		on = a.pan.Search.Selected
	}
	for i, quads := range a.hitQuads {
		colour := pal.Match
		if i == on {
			colour = pal.MatchOn
		}
		a.paint.FillScrollQuads(p, view, quads, colour, y)
	}
	a.paint.FillScrollQuads(p, view, a.selQuads, pal.Select, y)
}

// scrollLocation maps a point in the column's own coordinates — as the draw
// callback and the pointer events receive them — to a document location. The
// x is measured from the element's left edge, so the page margin comes off; the
// y is below the head inset and the flow has already scrolled by scrollY.
func (a *app) scrollLocation(x, y float32) (render.Location, bool) {
	if a.layout == nil {
		return render.Location{}, false
	}
	m := a.layout.Margins()
	return a.layout.HitTest(x-m.Left, a.scrollY+(y-scrollInset))
}

// scrollSelectAll selects the lines the column is showing, the scroll-mode
// counterpart of selecting the page.
func (a *app) scrollSelectAll() {
	if a.layout == nil {
		return
	}
	v := a.layout.Viewport()
	m := a.layout.Margins()
	from, ok1 := a.layout.HitTest(1, a.scrollY+1)
	to, ok2 := a.layout.HitTest(v.Width-m.Left-m.Right-1, a.scrollY+a.scrollViewport()-1)
	if !ok1 || !ok2 {
		return
	}
	a.sel.Start(from)
	a.sel.Extend(to)
	a.selQuads = a.sel.Quads(a.layout)
	a.selCopied = 0
}

// scrollLabel is the folio's name for a scroll position: how far through the
// book the column is, as a percentage.
func (a *app) scrollLabel() string {
	if a.layout == nil {
		return "—"
	}
	return locales.ScrollPercent(int(a.scrollProgress()*100 + 0.5))
}

// chapterScreensLeft is how much of the chapter is below the column, counted in
// viewports: the scroll-mode reading of "pages left in chapter".
func (a *app) chapterScreensLeft() string {
	if a.layout == nil {
		return ""
	}
	chapter := a.currentChapter()
	end := a.layout.ContentHeight()
	if top, ok := a.layout.ChapterContentTop(chapter + 1); ok {
		end = top
	}
	view := a.scrollViewport()
	left := end - (a.scrollY + view)
	if left <= 0 {
		return locales.ChapterLastPage()
	}
	screens := int((left + view - 1) / view)
	if screens == 1 {
		return locales.ChapterOnePageLeft()
	}
	return locales.ChapterPagesLeft(screens)
}

// resumeScrollNote is the status line a scroll-mode book opens with: how far
// into the book the saved position is, and which store path found it.
func resumeScrollNote(l *render.Layout, loc render.Location, how string) string {
	if p, ok := l.Progress(loc); ok {
		return locales.ResumeScroll(int(p*100+0.5), resumeHow(how))
	}
	return locales.ResumeScrollHere(resumeHow(how))
}

// resumeHow names the store's degradation path in the language the app shows,
// so the note the reader opens with says where the position came from.
func resumeHow(how string) string {
	switch how {
	case "node":
		return locales.HowNode()
	case "anchor":
		return locales.HowAnchor()
	case "progress":
		return locales.HowProgress()
	}
	return how
}
