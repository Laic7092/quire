package appstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The shelf lists the sample books and reports a click to the host, all through
// the App, without a window. The library page shows it; the reader floats its
// own panels, so the shelf is drawn on its own here.
func TestShelfPanelOpensABook(t *testing.T) {
	st := newStore(t)
	a := New(st)
	a.ImportDir(filepath.Join("..", "book", "testdata", "samples"))
	if len(a.Library.Entries) != 3 {
		t.Fatalf("shelf lists %d books, want the 3 samples", len(a.Library.Entries))
	}

	var opened string
	a.Open = func(path string) { opened = path }

	tt := ui.NewTester(func(c *ui.Context) { a.Shelf.View(c) }, 360, 600)
	if !tt.HasText(locales.ShelfSearchLabel()) || !tt.HasText("阿Ｑ正傳") {
		t.Fatalf("shelf did not build: %q", tt.Texts())
	}
	if err := tt.Click("阿Ｑ正傳"); err != nil {
		t.Fatalf("click: %v (texts %q)", err, tt.Texts())
	}
	if want := filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub"); opened != want {
		t.Errorf("opened %q, want %q", opened, want)
	}
	// The click also put the book in the store, before the host opened it.
	if recent := st.Recent(1); len(recent) != 1 || recent[0].Path != opened {
		t.Errorf("recent = %+v", recent)
	}
}

// The shelf is the store's list of books, not a folder: a book opened once is
// still there after a restart, with no folder to scan and nothing to import
// again.
func TestShelfRemembersAnOpenedBookAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sample := filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub")
	a := New(st)
	// Opening from the file dialog is the panel's own path: it remembers the
	// book and puts it on the shelf, and the host then parses it.
	a.open(sample)
	if len(a.Library.Entries) != 1 {
		t.Fatalf("opening put %d books on the shelf, want 1", len(a.Library.Entries))
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	restarted := New(again)
	restarted.ReloadShelf()
	if len(restarted.Library.Entries) != 1 {
		t.Fatalf("after the restart the shelf lists %d books, want 1", len(restarted.Library.Entries))
	}
	if got := restarted.Library.Entries[0]; !samePath(got.Path, sample) || got.Title != "阿Ｑ正傳" {
		t.Errorf("shelf row %+v, want the sample under its own title", got)
	}
}

// A book whose file went away keeps its row under the name the store remembers,
// so a moved file or an unmounted disk does not empty the shelf silently.
func TestShelfListsABookWhoseFileIsGone(t *testing.T) {
	st := newStore(t)
	// A copy in a temporary folder, so removing the file does not touch the
	// samples the other packages read while this test runs.
	sample := filepath.Join(t.TempDir(), "aq-zhuan.epub")
	data, err := os.ReadFile(filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sample, data, 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(st)
	a.open(sample)
	if err := os.Remove(sample); err != nil {
		t.Fatal(err)
	}

	a.ReloadShelf()
	if len(a.Library.Entries) != 1 {
		t.Fatalf("the shelf lists %d rows, want the missing book's row", len(a.Library.Entries))
	}
	if got := a.Library.Entries[0]; !samePath(got.Path, sample) || got.Title != "aq-zhuan" {
		t.Errorf("row %+v, want the remembered book under its store title", got)
	}
}

// The open panel survives a restart, and an unknown value falls back to none.
func TestSidebarIsRemembered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st)
	a.Show(SidebarToc)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := New(again).Sidebar; got != SidebarToc {
		t.Errorf("reopened sidebar %q, want %q", got, SidebarToc)
	}

	again.Update(func(s *store.State) { s.Prefs.Sidebar = "nonsense" })
	if got := New(again).Sidebar; got != SidebarNone {
		t.Errorf("unknown sidebar %q, want the empty panel", got)
	}
}

