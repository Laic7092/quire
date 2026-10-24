package appstate

import (
	"math"
	"sort"
	"strings"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/bookmarks"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// rebuildBookmarks republishes the bookmarks panel from the store for the open
// book and the layout the panels were refreshed with.
//
// It is the only place a stored bookmark becomes a row: the layout resolves
// each recorded Position to a place (a page slot, or a content offset in a
// scrolling column), which is what the row jumps to and what its title and
// detail are worded from. A layout that cannot place a row leaves it in the
// list, muted and not clickable, rather than dropping the reader's bookmark.
func (a *App) rebuildBookmarks(bk *book.Book) {
	p := &a.Bookmarks
	selected := p.SelectedID()
	p.Entries = p.Entries[:0]
	p.Selected = -1
	if a.Store == nil || a.BookKey == "" {
		return
	}
	for _, b := range a.Store.BookmarksOf(a.BookKey) {
		e := bookmarks.Entry{ID: b.ID}
		chapter := b.Position.Chapter
		if a.layout != nil {
			if loc, how := store.Locate(a.layout, b.Position); how != "none" {
				e.Loc, e.HasLoc = loc, true
				chapter = loc.Chapter
			}
		}
		e.Title = bookmarkTitle(bk, b, chapter)
		e.Detail = a.bookmarkDetail(b.Position, e.HasLoc)
		if e.HasLoc && a.hasLoc {
			e.Current = samePlace(a.layout, e.Loc, a.loc)
		}
		p.Entries = append(p.Entries, e)
	}
	sortBookmarks(p.Entries)
	for i, e := range p.Entries {
		if e.ID == selected {
			p.Selected = i
			break
		}
	}
}

// bookmarkTitle is what names a row: the reader's own label when there is one,
// otherwise the chapter's title, otherwise its number.
func bookmarkTitle(bk *book.Book, b store.Bookmark, chapter int) string {
	if label := strings.TrimSpace(b.Label); label != "" {
		return label
	}
	if bk != nil && chapter >= 0 && chapter < len(bk.Chapters) {
		if title := strings.TrimSpace(bk.Chapters[chapter].Title); title != "" {
			return title
		}
	}
	return locales.ChapterNumber(max(chapter, 0) + 1)
}

// bookmarkDetail is where a row points, in the words of the layout on screen:
// a page on paper, a percentage in a column that has no pages. It is empty
// when the layout cannot say.
func (a *App) bookmarkDetail(p store.Position, resolved bool) string {
	if a.layout == nil || !resolved {
		return ""
	}
	if a.layout.PageCount() > 0 {
		if page, how := store.PageFor(a.layout, p); how != "none" {
			return locales.BookmarkPage(page + 1)
		}
		return ""
	}
	if p.Progress <= 0 || p.Progress > 1 {
		return ""
	}
	percent := min(int(math.Round(float64(p.Progress)*100)), 100)
	return locales.BookmarkProgress(percent)
}

// sortBookmarks orders rows the way the book reads: chapter first, then the
// node and the rune offset inside it, which the builder assigns in reading
// order. Rows the layout could not place keep their stored order at the end.
func sortBookmarks(rows []bookmarks.Entry) {
	sort.SliceStable(rows, func(i, j int) bool {
		x, y := rows[i], rows[j]
		if x.HasLoc != y.HasLoc {
			return x.HasLoc
		}
		if !x.HasLoc {
			return false
		}
		if x.Loc.Chapter != y.Loc.Chapter {
			return x.Loc.Chapter < y.Loc.Chapter
		}
		if x.Loc.Node != y.Loc.Node {
			return x.Loc.Node < y.Loc.Node
		}
		return x.Loc.Offset < y.Loc.Offset
	})
}

// Bookmarked reports whether the reading position loc already has a bookmark,
// which is what lights the toolbar's ribbon. It compares the rows the panel
// already resolved, so it costs no store lock during a frame.
func (a *App) Bookmarked(loc render.Location) bool {
	if a.layout == nil {
		return false
	}
	for _, e := range a.Bookmarks.Entries {
		if e.HasLoc && samePlace(a.layout, e.Loc, loc) {
			return true
		}
	}
	return false
}

// BookmarkHere bookmarks the reading position, unless it is bookmarked
// already. The panel's add control calls it; it reports whether a bookmark was
// made.
func (a *App) BookmarkHere() bool {
	loc, ok := a.readingLoc()
	if !ok || a.layout == nil || a.Store == nil || a.BookKey == "" {
		return false
	}
	add, ok := store.PositionAt(a.layout, loc)
	if !ok {
		return false
	}
	if a.Bookmarked(loc) {
		return false
	}
	a.Store.AddBookmark(a.BookKey, store.Bookmark{Position: add})
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	return true
}

// ToggleBookmark makes the reading position a bookmark, or drops the bookmark
// standing there, and reports whether the place is bookmarked afterwards. The
// toolbar's ribbon calls it.
func (a *App) ToggleBookmark(loc render.Location) bool {
	if a.layout == nil || a.Store == nil || a.BookKey == "" {
		return false
	}
	for _, e := range a.Bookmarks.Entries {
		if e.HasLoc && samePlace(a.layout, e.Loc, loc) {
			a.RemoveBookmark(e.ID)
			return false
		}
	}
	add, ok := store.PositionAt(a.layout, loc)
	if !ok {
		return false
	}
	a.Store.AddBookmark(a.BookKey, store.Bookmark{Position: add})
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	return true
}

// RemoveBookmark drops the bookmark with id and republishes the panel. It
// reports whether the row was there.
func (a *App) RemoveBookmark(id string) bool {
	if a.Store == nil || a.BookKey == "" || id == "" {
		return false
	}
	if !a.Store.RemoveBookmark(a.BookKey, id) {
		return false
	}
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	return true
}

// readingLoc is the place the host is reading now: the host's own idea of it
// when it has one (a scrolling column moves between refreshes), otherwise the
// place the panels were last refreshed for.
func (a *App) readingLoc() (render.Location, bool) {
	if a.Reading != nil {
		return a.Reading()
	}
	return a.loc, a.hasLoc
}

// samePlace reports whether two resolved places are the same spot of the book:
// the same page on paper, the same percent of the flow in a scrolling column.
// Comparing pages is what makes a bookmark survive a change of type size: the
// row still names the page the reader is on, though the node ids moved.
func samePlace(layout *render.Layout, x, y render.Location) bool {
	if layout == nil {
		return false
	}
	if layout.PageCount() > 0 {
		px, okx := layout.PageOf(x)
		py, oky := layout.PageOf(y)
		return okx && oky && px == py
	}
	px, okx := layout.Progress(x)
	py, oky := layout.Progress(y)
	return okx && oky && int(px*100) == int(py*100)
}
