// Package appstate wires the reader's side panels — shelf, table of contents
// and full-text search — to the persisted state, so main.go stays a thin shell:
// it owns the window, the render document and the painter, and calls Refresh
// whenever one of those changes.
//
// The package never touches a Window: background work publishes its result
// through Post, which the host sets to Window.Update. Everything else is plain
// fields, which keeps the whole panel layer testable with ui.NewTester.
package appstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/search"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/internal/toc"
	"github.com/laix/layx/render"
)

// Sidebar identifiers, also what Prefs.Sidebar stores. SidebarNone closes the
// panel.
const (
	SidebarNone   = ""
	SidebarShelf  = "shelf"
	SidebarToc    = "toc"
	SidebarSearch = "search"
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

	Shelf  library.Panel
	Toc    toc.Panel
	Search search.Panel

	// Post publishes fn on the UI thread (Window.Update). When nil, background
	// work runs inline, which is what the tests want.
	Post func(fn func())
	// Open opens a book path; the host parses it off the UI thread.
	Open func(path string)
	// Jump shows a page of the current book.
	Jump func(page int)
	// OnHits runs on the UI thread whenever Search.Hits changes, so the host can
	// rebuild what it derives from them, such as the highlight rectangles it
	// paints on the page.
	OnHits func()
	// OnPickDir runs when the shelf asks for a library directory.
	OnPickDir func()
	// Cover returns a book's decoded cover for the shelf, or nil. The host
	// caches the bitmaps; the panel never decodes one per frame.
	Cover func(entry library.Entry) *ui.Bitmap

	// SidebarWidth is the width the user dragged the sidebar to, in DIPs.
	// The host keeps it in a ui.Split and calls SaveSidebarWidth when it
	// changes.
	SidebarWidth float32

	layout   *render.Layout
	page     int
	searchID int
}

// New builds an App over st. st may be nil, in which case nothing is persisted
// and the shelf has no directory until SetLibraryDir is called.
func New(st *store.Store) *App {
	a := &App{Store: st}
	a.Shelf.Selected = -1
	a.Toc.Selected = -1

	if st != nil {
		prefs := st.State().Prefs
		a.SidebarWidth = prefs.SidebarWidth
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

	a.Toc.OnJump = a.jump
	a.Search.OnJump = a.jump
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
		a.Shelf.Status = "先选择一个书库目录"
		return
	}
	lib := a.Library
	a.Shelf.Status = "正在扫描…"
	// Scan off the UI thread, then install the result on it: the shelf reads
	// Entries while it draws, so the slice must not be swapped underneath.
	a.async(func() func() {
		entries, err := library.Scan(lib.Dir)
		return func() {
			if err != nil {
				a.Shelf.Status = "扫描失败：" + err.Error()
				return
			}
			lib.Entries = entries
			a.Shelf.Status = fmt.Sprintf("%d 本", len(entries))
		}
	})
}

// SaveSidebarWidth remembers the width the user dragged the sidebar to.
func (a *App) SaveSidebarWidth(w float32) {
	if w <= 0 || w == a.SidebarWidth {
		return
	}
	a.SidebarWidth = w
	if a.Store == nil {
		return
	}
	a.Store.Update(func(s *store.State) { s.Prefs.SidebarWidth = w })
	a.Store.SaveSoon(0)
}

// Refresh republishes the panels for a new layout, book or page. Hosts call it
// after a build, after a relayout and after every page turn.
func (a *App) Refresh(bk *book.Book, layout *render.Layout, page int) {
	a.layout, a.page = layout, page
	a.Toc.Layout = layout
	a.Toc.Entries = toc.Build(bk, layout, page)
	if len(a.Toc.Entries) > 0 {
		a.Toc.SelectCurrent()
	}
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
// "progress", "chapter"), and empty when nothing is saved.
func (a *App) Resume(layout *render.Layout) (page int, how string) {
	if a.Store == nil || a.BookKey == "" {
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

func (a *App) jump(page int) {
	if a.Jump != nil && page >= 0 {
		a.Jump(page)
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
		a.Search.Status = "索引还没建好"
		a.hitsChanged()
		return
	}
	a.Search.Status = "搜索中…"
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
				a.Search.Status = "未找到「" + query + "」"
			} else {
				a.Search.Status = fmt.Sprintf("%d 条结果", len(hits))
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
// cannot leave the window with an unnamed panel.
func normalizeSidebar(side string) string {
	switch side {
	case SidebarShelf, SidebarToc, SidebarSearch:
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
