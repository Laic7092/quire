package main

import (
	"github.com/laix/layx-reader/locales"
)

// bookmarkHere bookmarks the page being read and says so in the status line.
// The bookmark panel's add control calls it through appstate; the toolbar's
// ribbon and ⌘D call toggleBookmark, which also removes a bookmark already on
// the page.
func (a *app) bookmarkHere() {
	if a.pan == nil || a.showCover {
		return
	}
	if !a.pan.BookmarkHere() {
		return
	}
	a.say(locales.BookmarkAdded())
}

// toggleBookmark is the round ribbon in the title bar and ⌘D: it bookmarks the
// reading position, or drops the bookmark that is already there. A click the
// reader's book cannot answer — no layout yet, no place to record — leaves the
// status line alone instead of reporting a removal that did not happen.
func (a *app) toggleBookmark() {
	if a.pan == nil || a.showCover {
		return
	}
	loc, ok := a.currentLoc()
	if !ok {
		return
	}
	bookmarked, changed := a.pan.ToggleBookmark(loc)
	if !changed {
		return
	}
	if bookmarked {
		a.say(locales.BookmarkAdded())
		return
	}
	a.say(locales.BookmarkRemoved())
}

// bookmarkedAt reports whether the reading position has a bookmark, which is
// what lights the toolbar's ribbon. It reads the rows the panel already
// resolved, so it is cheap enough for every frame.
func (a *app) bookmarkedAt() bool {
	if a.pan == nil || a.layout == nil || a.showCover {
		return false
	}
	loc, ok := a.currentLoc()
	if !ok {
		return false
	}
	return a.pan.Bookmarked(loc)
}
