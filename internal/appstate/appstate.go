// Package appstate wires the reader's floating panels — table of contents,
// bookmarks, notes and full-text search — to the persisted state, so main.go
// stays a thin shell:
// it owns the window, the render document and the painter, and calls Refresh
// whenever one of those changes.
//
// The package never touches a Window: background work publishes its result
// through Post, which the host sets to Window.Update. Everything else is plain
// fields, which keeps the whole panel layer testable with ui.NewTester.
package appstate

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/bookmarks"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/search"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/internal/toc"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// Sidebar identifiers, also what Prefs.Sidebar stores. SidebarNone closes the
// panel. The reader floats the open one over the page; it never narrows it.
const (
	SidebarNone      = ""
	SidebarToc       = "toc"
	SidebarBookmarks = "bookmarks"
	SidebarNotes     = "notes"
	SidebarSearch    = "search"
)

// App is the panel layer's state. Build it with New, then let the host fill the
// hooks before the first frame.
type App struct {
	Store   *store.Store
	Library *library.Library

	// Sidebar is the open panel; SidebarNone means none.
	Sidebar string
	// BookKey is the store key of the open book, "" for none.
	BookKey string
	// Index is the full-text index of the current document; nil until the host
	// publishes one after a build.
	Index *search.Index

	Shelf     library.Panel
	Toc       toc.Panel
	Bookmarks bookmarks.Panel
	Search    search.Panel

	// Post publishes fn on the UI thread (Window.Update). When nil, background
	// work runs inline, which is what the tests want.
	Post func(fn func())
	// Open opens a book path; the host parses it off the UI thread.
	Open func(path string)
	// Jump shows a location of the current book: a table of contents row or a
	// search hit. The host turns it into a page or a scroll offset, whichever
	// the layout's mode uses.
	Jump func(loc render.Location)
	// Reading returns the place the host is reading now, which is the place a
	// bookmark is made at. A scrolling column passes its viewport's top, so the
	// bookmark lands where the reader is rather than at the last panel refresh;
	// without the hook the last refreshed location is used.
	Reading func() (render.Location, bool)
	// OnHits runs on the UI thread whenever Search.Hits changes, so the host can
	// rebuild what it derives from them, such as the highlight rectangles it
	// paints on the page.
	OnHits func()
	// OnPickDir runs when the shelf asks for a library directory.
	OnPickDir func()
	// Cover returns a book's decoded cover for the shelf, or nil. The host
	// caches the bitmaps; the panel never decodes one per frame.
	Cover func(entry library.Entry) *ui.Bitmap

	layout   *render.Layout
	page     int
	searchID int
	// book is the book the panels were last refreshed for, nil when none is
	// open; the bookmark rows take their chapter names from it.
	book *book.Book
	// loc is the place the panels were last refreshed for, and hasLoc whether
	// that refresh had a layout to read one from. The bookmarks panel marks the
	// row standing there.
	loc    render.Location
	hasLoc bool
}

// New builds an App over st. st may be nil, in which case nothing is persisted
// and the shelf has no directory until SetLibraryDir is called.
func New(st *store.Store) *App {
	a := &App{Store: st}
	a.Shelf.Selected = -1
	a.Toc.Selected = -1

	if st != nil {
		prefs := st.State().Prefs
		if prefs.SidebarOpen {
			a.Sidebar = normalizeSidebar(prefs.Sidebar)
		}
	}
	a.Shelf.Cover = func(entry library.Entry) *ui.Bitmap {
		if a.Cover == nil {
			return nil
		}
		return a.Cover(entry)
	}

	a.Shelf.OnOpen = a.open
	a.Shelf.OnReload = a.ReloadShelf

	a.Toc.OnJump = a.jumpEntry
	a.Search.OnJump = a.jump
	a.Bookmarks.Selected = -1
	a.Bookmarks.OnJump = func(e bookmarks.Entry) {
		if e.HasLoc {
			a.jump(e.Loc)
		}
	}
	a.Bookmarks.OnRemove = func(e bookmarks.Entry) { a.RemoveBookmark(e.ID) }
	a.Bookmarks.OnAdd = func() { a.BookmarkHere() }
	a.Search.OnSearch = a.search
	a.Search.PageOf = func(h search.Hit) (int, bool) {
		if a.layout == nil || a.Index == nil {
			return 0, false
		}
		return search.Page(a.layout, a.Index, h)
	}
	return a
}

