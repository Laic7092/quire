package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/locales"
)

// handlePageInput turns presses on the sheet into the reader's pointer
// gestures: a click turns the page, and a press that holds still on a phone — or
// any drag of a mouse — marks text. The gesture is decided on release, by
// whether the pointer left its press, so the turn does not have to claim a strip
// of the text where a drag may begin.
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
		perf.scrolled(ev.DY, ev.Precise)
		// A delta a device reports as it happens is the movement itself: it lands
		// in the frame it arrives in. A notch is a distance asked for, which the
		// column glides to — and standing still, one ask looks the same whether it
		// came from a wheel, a key or a screenful.
		if ev.Precise {
			a.scrollByNow(ev.DY)
		} else {
			// A notch comes in the platform's lines, which the toolkit has
			// already turned into DIPs: the count is kept and the size is the
			// book's (see wheelDistance).
			a.scrollBy(a.wheelDistance(ev.DY))
		}
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		if a.showCover {
			// The cover is the book's own front page: a press on it opens the
			// book where the reader left off, as the first turn does. It is not
			// a selection, so the press is taken and the release ignored.
			a.dismissCover()
			return true
		}
		if a.layout == nil {
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
		a.selQuads = a.sel.Quads(a.showing())
		a.selCopied = 0
		a.pressX, a.pressY = ev.X, ev.Y
		a.pressAt = a.clock()
		a.selMoved = false
		a.selHold = false
		a.selDragging = true
		a.hapticPlayed = false
		a.selMenu = false
		a.swipePage = false
		a.dragScroll = false
		a.pressDead = false
		return true
	case ui.InputPointerMove:
		// A finger dragging the scrolling column owns the move from the first
		// DIP past the slop; a page swipe owns it until the finger lifts.
		if a.dragScroll {
			a.dragScrollTo(ev)
			return true
		}
		if a.swipePage {
			return true
		}
		if a.pressDead {
			// The press already turned into a gesture the page does not use
			// (a quick drag on a page that is not a swipe): it stays quiet
			// until the finger lifts.
			return true
		}
		// A move with no press on the sheet is the pointer passing over the
		// text, which is when a link under it shows the hand cursor.
		if !a.selDragging {
			a.trackLinkHover(ev.X, ev.Y)
			return false
		}
		if a.layout == nil {
			return false
		}
		dx, dy := ev.X-a.pressX, ev.Y-a.pressY
		past := dx > clickSlop || dx < -clickSlop || dy > clickSlop || dy < -clickSlop
		if a.phone && !a.selHold {
			if !past {
				// Still within the slop of the press: neither a scroll nor a
				// swipe has been asked for, and the hold may yet make this a
				// selection. Nothing is marked before that.
				return true
			}
			if a.clock().Sub(a.pressAt) >= touchHold {
				// The finger stayed long enough, so from here on its drag marks
				// text. This is the only way a selection begins on a phone.
				a.selHold = true
				a.longPressHaptic()
			} else {
				// A finger that moves before the hold selects nothing: on a
				// scrolling column it drags the column, on a paged book a quick
				// sideways drag swipes, and a drag that is neither leaves the
				// page alone.
				if a.scrolling() {
					a.startScrollDrag()
					a.dragScrollTo(ev)
					return true
				}
				if absf(dx) > absf(dy)*1.2 && absf(dx) > swipePageMin {
					a.swipePage = true
					a.clearSelection()
					a.selDragging = false
					return true
				}
				a.pressDead = true
				return true
			}
		}
		if !past {
			return true
		}
		if !a.selMoved {
			a.longPressHaptic()
			a.selMoved = true
		}
		loc, ok := a.pageLocation(ev.X, ev.Y)
		if !ok || loc == a.sel.Focus() {
			return true
		}
		a.sel.Extend(loc)
		a.selQuads = a.sel.Quads(a.showing())
		a.selCopied = 0
		return true
	case ui.InputPointerUp:
		if a.dragScroll {
			a.endScrollDrag()
			return true
		}
		if a.swipePage {
			a.swipePage = false
			a.selDragging = false
			delta := 1
			if ev.X > a.pressX {
				delta = -1
			}
			a.turn(delta)
			return true
		}
		if a.pressDead {
			a.pressDead = false
			a.selDragging = false
			return true
		}
		if !a.selDragging {
			return false
		}
		a.selDragging = false
		if a.phone && !a.selHold && a.clock().Sub(a.pressAt) >= touchHold {
			// The finger held without moving: that is the hold, and a hold that
			// marks nothing does not also turn the page under it.
			a.selHold = true
			a.longPressHaptic()
		}
		// A release that did not leave its press is a click. A click on a link
		// follows it. On a phone the middle of the page shows and hides the
		// chrome, and the sides turn the page; on a desktop a click on either
		// half turns it. A scrolling column has no page to turn, so its click
		// only clears the selection.
		if !a.selMoved {
			if a.phone && a.selHold {
				return true
			}
			if link, ok := a.linkAt(a.pressX, a.pressY); ok {
				a.openLink(link)
				return true
			}
			if a.phone {
				zone := a.tapZone(a.pressX)
				switch {
				case zone == 0:
					a.toggleChrome()
				case !a.scrolling():
					a.turn(zone)
				}
				return true
			}
			if !a.scrolling() {
				a.turn(a.turnAt(a.pressX))
			}
		}
		if a.phone && a.selHold && len(a.selQuads) > 0 {
			// The selection is made, so now its commands come: the card opens
			// at the finger that finished it. Nothing opens it before that —
			// a long press on the page selects, it does not pop a menu over
			// an empty page.
			a.pressX, a.pressY = ev.X, ev.Y
			a.selMenu = true
		}
		return true
	}
	return false
}