// Show picks a panel; an empty name closes the sidebar and remembers what was
// open, so a restart looks the same.
func TestShowAndClose(t *testing.T) {
	st := newStore(t)
	a := New(st)
	a.Show(SidebarSearch)
	if !a.Showing() || a.Title() != locales.Search() {
		t.Fatalf("shows %q", a.Title())
	}
	a.Show(SidebarNone)
	if a.Showing() {
		t.Error("still showing after closing")
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	saved := st.State().Prefs
	if saved.SidebarOpen || saved.Sidebar != SidebarSearch {
		t.Errorf("prefs %+v after closing the search panel", saved)
	}
	// An unknown stored panel falls back to the table of contents rather than
	// leaving the window with an unnamed one.
	st.Update(func(s *store.State) { s.Prefs.Sidebar = "nonsense" })
	if got := New(st).Title(); got != "" {
		t.Errorf("unknown panel shows %q", got)
	}
}

// A saved position comes back after a relayout: the store's PageFor finds the
// node, and the reader resumes on that page. The chapter fallback still works
// without fonts.
func TestResumeRoundTrip(t *testing.T) {
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := book.Open(filepath.Join("..", "book", "testdata", "mini.epub"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 600, 400, reader.Options{FontSize: reader.DefaultFontSize})
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageCount() < 2 {
		t.Skip("the sample book fits on one page")
	}
	want := layout.PageCount() - 1

	st := newStore(t)
	a := New(st)
	key := a.Opened(bk, filepath.Join("..", "book", "testdata", "mini.epub"))
	if key == "" {
		t.Fatal("no key for the opened book")
	}
	a.Refresh(bk, layout, want)
	a.RecordPage(layout)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	b := New(st)
	b.BookKey = key
	page, how := b.Resume(layout)
	if page != want || how != "node" {
		t.Errorf("resume = page %d via %q, want page %d via node", page, how, want)
	}

	// Without a layout the reader stays at the top instead of failing.
	if page, how := b.Resume(nil); page != 0 || how != "" {
		t.Errorf("resume without a layout = %d, %q", page, how)
	}
}

// The table of contents and the search panel are wired to the same layout, so a
// jump from either lands on a real page.
func TestTocAndSearchJump(t *testing.T) {
	bk, err := book.Open(filepath.Join("..", "book", "testdata", "mini.epub"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(nil)
	var jumped bool
	a.Jump = func(loc render.Location) { jumped = loc != (render.Location{}) }
	a.Refresh(bk, nil, 0)
	if len(a.Toc.Entries) != len(bk.Chapters) {
		t.Fatalf("%d entries for %d chapters", len(a.Toc.Entries), len(bk.Chapters))
	}
	if a.Toc.Entries[0].Page != -1 {
		t.Errorf("entry without a layout has page %d", a.Toc.Entries[0].Page)
	}

	// With no index, searching reports that and never panics.
	a.Search.OnSearch("anything")
	if a.Search.Status == "" || len(a.Search.Hits) != 0 {
		t.Errorf("search without an index: %q, %d hits", a.Search.Status, len(a.Search.Hits))
	}

	a.Show(SidebarToc)
	tt := ui.NewTester(func(c *ui.Context) { a.View(c) }, 320, 500)
	if !tt.HasText(locales.TocCount(len(a.Toc.Entries))) || !tt.HasText(bk.Chapters[0].Title) {
		t.Fatalf("toc did not build: %q", tt.Texts())
	}
	if jumped {
		t.Error("jumped without a click")
	}
}

// Background work never touches a panel field: the result arrives as a function
// that the host runs on the UI thread through Window.Update.
func TestBackgroundWorkAppliesOnTheUIThread(t *testing.T) {
	posts := make(chan func(), 4)
	a := New(newStore(t))
	a.Post = func(fn func()) { posts <- fn }

	a.ImportDir(filepath.Join("..", "book", "testdata", "samples"))
	if len(a.Shelf.Lib.Entries) != 0 {
		t.Fatalf("the scan wrote the entries before the UI thread ran it")
	}

	var apply func()
	// The import remembers the books and then lists the shelf from the store,
	// which is a second publication: apply them until the shelf is filled.
	for len(a.Shelf.Lib.Entries) == 0 {
		select {
		case apply = <-posts:
			apply()
		case <-time.After(5 * time.Second):
			t.Fatal("the import published nothing")
		}
	}
	if a.Shelf.Status != locales.ShelfCount(3) {
		t.Errorf("status %q after the scan", a.Shelf.Status)
	}
}

// Drawing a panel never chooses another one: the toolbar's buttons do that, so
// a frame cannot overwrite what the reader chose.
func TestPanelViewDrawsTheChosenPanel(t *testing.T) {
	st := newStore(t)
	a := New(st)
	a.Show(SidebarToc)
	tt := ui.NewTester(func(c *ui.Context) { a.View(c) }, 320, 500)
	tt.Frame()
	tt.Frame()
	if a.Sidebar != SidebarToc {
		t.Fatalf("panel %q after two frames without a click", a.Sidebar)
	}
	if !tt.HasText(locales.TocEmpty()) {
		t.Errorf("the contents did not build: %q", tt.Texts())
	}
	a.Show(SidebarSearch)
	tt.Frame()
	if !tt.HasText(locales.SearchFieldLabel()) || a.Sidebar != SidebarSearch {
		t.Errorf("panel %q after showing search: %q", a.Sidebar, tt.Texts())
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if prefs := st.State().Prefs; !prefs.SidebarOpen || prefs.Sidebar != SidebarSearch {
		t.Errorf("prefs %+v after showing the search panel", prefs)
	}
}

// Bookmarks and notes are panels of their own until their data layer lands:
// they say what will fill them rather than showing an empty frame.
func TestBookmarksAndNotesPanels(t *testing.T) {
	a := New(nil)
	for _, tc := range []struct{ side, title, empty string }{
		{SidebarBookmarks, locales.PanelBookmarks(), locales.EmptyBookmarks()},
		{SidebarNotes, locales.PanelNotes(), locales.EmptyNotes()},
	} {
		a.Show(tc.side)
		if got := a.Title(); got != tc.title {
			t.Errorf("title of %q = %q, want %q", tc.side, got, tc.title)
		}
		tt := ui.NewTester(func(c *ui.Context) { a.View(c) }, 300, 500)
		if !tt.HasText(tc.empty) {
			t.Errorf("%s panel: %q", tc.side, tt.Texts())
		}
	}
}

// A scroll layout has no pages, so the reading place is a location: RefreshAt
// marks the chapter it falls in, and the store brings the same place back
// through RecordAt and ResumeAt.
func TestResumeAtScroll(t *testing.T) {
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub")
	bk, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 600,
		reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageCount() != 0 || layout.ContentHeight() <= 600 {
		t.Fatalf("not a scroll layout: %d pages, %.0f tall", layout.PageCount(), layout.ContentHeight())
	}

	st := newStore(t)
	a := New(st)
	key := a.Opened(bk, path)
	loc, ok := layout.LocationAt(layout.ContentHeight() / 2)
	if !ok {
		t.Fatal("no location halfway down the book")
	}
	a.RefreshAt(bk, layout, loc)
	if a.page != -1 {
		t.Errorf("a scroll refresh set page %d", a.page)
	}
	current := -1
	for i, e := range a.Toc.Entries {
		if e.Current {
			current = i
		}
		if !e.HasLoc {
			t.Errorf("chapter %d has no location: %+v", i, e)
		}
		if e.Page != -1 {
			t.Errorf("chapter %d has page %d in a scroll layout", i, e.Page)
		}
	}
	if current != loc.Chapter {
		t.Errorf("marked chapter %d as current, reading chapter %d", current, loc.Chapter)
	}

	a.RecordAt(layout, loc)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	b := New(st)
	b.BookKey = key
	got, how := b.ResumeAt(layout)
	if how == "" {
		t.Fatal("nothing was resumed")
	}
	want, _ := layout.OffsetOf(loc)
	back, ok := layout.OffsetOf(got)
	if !ok || back != want {
		t.Errorf("resumed at %.0f, want %.0f (via %s)", back, want, how)
	}
	if page, how := b.Resume(layout); page != 0 || how != "" {
		t.Errorf("paged resume on a scroll layout = %d, %q", page, how)
	}
}
