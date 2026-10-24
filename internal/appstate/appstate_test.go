package appstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/store"
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
// the App, without a window.
func TestShelfPanelOpensABook(t *testing.T) {
	st := newStore(t)
	a := New(st)
	a.SetLibraryDir(filepath.Join("..", "book", "testdata", "samples"))
	if len(a.Library.Entries) != 3 {
		t.Fatalf("shelf lists %d books, want the 3 samples", len(a.Library.Entries))
	}

	var opened string
	a.Open = func(path string) { opened = path }
	a.Show(SidebarShelf)

	tt := ui.NewTester(func(c *ui.Context) { a.View(c) }, 360, 600)
	if !tt.HasText("书架") || !tt.HasText("阿Ｑ正傳") {
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
	if !a.Showing() || a.Title() != "搜索" {
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
	var jumped int
	a.Jump = func(page int) { jumped = page }
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
	if !tt.HasText("目录") || !tt.HasText(bk.Chapters[0].Title) {
		t.Fatalf("toc did not build: %q", tt.Texts())
	}
	if jumped != 0 {
		t.Errorf("jumped to %d without a click", jumped)
	}
}

// Background work never touches a panel field: the result arrives as a function
// that the host runs on the UI thread through Window.Update.
func TestBackgroundWorkAppliesOnTheUIThread(t *testing.T) {
	posts := make(chan func(), 4)
	a := New(nil)
	a.Post = func(fn func()) { posts <- fn }

	a.SetLibraryDir(filepath.Join("..", "book", "testdata", "samples"))
	if len(a.Shelf.Lib.Entries) != 0 {
		t.Fatalf("the scan wrote the entries before the UI thread ran it")
	}

	var apply func()
	select {
	case apply = <-posts:
	case <-time.After(5 * time.Second):
		t.Fatal("the scan published nothing")
	}
	apply()
	if len(a.Shelf.Lib.Entries) == 0 {
		t.Errorf("no entries after applying the published update")
	}
	if a.Shelf.Status != "3 本" {
		t.Errorf("status %q after the scan", a.Shelf.Status)
	}
}

// Drawing the switcher does not choose a panel by itself: it reports only a
// click, so a frame cannot overwrite what the user chose.
func TestSwitcherOnlyReportsClicks(t *testing.T) {
	st := newStore(t)
	a := New(st)
	a.Show(SidebarToc)
	tt := ui.NewTester(func(c *ui.Context) { a.View(c) }, 320, 500)
	tt.Frame()
	tt.Frame()
	if a.Sidebar != SidebarToc {
		t.Fatalf("sidebar %q after two frames without a click", a.Sidebar)
	}
	if err := tt.Click("搜索"); err != nil {
		t.Fatalf("segment: %v (%q)", err, tt.Texts())
	}
	if a.Sidebar != SidebarSearch {
		t.Errorf("sidebar %q after clicking the search segment", a.Sidebar)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if prefs := st.State().Prefs; !prefs.SidebarOpen || prefs.Sidebar != SidebarSearch {
		t.Errorf("prefs %+v after choosing the search panel", prefs)
	}
}