// touchHold is how long a finger may stay still on the sheet before its drag is
// taken as selecting text rather than as scrolling the column or turning the
// page. It is the system's own long press, half a second: any shorter and a
// reader who lands a thumb and thinks for a moment before dragging gets a
// selection where they meant to scroll.
const touchHold = 500 * time.Millisecond

// swipePageMin is how far a finger must go sideways before a paged book reads
// it as a swipe rather than as a tap.
const swipePageMin = 36

// startScrollDrag hands the press to the scrolling column: the selection the
// press began is dropped, and the column follows the finger from where it was.
func (a *app) startScrollDrag() {
	a.clearSelection()
	a.dragScroll = true
	a.selDragging = false
	a.dragFrom = a.scrollY
	a.dragY = a.pressY
	a.dragAt = a.clock()
	a.dragV = 0
	a.stopScroll()
}

// dragScrollTo moves the column with the finger: content follows it, so a finger
// going down takes the page back up the flow.
func (a *app) dragScrollTo(ev ui.InputEvent) {
	now := a.clock()
	// A speed read over a gap shorter than flingSample is mostly the noise of
	// when two events happened to arrive, and a spike there throws the column
	// much further than the hand ever moved. The gap is measured from the last
	// sample that counted, and the position is still the newest one.
	if dt := now.Sub(a.dragAt); dt >= flingSample {
		a.dragV = 0.7*a.dragV + 0.3*float32(-(ev.Y-a.dragY)/float32(dt.Seconds()))
		a.dragAt = now
	}
	a.dragY = ev.Y
	a.scrollNow(a.dragFrom - (ev.Y - a.pressY))
}

// endScrollDrag carries the column on with the speed the finger lifted with: a
// flick, whose motion the frames spend as it decays (see startFling).
func (a *app) endScrollDrag() {
	a.dragScroll = false
	a.selDragging = false
	if a.clock().Sub(a.dragAt) > flingIdle {
		// The finger rested before it let go: it stopped, and a stop is no flick.
		a.dragV = 0
	}
	// Only a flick that went the way the drag went carries the column on. A hand
	// that scrolled up and back is leaving the column where it put it, and the
	// last stroke's speed, pointing the other way, is not a throw: carrying it
	// on would make the page spring back a section after the finger lifted.
	if net := a.scrollY - a.dragFrom; net*a.dragV > 0 {
		a.startFling(a.dragV)
	}
	a.dragV = 0
}

// toggleChrome shows or hides the reading bars, as a tap in the middle of the
// page does: on a phone it works in a paged book too, where scrolling never asks
// for the room.
func (a *app) toggleChrome() {
	a.chromeAway = !a.chromeAway
}

// playHaptic is the app's only road to the phone's motor. It is a variable so a
// test can count what a gesture asks for without a device in the hand.
var playHaptic = func() { mygo.Haptics.Selection() }

// longPressHaptic is the one gesture that earns a haptic: a finger that stays
// still on the page until the hold is a selection starts, and then moves. It
// ticks once per press, however far the finger then drags.
//
// Everything else is silent on purpose. Turning a page, swiping, a tap, a tab
// or a chrome toggle are things a reader does dozens of times a minute; a motor
// under the thumb for each of them is noise, not feedback. So no other call
// site plays a haptic, and this gate is where a new one would have to argue for
// itself.
func (a *app) longPressHaptic() {
	if !a.phone || a.hapticPlayed || a.clock().Sub(a.pressAt) < touchHold {
		return
	}
	a.hapticPlayed = true
	playHaptic()
}

