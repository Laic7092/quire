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
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/bookmarks"
	"github.com/laix/quire/internal/library"
	"github.com/laix/quire/internal/notes"
	"github.com/laix/quire/internal/search"
	"github.com/laix/quire/internal/store"
	"github.com/laix/quire/internal/toc"
	"github.com/laix/quire/locales"
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
	Notes     notes.Panel
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
	// OnNotes runs on the UI thread whenever the set of notes changes, so the
	// host can rebuild the rectangles it paints their passages with.
	OnNotes func()
	// OnPickDir runs when the shelf asks for a folder of books to import.
	OnPickDir func()
	// Cover returns a book's decoded cover for the shelf, or nil. The host
	// caches the bitmaps; the panel never decodes one per frame.
	Cover func(entry library.Entry) *ui.Bitmap

	layout   *render.Layout
	page     int
	searchID int
	// shelfID counts shelf publications, so a reload that lost the race with a
	// newer reload or an import does not install its older list.
	shelfID int
	// book is the book the panels were last refreshed for, nil when none is
	// open; the bookmark rows take their chapter names from it.
	book *book.Book
	// placed is the store key of the book the layout and the rows were resolved
	// against. It differs from BookKey between opening a book and refreshing for
	// its first layout, which is exactly when an annotation must not be made:
	// the layout still in hand describes the book before it.
	placed string
	// loc is the place the panels were last refreshed for, and hasLoc whether
	// that refresh had a layout to read one from. The bookmarks panel marks the
	// row standing there.
	loc    render.Location
	hasLoc bool
}

// New builds an App over st. st may be nil, in which case nothing is persisted
// and the shelf stays empty until books are opened.
func New(st *store.Store) *App {
	a := &App{Store: st, Library: &library.Library{}}
	a.Shelf.Selected = -1
	a.Toc.Selected = -1
	a.Shelf.Lib = a.Library

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
	a.Notes.Selected = -1
	a.Notes.OnJump = func(e notes.Entry) {
		if e.HasLoc {
			a.jump(e.From)
		}
	}
	a.Notes.OnSave = func(e notes.Entry, comment string) { a.SaveNote(e.ID, comment) }
	a.Notes.OnRemove = func(e notes.Entry) { a.RemoveNote(e.ID) }
	a.Search.OnSearch = a.search
	a.Search.PageOf = func(h search.Hit) (int, bool) {
		if a.layout == nil || a.Index == nil {
			return 0, false
		}
		return search.Page(a.layout, a.Index, h)
	}
	return a
}

// ImportDir adds the EPUB and TXT books directly inside dir to the shelf,
// off the UI thread. Importing adds: the shelf is the store's remembered
// books, so a folder puts its books there and never forgets the others.
func (a *App) ImportDir(dir string) {
	a.initShelf()
	a.Shelf.Status = locales.ShelfScanning()
	a.async(func() func() {
		entries, err := library.Scan(dir)
		if err != nil {
			return func() { a.Shelf.Status = locales.ShelfScanFailed(err.Error()) }
		}
		return func() {
			if a.Store == nil {
				// Without a store the shelf cannot remember anything, so it shows
				// what this import found and nothing more.
				a.mergeShelf(entries)
				return
			}
			for _, e := range entries {
				a.remember(e.Path, e.Title)
			}
			// Listing the shelf from the store rather than from the scan keeps
			// one source of truth: the import added to it, and this shows all of it.
			a.ReloadShelf()
		}
	})
}

// ReloadShelf republishes the shelf from the books the store remembers,
// describing each file off the UI thread. It is what makes an opened book stay
// on the shelf across a restart: the store is the list, and this reads it.
func (a *App) ReloadShelf() {
	a.initShelf()
	if a.Store == nil {
		a.Shelf.Status = ""
		return
	}
	books := a.Store.Recent(0)
	if len(books) == 0 {
		a.Library.Entries = nil
		a.Shelf.Status = ""
		return
	}
	a.Shelf.Status = locales.ShelfScanning()
	a.shelfID++
	id := a.shelfID
	// The paths this reload knows about; a row for any other book was added
	// while it was reading and must survive it.
	listed := make(map[string]struct{}, len(books))
	for _, b := range books {
		listed[pathKey(b.Path)] = struct{}{}
	}
	a.async(func() func() {
		entries := make([]library.Entry, 0, len(books))
		for _, b := range books {
			if e, ok := library.Meta(b.Path); ok {
				entries = append(entries, e)
				continue
			}
			// The file is gone — moved, deleted, or on a disk that is not
			// mounted — or it is no longer a book this reader can describe: a
			// truncated download, a directory where a book was. Either way it
			// keeps the row the store remembers under the name it had, so the
			// reader sees what is broken instead of a quietly shorter shelf, and
			// opening it reports the error.
			entries = append(entries, library.Entry{
				Path:     b.Path,
				Title:    b.Title,
				Format:   b.Format,
				Size:     b.Size,
				Modified: b.Modified,
			})
		}
		return func() {
			if id != a.shelfID {
				return // a newer import or reload owns the shelf
			}
			// A book opened while this reload was reading is newer than its
			// snapshot: keep its row instead of hiding it until the next reload.
			for _, row := range a.Library.Entries {
				if _, ok := listed[pathKey(row.Path)]; ok {
					continue
				}
				entries = append(entries, row)
			}
			a.Library.Entries = entries
			a.Shelf.Status = shelfCount(len(entries))
		}
	})
}

