package appstate

import (
	"math"
	"sort"
	"strings"

	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/bookmarks"
	"github.com/laix/quire/internal/store"
	"github.com/laix/quire/locales"
	"github.com/laic7092/layx/render"
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
		e.Title = annotationTitle(bk, b.Label, chapter)
		e.Detail = a.annotationDetail(b.Position, e.HasLoc)
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

// annotationTitle is what names a row: the reader's own label when there is one,
// otherwise the chapter's title, otherwise its number.
func annotationTitle(bk *book.Book, label string, chapter int) string {
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	if bk != nil && chapter >= 0 && chapter < len(bk.Chapters) {
		if title := strings.TrimSpace(bk.Chapters[chapter].Title); title != "" {
			return title
		}
	}
	return locales.ChapterNumber(max(chapter, 0) + 1)
}

// annotationDetail is where a row points, in the words of the layout on screen:
// a page on paper, a percentage in a column that has no pages. It is empty
// when the layout cannot say.
func (a *App) annotationDetail(p store.Position, resolved bool) string {
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
	layout := a.currentLayout()
	if layout == nil {
		return false
	}
	for _, e := range a.Bookmarks.Entries {
		if e.HasLoc && samePlace(layout, e.Loc, loc) {
			return true
		}
	}
	return false
}

// currentLayout is the layout the panels were refreshed with, when it still
// describes the book that is open. Between opening a book and refreshing for
// its first layout it does not: the layout in hand is the one before it, and a
// row resolved against it would be stored at a place of another book.
func (a *App) currentLayout() *render.Layout {
	if a.layout == nil || a.placed != a.BookKey {
		return nil
	}
	return a.layout
}

// BookmarkHere bookmarks the reading position, unless it is bookmarked
// already. The panel's add control calls it; it reports whether a bookmark was
// made.
func (a *App) BookmarkHere() bool {
	layout := a.currentLayout()
	loc, ok := a.readingLoc()
	if !ok || layout == nil || a.Store == nil || a.BookKey == "" {
		return false
	}
	if _, exists := a.bookmarkAt(loc); exists {
		return false
	}
	add, ok := store.PositionAt(layout, loc)
	if !ok {
		return false
	}
	a.Store.AddBookmark(a.BookKey, store.Bookmark{Position: add})
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	return true
}

// ToggleBookmark makes the reading position a bookmark, or drops the bookmark
// standing there. It reports whether the place is bookmarked afterwards and
// whether the store changed: a click that could not be acted on (no layout for
// this book yet, no store) changes nothing and must not claim a bookmark was
// removed. The toolbar's ribbon calls it.
func (a *App) ToggleBookmark(loc render.Location) (bookmarked, changed bool) {
	layout := a.currentLayout()
	if layout == nil || a.Store == nil || a.BookKey == "" {
		return false, false
	}
	if old, exists := a.bookmarkAt(loc); exists {
		if !a.RemoveBookmark(old.ID) {
			return true, false
		}
		return false, true
	}
	add, ok := store.PositionAt(layout, loc)
	if !ok {
		return false, false
	}
	a.Store.AddBookmark(a.BookKey, store.Bookmark{Position: add})
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	return true, true
}

// bookmarkAt returns the bookmark standing at loc. It reads the store, not the
// rows: adding and removing are rare one-off actions, and a page turn between
// two of them must not leave a stale copy of the rows in the way.
func (a *App) bookmarkAt(loc render.Location) (store.Bookmark, bool) {
	layout := a.currentLayout()
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return store.Bookmark{}, false
	}
	for _, b := range a.Store.BookmarksOf(a.BookKey) {
		if p, how := store.Locate(layout, b.Position); how != "none" && samePlace(layout, p, loc) {
			return b, true
		}
	}
	return store.Bookmark{}, false
}

// RemoveBookmark drops the bookmark with id and republishes the panel. It
// reports whether the row was there. The row that takes its place is chosen, so
// a reader deleting several rows keeps a hand on the list and the panel does
// not fall back to none after every delete.
func (a *App) RemoveBookmark(id string) bool {
	if a.Store == nil || a.BookKey == "" || id == "" {
		return false
	}
	index := indexOfBookmark(a.Bookmarks.Entries, id)
	chosen := a.Bookmarks.SelectedID() == id
	if !a.Store.RemoveBookmark(a.BookKey, id) {
		return false
	}
	a.Store.SaveSoon(0)
	a.rebuildBookmarks(a.book)
	if chosen {
		a.selectBookmarkAt(index)
	}
	return true
}

// selectBookmarkAt chooses the row at index i, clamped to the rows left: the
// one that moved up into the deleted row's place, or the last one.
func (a *App) selectBookmarkAt(i int) {
	if i < 0 || len(a.Bookmarks.Entries) == 0 {
		return
	}
	a.Bookmarks.Selected = min(i, len(a.Bookmarks.Entries)-1)
}

// indexOfBookmark is the row index of the bookmark with id, -1 when the panel
// does not list it.
func indexOfBookmark(rows []bookmarks.Entry, id string) int {
	for i, e := range rows {
		if e.ID == id {
			return i
		}
	}
	return -1
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