// tapZone maps a tap at x to what it does on a phone: the left side turns back,
// the right side turns on, and the middle shows or hides the bars.
func (a *app) tapZone(x float32) int {
	switch {
	case a.boxW <= 0:
		return 0
	case x < a.boxW*0.28:
		return -1
	case x > a.boxW*0.72:
		return 1
	}
	return 0
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
	l, index := a.sheetPlan(a.page)
	if l == nil {
		return render.Location{}, false
	}
	width := a.boxW / float32(a.pagesOnSheet())
	if a.pagesOnSheet() == 2 && x >= a.boxW/2 && index+1 < l.PageCount() {
		index++
		x -= width
	}
	page := l.Page(index)
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
	a.selMenu = false
	a.pressDead = false
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
	l, page := a.sheetPlan(a.page)
	pages := a.pagesFrom(l, page)
	if len(pages) == 0 {
		return
	}
	first, last := l.Page(pages[0]), l.Page(pages[len(pages)-1])
	if first == nil || last == nil {
		return
	}
	v := l.Viewport()
	margin := a.opts.Margin
	from, ok1 := first.HitTest(paint.Point{X: margin, Y: margin})
	to, ok2 := last.HitTest(paint.Point{X: v.Width - margin, Y: v.Height - margin})
	if !ok1 || !ok2 {
		return
	}
	a.sel.Start(from)
	a.sel.Extend(to)
	a.selQuads = a.sel.Quads(l)
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

// refreshMarks rebuilds the search hits' and the notes' rectangles for the
// layout the sheet draws (see showing). The engine shapes each range, so this
// runs when a layout or a set of results or notes arrives, never while drawing:
// a resize makes a new preview, which rebuilds them for the new geometry.
func (a *app) refreshMarks() {
	a.hitQuads = nil
	l := a.showing()
	a.marksOn = l
	if l == nil || a.pan == nil || a.pan.Index == nil || a.pan.Index.Stale(l.Document()) {
		a.refreshNoteMarks()
		return
	}
	hits := a.pan.Search.Hits
	quads := make([][]render.SelectionQuad, 0, len(hits))
	for _, h := range hits {
		quads = append(quads, l.Selection(h.Loc, h.End))
	}
	a.hitQuads = quads
	a.refreshNoteMarks()
}

// drawMarks paints the notes, the search hits and the selection over one page
// of the sheet. It runs inside the box's Draw callback and only fills
// rectangles it already has, for the layout they were built against: a page
// drawn from another layout — a preview that does not hold it — gets no veils
// rather than the right rectangles in the wrong place.
func (a *app) drawMarks(p *ui.Painter, box ui.Rect, l *render.Layout, page int) {
	if a.marksOn != l {
		return
	}
	pal := a.palette()
	a.drawNoteMarks(p, box, page)
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
	if m.Item(locales.AddNote()).Disabled(a.pan == nil || text == "").Chosen() {
		a.addNoteFromSelection()
	}
	if label := searchLabel(text); label != "" {
		// The index may still be waiting for its first query; CanSearch also
		// counts the document's lazy builder.
		canSearch := a.pan != nil && a.pan.CanSearch()
		if m.Item(label).Disabled(!canSearch).Chosen() {
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

// selectionMenu is the phone's card over a finished selection: the commands of
// the page's context menu, on a finger's rows, opening at the point the finger
// lifted. It is built only while a selection is on the page, so nothing pops
// over an empty page.
func (a *app) selectionMenu(c *ui.Context) {
	anchor := ui.Box(c).Absolute().Left(a.pressX).Top(a.pressY).Size(0, 0)
	m := ui.ContextMenuBase(c, anchor, &a.selMenu)
	m.Popup(func(panel ui.Element) {
		t := c.Theme()
		panel.Background(t.Surface).Radius(14).Padding(6).Width(212)
		a.selectionMenuItems(c, m)
	})
}

// selectionMenuItems fills the card with what the page menu offers, as rows.
func (a *app) selectionMenuItems(c *ui.Context, m ui.MenuParts) {
	t := c.Theme()
	text := a.selectedText()
	row := func(label string, enabled bool) ui.Element {
		it := m.Item().Padding(12, 14).Radius(10)
		it.Disabled(!enabled)
		if enabled && it.Hovered() {
			it.Background(t.SurfaceHover)
		}
		it.Children(func() {
			ui.Text(c, label).FontSize(15).TextColor(t.Text)
		})
		return it
	}
	if it := row(locales.MenuCopy(), text != ""); it.Clicked() {
		a.copySelection(c)
	}
	if it := row(locales.AddNote(), a.pan != nil && text != ""); it.Clicked() {
		a.addNoteFromSelection()
	}
	if label := searchLabel(text); label != "" {
		canSearch := a.pan != nil && a.pan.CanSearch()
		if it := row(label, canSearch); it.Clicked() {
			a.searchSelection(text)
		}
	}
	if it := row(locales.MenuSelectPage(), !a.showCover && a.layout != nil); it.Clicked() {
		a.selectPage()
	}
	if it := row(locales.MenuClearSelection(), len(a.selQuads) > 0); it.Clicked() {
		a.clearSelection()
	}
}
