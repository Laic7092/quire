package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// In-book links are the one thing the XHTML converter hands over that the
// reader still has to act on: `epub` turns every `<a href>` into
// render.Node.Link and gives the host the rectangles it occupies
// (Page.Links / Tile.Links). A click inside one either moves inside the book —
// Book.ResolveLink turns the href into a chapter and a render.AnchorID — or
// opens in the system browser.

// linkAt returns the link at a point of the sheet or column, in the same
// coordinates the pointer events arrive in.
func (a *app) linkAt(x, y float32) (render.Link, bool) {
	if a.layout == nil {
		return render.Link{}, false
	}
	if a.scrolling() {
		return a.scrollLinkAt(x, y)
	}
	index := a.page
	width := a.boxW / float32(a.pagesOnSheet())
	if a.pagesOnSheet() == 2 && x >= a.boxW/2 && index+1 < a.layout.PageCount() {
		index++
		x -= width
	}
	page := a.layout.Page(index)
	if page == nil {
		return render.Link{}, false
	}
	for _, link := range page.Links() {
		if hitRect(link.Rect, x, y) {
			return link, true
		}
	}
	return render.Link{}, false
}

// scrollLinkAt finds the link under a point of the column. A tile's link
// rectangles are tile-local, so the point moves into the tile that holds it:
// the same arithmetic drawScroll uses to place the tile on screen.
func (a *app) scrollLinkAt(x, y float32) (render.Link, bool) {
	if a.tileH <= 0 {
		return render.Link{}, false
	}
	y0 := clampf(a.scrollY, 0, a.maxScroll())
	content := y0 + (y - scrollInset)
	tile := a.layout.Tile(int(content / a.tileH))
	if tile == nil {
		return render.Link{}, false
	}
	local := content - tile.Top()
	for _, link := range tile.Links() {
		if hitRect(link.Rect, x, local) {
			return link, true
		}
	}
	return render.Link{}, false
}

// hitRect reports whether a point is inside a rectangle.
func hitRect(r paint.Rect, x, y float32) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

// trackLinkHover keeps the cursor in step with the text under the pointer: a
// link gets the pointer cursor, everything else the arrow. It runs on pointer
// moves, which is also when the sheet is hovered, and asks for a frame only
// when the answer changes — the cursor is set while the frame is built.
func (a *app) trackLinkHover(x, y float32) {
	if a.layout == nil || a.showCover {
		if a.hoverLink {
			a.hoverLink = false
			a.services.Invalidate()
		}
		return
	}
	_, over := a.linkAt(x, y)
	if over == a.hoverLink {
		return
	}
	a.hoverLink = over
	a.services.Invalidate()
}

// linkCursor is the cursor the sheet asks for: a hand over a link, as a browser
// shows, and the arrow everywhere else on the page.
func (a *app) linkCursor() ui.Cursor {
	if a.hoverLink {
		return ui.CursorPointer
	}
	return ui.CursorDefault
}

// openLink follows a link the reader clicked: an in-book target moves the
// reading position, an external URL goes to the system browser. A scheme the
// system would treat as a program or a script is left alone, which is what
// ui.SafeToOpen is for.
func (a *app) openLink(link render.Link) {
	if a.book != nil {
		if target, ok := a.book.ResolveLink(link.URL); ok {
			if a.layout == nil {
				return
			}
			if loc, ok := a.layout.Locate(target.Anchor); ok {
				a.gotoLoc(loc)
			}
			return
		}
	}
	if ui.SafeToOpen(link.URL) {
		a.services.OpenURL(link.URL)
	}
}