// SetLibraryDir points the shelf at dir and rescans it in the background.
func (a *App) SetLibraryDir(dir string) {
	if a.Library == nil {
		a.Library = &library.Library{}
	}
	a.Library.Dir = dir
	a.Library.Entries = nil
	a.Shelf.Lib = a.Library
	a.Shelf.Status = ""
	a.ReloadShelf()
}

// ReloadShelf rescans the library off the UI thread.
func (a *App) ReloadShelf() {
	if a.Library == nil || a.Library.Dir == "" {
		a.Shelf.Status = locales.ShelfChooseFolderFirst()
		return
	}
	lib := a.Library
	a.Shelf.Status = locales.ShelfScanning()
	// Scan off the UI thread, then install the result on it: the shelf reads
	// Entries while it draws, so the slice must not be swapped underneath.
	a.async(func() func() {
		entries, err := library.Scan(lib.Dir)
		return func() {
			if err != nil {
				a.Shelf.Status = locales.ShelfScanFailed(err.Error())
				return
			}
			lib.Entries = entries
			a.Shelf.Status = locales.ShelfCount(len(entries))
		}
	})
}

// Refresh republishes the panels for a new layout, book or page. Hosts call it
// after a build, after a relayout and after every page turn. It is the paged
// form; in a scroll layout use RefreshAt, which names the place by location.
func (a *App) Refresh(bk *book.Book, layout *render.Layout, page int) {
	loc := render.Location{}
	if layout != nil {
		if l, ok := layout.LocationOfPage(page); ok {
			loc = l
		}
	}
	a.refreshAt(bk, layout, loc, page)
}

// RefreshAt is Refresh for a reading position: the page (when the layout has
// pages) and the current chapter both follow the location, so a scroll layout,
// which has no pages, still marks the chapter being read.
func (a *App) RefreshAt(bk *book.Book, layout *render.Layout, loc render.Location) {
	page := -1
	if layout != nil && layout.PageCount() > 0 {
		if n, ok := layout.PageOf(loc); ok {
			page = n
		}
	}
	a.refreshAt(bk, layout, loc, page)
}

// refreshAt rebuilds the panels from the layout and the place read to.
func (a *App) refreshAt(bk *book.Book, layout *render.Layout, loc render.Location, page int) {
	a.layout, a.page = layout, page
	a.book = bk
	a.loc, a.hasLoc = loc, layout != nil
	a.Toc.Entries = toc.BuildAt(bk, layout, loc)
	if len(a.Toc.Entries) > 0 {
		a.Toc.SelectCurrent()
	}
	a.rebuildBookmarks(bk)
}

// SetIndex publishes a freshly built index. The host must call it with the
// document the next Refresh's layout was built from, or hits resolve to the
// wrong page: a new document means a new index.
func (a *App) SetIndex(ix *search.Index) {
	a.Index = ix
	a.Search.Hits = nil
	a.Search.Selected = -1
	a.Search.Status = ""
	a.searchID++
	a.hitsChanged()
}

// Opened records bk as the open book, remembers it in the store and returns its
// key. path is the file the host opened, for size and modification time.
func (a *App) Opened(bk *book.Book, path string) string {
	if bk == nil {
		return ""
	}
	key := store.Key(path, fileSize(path), fileModTime(path))
	if a.Store != nil {
		a.Store.TouchBook(store.Book{
			Key:      key,
			Path:     path,
			Title:    bk.Title,
			Format:   formatOf(path),
			Size:     fileSize(path),
			Modified: fileModTime(path),
		})
		a.Store.SaveSoon(0)
	}
	a.BookKey = key
	a.Search.Query = ""
	a.Search.Hits = nil
	a.Search.Status = ""
	a.searchID++
	a.hitsChanged()
	return key
}

// Resume returns the page to open the book at, from the position saved for the
// current key. how is the store's degradation path ("node", "anchor",
// "progress", "chapter"), and empty when nothing is saved. It needs a paged
// layout: a scroll layout has no pages, so use ResumeAt.
func (a *App) Resume(layout *render.Layout) (page int, how string) {
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return 0, ""
	}
	p, ok := a.Store.Position(a.BookKey)
	if !ok {
		return 0, ""
	}
	page, how = store.PageFor(layout, p)
	if page < 0 {
		return 0, ""
	}
	return page, how
}

// ResumeAt returns the location to open the book at, in either mode.
func (a *App) ResumeAt(layout *render.Layout) (loc render.Location, how string) {
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return render.Location{}, ""
	}
	p, ok := a.Store.Position(a.BookKey)
	if !ok {
		return render.Location{}, ""
	}
	return store.Locate(layout, p)
}