// upsertShelf puts the book at path on the shelf, describing it off the UI
// thread. A book opened from the file dialog shows up at once instead of after
// the next full reload.
func (a *App) upsertShelf(path string) {
	a.async(func() func() {
		entry, ok := library.Meta(path)
		if !ok {
			return nil
		}
		return func() { a.mergeShelf([]library.Entry{entry}) }
	})
}

// RemoveBook takes the book at path off the shelf, and with it what the reader
// recorded about that revision of the file: the reading position, the bookmarks
// and the notes. The file itself stays where it is — the shelf lists books, it
// does not own them — so tidying it up never costs the reader a library.
//
// It reports whether the shelf changed.
func (a *App) RemoveBook(path string) bool {
	if path == "" {
		return false
	}
	if a.Store != nil {
		a.Store.RemoveBook(path)
		a.Store.SaveSoon(0)
	}
	a.initShelf()
	rows := a.Library.Entries
	kept := rows[:0]
	for _, row := range rows {
		if !samePath(row.Path, path) {
			kept = append(kept, row)
		}
	}
	if len(kept) == len(rows) {
		return false
	}
	a.Library.Entries = kept
	// A listing that read the shelf before this removal would put the book back
	// on the next publication: this removal is newer than that snapshot.
	a.shelfID++
	if a.Shelf.Selected >= len(kept) {
		a.Shelf.Selected = len(kept) - 1
	}
	a.Shelf.Status = shelfCount(len(kept))
	return true
}

// mergeShelf adds entries to the shelf, replacing a row that names the same
// file. It is the UI-thread half of upsertShelf; the row is keyed by path
// because that is what the reader recognises as one book.
func (a *App) mergeShelf(entries []library.Entry) {
	if len(entries) == 0 {
		return
	}
	a.initShelf()
	rows := a.Library.Entries
	for _, entry := range entries {
		replaced := false
		for i := range rows {
			if !samePath(rows[i].Path, entry.Path) {
				continue
			}
			rows[i], replaced = entry, true
			break
		}
		if !replaced {
			rows = append(rows, entry)
		}
	}
	a.Library.Entries = rows
	a.Shelf.Status = shelfCount(len(rows))
}

// initShelf makes sure the shelf has a Library to list into. New always
// makes one; this is for an App built by hand.
func (a *App) initShelf() {
	if a.Library == nil {
		a.Library = &library.Library{}
	}
	if a.Shelf.Lib != a.Library {
		a.Shelf.Lib = a.Library
	}
}

// shelfCount is the panel's status line: the number of books, nothing while the
// shelf is empty and the panel already says so.
func shelfCount(n int) string {
	if n == 0 {
		return ""
	}
	return locales.ShelfCount(n)
}

// Refresh republishes the panels for a new layout, book or page. Hosts call it
// after a build, after a relayout and after every page turn. It is the paged
// form; in a scroll layout use RefreshAt, which names the place by location.
//
// Refreshing with no book and no layout closes the book: the rows, the index
// and the query belonged to it and are dropped.
func (a *App) Refresh(bk *book.Book, layout *render.Layout, page int) {
	loc, placed := render.Location{}, false
	if layout != nil {
		loc, placed = layout.LocationOfPage(page)
	}
	a.refreshAt(bk, layout, loc, placed, page)
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
	a.refreshAt(bk, layout, loc, layout != nil, page)
}

// refreshAt rebuilds the panels from the layout and the place read to. hasLoc
// reports whether loc is a place of this layout at all: a blank page or a
// layout that names no place leaves no row standing at the reading position,
// rather than matching whatever a zero location resolves to.
func (a *App) refreshAt(bk *book.Book, layout *render.Layout, loc render.Location, hasLoc bool, page int) {
	if bk == nil && layout == nil {
		a.clearBook()
	}
	a.layout, a.page = layout, page
	a.book = bk
	a.placed = a.BookKey
	a.loc, a.hasLoc = loc, hasLoc && layout != nil
	a.Toc.Entries = toc.BuildAt(bk, layout, loc)
	if len(a.Toc.Entries) > 0 {
		a.Toc.SelectCurrent()
	}
	a.rebuildBookmarks(bk)
	a.rebuildNotes(bk)
}

