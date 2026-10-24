package main

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// handlePageInput turns presses on the sheet into the reader's two pointer
// gestures: a click turns the page, and a press that moves selects text. The
// gesture is decided on release, by whether the pointer left its press, so the
// turn does not have to claim a strip of the text where a drag may begin.
//
// The page box takes the pointer when it is pressed, so its moves and its
// release come here wherever the pointer goes; a drag past the edge of the
// sheet clamps to the page being shown, which is all the reader can mark.
//
// It runs on the UI thread, between frames, so it recomputes the selection's
// rectangles here and the draw callback only fills what it is given.
func (a *app) handlePageInput(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputScroll:
		// A scrolling column takes the wheel and the touchpad itself; a paged
		// book leaves the event to whatever is around it.
		if !a.scrolling() || a.layout == nil {
			return false
		}
		a.scrollBy(ev.DY)
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 || a.showCover || a.layout == nil {
			return false
		}
		a.stopScroll()
		loc, ok := a.pageLocation(ev.X, ev.Y)
		if !ok {
			return false
		}
		// A press is a new anchor: a click clears what was selected, because
		// its zero-length range paints no rectangle and copies no text.
		a.sel.Start(loc)
		a.selQuads = a.sel.Quads(a.layout)
		a.selCopied = 0
		a.pressX, a.pressY = ev.X, ev.Y
		a.selMoved = false
		a.selDragging = true
		return true
	case ui.InputPointerMove:
		// A move with no press on the sheet is the pointer passing over the
		// text, which is when a link under it shows the hand cursor.
		if !a.selDragging {
			a.trackLinkHover(ev.X, ev.Y)
			return false
		}
		if a.layout == nil {
			return false
		}
		if dx, dy := ev.X-a.pressX, ev.Y-a.pressY; dx > clickSlop || dx < -clickSlop || dy > clickSlop || dy < -clickSlop {
			a.selMoved = true
		}
		loc, ok := a.pageLocation(ev.X, ev.Y)
		if !ok || loc == a.sel.Focus() {
			return true
		}
		a.sel.Extend(loc)
		a.selQuads = a.sel.Quads(a.layout)
		a.selCopied = 0
		return true
	case ui.InputPointerUp:
		if !a.selDragging {
			return false
		}
		a.selDragging = false
		// A release that did not leave its press is a click. A click on a link
		// follows it, and any other click turns the page: inside the window a
		// pointer may wobble a little without meaning to select anything. A
		// scrolling column has no page to turn, so its click only clears the
		// selection.
		if !a.selMoved {
			if link, ok := a.linkAt(a.pressX, a.pressY); ok {
				a.openLink(link)
				return true
			}
			if !a.scrolling() {
				a.turn(a.turnAt(a.pressX))
			}
		}
		return true
	}
	return false
}

// clickSlop is how far a pointer may wobble during a click, in DIPs, before the
// press counts as a drag that selects.
const clickSlop = 4

// turnAt maps a click at x, in the sheet's own coordinates, to a page turn: the
// left half of the sheet goes back and the right half on, as clicking the two
// pages of an open book does. A click reads no text, so it may anywhere on the
// sheet; dragging is what keeps the text for a selection.
func (a *app) turnAt(x float32) int {
	switch {
	case a.boxW <= 0:
		return 0
	case x < a.boxW/2:
		return -1
	}
	return 1
}

// pageLocation maps a point in the sheet's own coordinates, as the draw callback
// receives them, to a document location. Page coordinates include the margins,
// and Page.HitTest clamps to the nearest line or image, so a point off the text
// block still names a place. The right half of an open book is its second page.
func (a *app) pageLocation(x, y float32) (render.Location, bool) {
	if a.layout == nil {
		return render.Location{}, false
	}
	if a.scrolling() {
		return a.scrollLocation(x, y)
	}
	index := a.page
	width := a.boxW / float32(a.pagesOnSheet())
	if a.pagesOnSheet() == 2 && x >= a.boxW/2 && index+1 < a.layout.PageCount() {
		index++
		x -= width
	} else if a.pagesOnSheet() == 2 {
		x -= float32(index-a.page) * width
	}
	page := a.layout.Page(index)
	if page == nil {
		return render.Location{}, false
	}
	return page.HitTest(paint.Point{X: x, Y: y})
}

// clearSelection drops the selected text, as a new book or a relayout must:
// locations name nodes of one layout only.
func (a *app) clearSelection() {
	a.sel.Clear()
	a.selQuads = nil
	a.selDragging = false
	a.selMoved = false
	a.selCopied = 0
}

// copySelection puts the selected text on the clipboard, read from the document
// the layout was built from. It reports whether there was anything to copy.
func (a *app) copySelection(c *ui.Context) bool {
	text := a.selectedText()
	if text == "" {
		return false
	}
	c.WriteClipboard(text)
	a.selCopied = utf8.RuneCountInString(text)
	return true
}