// RecordPage saves the reading position of the current book. A burst of page
// turns costs one write, two seconds after the last.
func (a *App) RecordPage(layout *render.Layout) {
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return
	}
	p, ok := store.PositionFor(layout, a.page)
	if !ok {
		return
	}
	a.record(p)
}

// RecordAt saves the reading location in either mode: a paged host records the
// position of the page, a scroll host the position at the top of the viewport.
func (a *App) RecordAt(layout *render.Layout, loc render.Location) {
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return
	}
	p, ok := store.PositionAt(layout, loc)
	if !ok {
		return
	}
	a.record(p)
}

// record stores a position and asks for a debounced write.
func (a *App) record(p store.Position) {
	a.Store.SetPosition(a.BookKey, p)
	a.Store.SaveSoon(2 * time.Second)
}

// open is the shelf's callback: remember the revision and hand the path to the
// host, which parses the whole book off the UI thread.
func (a *App) open(path string) {
	if a.Store != nil {
		a.Store.TouchBook(store.Book{
			Key:      store.Key(path, fileSize(path), fileModTime(path)),
			Path:     path,
			Title:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Format:   formatOf(path),
			Size:     fileSize(path),
			Modified: fileModTime(path),
		})
		a.Store.SaveSoon(0)
	}
	if a.Open != nil {
		a.Open(path)
	}
}

func (a *App) jump(loc render.Location) {
	if a.Jump != nil {
		a.Jump(loc)
	}
}

// jumpEntry jumps to what a table of contents row names. The row already
// carries the location the layout resolved, so the same call serves a page and
// a scroll position; a row whose anchor the layout no longer knows falls back
// to the start of its chapter's content.
func (a *App) jumpEntry(e toc.Entry) {
	if a.Jump == nil {
		return
	}
	if e.HasLoc {
		a.Jump(e.Loc)
		return
	}
	if a.layout == nil {
		return
	}
	if loc, ok := a.layout.Locate(e.Anchor); ok {
		a.Jump(loc)
		return
	}
	if top, ok := a.layout.ChapterContentTop(e.Chapter); ok {
		if loc, ok := a.layout.LocationAt(top); ok {
			a.Jump(loc)
		}
	}
}

// hitsChanged tells the host that Search.Hits changed.
func (a *App) hitsChanged() {
	if a.OnHits != nil {
		a.OnHits()
	}
}

// search runs the query against the current index. Obsolete runs are dropped by
// the id counter, so a slow search of an old query cannot overwrite a new one.
func (a *App) search(query string) {
	a.searchID++
	id := a.searchID
	if strings.TrimSpace(query) == "" {
		a.Search.Hits = nil
		a.Search.Selected = -1
		a.Search.Status = ""
		a.hitsChanged()
		return
	}
	ix := a.Index
	if ix == nil {
		a.Search.Hits = nil
		a.Search.Status = locales.SearchNoIndex()
		a.hitsChanged()
		return
	}
	a.Search.Status = locales.SearchSearching()
	a.async(func() func() {
		hits := ix.Search(query, 100)
		if hits == nil {
			hits = []search.Hit{}
		}
		return func() {
			if id != a.searchID {
				return
			}
			a.Search.Hits = hits
			a.Search.Selected = -1
			if len(hits) == 0 {
				a.Search.Status = locales.SearchNotFound(strings.TrimSpace(query))
			} else {
				a.Search.Status = locales.SearchResults(len(hits))
			}
			a.hitsChanged()
		}
	})
}

// async runs work off the UI thread and applies its result back on it. work
// returns the update, which runs on the UI thread when a Post hook exists, so
// no panel field is ever written from a goroutine. Without Post — the tests —
// everything runs inline.
func (a *App) async(work func() func()) {
	if a.Post == nil {
		if apply := work(); apply != nil {
			apply()
		}
		return
	}
	go func() {
		apply := work()
		if apply == nil {
			return
		}
		a.Post(apply)
	}()
}

// normalizeSidebar drops an unknown stored value, so a hand-edited state file
// cannot leave the window with an unnamed panel. "shelf" was the library panel
// of an older version, which the reader no longer floats.
func normalizeSidebar(side string) string {
	switch side {
	case SidebarToc, SidebarBookmarks, SidebarNotes, SidebarSearch:
		return side
	}
	return SidebarNone
}

func formatOf(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".epub") {
		return "epub"
	}
	return "txt"
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

func fileModTime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}