// clearBook drops everything the panels knew about a book that is no longer
// open: its rows, the layout and place they were resolved against, the index
// its hits were found in and the query that made them. Opening a book and
// closing one both start from here, so neither a stale row nor a hit of the
// book before can be shown, jumped to or saved into this one.
func (a *App) clearBook() {
	a.BookKey, a.placed = "", ""
	a.book, a.layout = nil, nil
	a.loc, a.hasLoc, a.page = render.Location{}, false, 0
	a.Index = nil
	a.Toc.Entries, a.Toc.Selected = nil, -1
	a.Bookmarks.Entries, a.Bookmarks.Selected = a.Bookmarks.Entries[:0], -1
	a.Notes.Entries, a.Notes.Selected = a.Notes.Entries[:0], -1
	a.Notes.Draft = ""
	a.Search.Query, a.Search.Hits = "", nil
	a.Search.Selected, a.Search.Status = -1, ""
	// A search still running belongs to the book that is going away.
	a.searchID++
	a.hitsChanged()
}

// SetIndex publishes a freshly built index. The host must call it with the
// document the next Refresh's layout was built from, or hits resolve to the
// wrong page: a new document means a new index.
//
// A relayout builds a new index of the same book, so the query the reader typed
// still belongs to it: it runs again against the new index instead of leaving
// the panel empty under a word it never answered.
func (a *App) SetIndex(ix *search.Index) {
	a.Index = ix
	a.Search.Hits = nil
	a.Search.Selected = -1
	a.Search.Status = ""
	a.searchID++
	a.hitsChanged()
	if strings.TrimSpace(a.Search.Query) != "" {
		a.search(a.Search.Query)
	}
}

// Opened records bk as the open book, remembers it in the store and returns its
// key. path is the file the host opened, for size and modification time.
func (a *App) Opened(bk *book.Book, path string) string {
	if bk == nil {
		return ""
	}
	key := a.remember(path, bk.Title)
	a.upsertShelf(path)
	// The rows, the index and the query on screen belong to the book that was
	// open, not to this one: the host refreshes the panels after the first
	// layout, and until then nothing may be bookmarked or annotated.
	a.clearBook()
	a.BookKey = key
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
	a.recordAt(layout, loc, false)
}

// RecordAtEnd is RecordAt for a scrolling host that has reached the end of the
// flow. The top of the viewport is then one screen short of the end, so the
// position is marked finished explicitly; without this the shelf could never
// show the book as read.
func (a *App) RecordAtEnd(layout *render.Layout, loc render.Location) {
	a.recordAt(layout, loc, true)
}

func (a *App) recordAt(layout *render.Layout, loc render.Location, finished bool) {
	if a.Store == nil || a.BookKey == "" || layout == nil {
		return
	}
	p, ok := store.PositionAt(layout, loc)
	if !ok {
		return
	}
	p.Finished = finished
	a.record(p)
}

// record stores a position and asks for a debounced write.
func (a *App) record(p store.Position) {
	a.Store.SetPosition(a.BookKey, p)
	a.Store.SaveSoon(2 * time.Second)
}

// remember records path as a shelf book, asks for a debounced write and
// returns the store key of this revision of the file. A book is on the shelf
// because it was opened or imported, which is the only way books get there;
// the key follows size and modification time, so a re-download is noticed.
func (a *App) remember(path, title string) string {
	size, modified := fileSize(path), fileModTime(path)
	key := store.Key(path, size, modified)
	if a.Store == nil {
		return key
	}
	a.Store.TouchBook(store.Book{
		Key:      key,
		Path:     path,
		Title:    title,
		Format:   formatOf(path),
		Size:     size,
		Modified: modified,
	})
	a.Store.SaveSoon(0)
	return key
}

// open is the shelf's callback: remember the revision and hand the path to the
// host, which parses the whole book off the UI thread.
func (a *App) open(path string) {
	a.remember(path, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	a.upsertShelf(path)
	if a.Open != nil {
		a.Open(path)
	}
}

// samePath reports whether two paths name the same file, so a book reached
// through a relative and an absolute path stays one shelf row.
func samePath(a, b string) bool {
	return pathKey(a) == pathKey(b)
}

// pathKey is what a shelf row is identified by: the path, made absolute and
// clean so one file reached two ways is one row.
func pathKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
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