// copyPageText is copySelection for the menu bar, which runs outside a build
// and so has no frame's Context to write through. The services kept from the
// last frame are the same clipboard.
func (a *app) copyPageText() {
	text := a.selectedText()
	if text == "" {
		return
	}
	a.services.WriteClipboard(text)
	a.selCopied = utf8.RuneCountInString(text)
}

// selectedText is what the selection reads now, trimmed: "" when it holds
// nothing, as after a click.
func (a *app) selectedText() string {
	return strings.TrimSpace(a.sel.Text(a.doc))
}

// selectPage selects everything on the page being read, so a reader can copy a
// whole page by hand. The layout's viewport is the page: its content box starts
// one margin in and ends one margin out, and HitTest clamps the corners to the
// first and last line or image. A scrolling column selects what it shows.
func (a *app) selectPage() {
	if a.layout == nil || a.showCover {
		return
	}
	if a.scrolling() {
		a.scrollSelectAll()
		return
	}
	pages := a.pagesFrom(a.page)
	if len(pages) == 0 {
		return
	}
	first, last := a.layout.Page(pages[0]), a.layout.Page(pages[len(pages)-1])
	if first == nil || last == nil {
		return
	}
	v := a.layout.Viewport()
	margin := a.opts.Margin
	from, ok1 := first.HitTest(paint.Point{X: margin, Y: margin})
	to, ok2 := last.HitTest(paint.Point{X: v.Width - margin, Y: v.Height - margin})
	if !ok1 || !ok2 {
		return
	}
	a.sel.Start(from)
	a.sel.Extend(to)
	a.selQuads = a.sel.Quads(a.layout)
	a.selCopied = 0
}

// searchQuery turns selected text into one search of the index: the first line,
// cut short, since the index looks for a phrase rather than a paragraph.
func searchQuery(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > searchQueryMax {
		runes = runes[:searchQueryMax]
	}
	return string(runes)
}

// searchQueryMax is how many runes of a selection become the query.
const searchQueryMax = 40

// searchSelection opens the search panel with the selected text as its query.
func (a *app) searchSelection(text string) {
	query := searchQuery(text)
	if query == "" || a.pan == nil || a.pan.Search.OnSearch == nil {
		return
	}
	a.pan.Show(appstate.SidebarSearch)
	a.pan.Search.Query = query
	a.pan.Search.OnSearch(query)
}

// searchLabel is the context menu item that searches the selection. The phrase
// is cut so a dragged paragraph does not stretch the menu wide; "" means the
// menu gets no such item.
func searchLabel(text string) string {
	query := searchQuery(text)
	if query == "" {
		return ""
	}
	runes := []rune(query)
	if len(runes) > 12 {
		query = string(runes[:12]) + "…"
	}
	return locales.SearchSelection(query)
}

// refreshMarks rebuilds the search hits' rectangles for the current layout. The
// engine shapes each range, so this runs when a layout or a set of results
// arrives, never while drawing.
func (a *app) refreshMarks() {
	a.hitQuads = nil
	if a.layout == nil || a.pan == nil || a.pan.Index == nil {
		return
	}
	hits := a.pan.Search.Hits
	quads := make([][]render.SelectionQuad, 0, len(hits))
	for _, h := range hits {
		quads = append(quads, a.layout.Selection(h.Loc, h.End))
	}
	a.hitQuads = quads
}

// drawMarks paints the search hits and the selection over one page of the sheet.
// It runs inside the box's Draw callback and only fills rectangles it already
// has.
func (a *app) drawMarks(p *ui.Painter, box ui.Rect, page int) {
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
		a.paint.FillQuads(p, box, page, quads, colour)
	}
	a.paint.FillQuads(p, box, page, a.selQuads, pal.Select)
}

// pageMenu fills the page's context menu, the one the secondary button, the
// menu key or Shift+F10 opens on the sheet. It runs twice per choice: once to
// collect the items and once after one was picked, so it must add the same
// items in the same order both times — the labels and the order are how the
// choice is found again.
func (a *app) pageMenu(c *ui.Context, m *ui.Menu) {
	text := a.selectedText()
	if m.Item(locales.MenuCopy()).Shortcut(ui.Cmd, ui.KeyC).Disabled(text == "").Chosen() {
		a.copySelection(c)
	}
	if label := searchLabel(text); label != "" {
		hasIndex := a.pan != nil && a.pan.Index != nil
		if m.Item(label).Disabled(!hasIndex).Chosen() {
			a.searchSelection(text)
		}
	}
	if m.Item(locales.MenuSelectPage()).Disabled(a.showCover || a.layout == nil).Chosen() {
		a.selectPage()
	}
	m.Separator()
	if m.Item(locales.MenuClearSelection()).Disabled(len(a.selQuads) == 0).Chosen() {
		a.clearSelection()
	}
}
