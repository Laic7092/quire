package appstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/search"
	"github.com/laix/quire/internal/store"
	"github.com/laix/quire/locales"
	"github.com/laic7092/layx/render"
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

// Taking a book off the shelf drops the row and the reading state kept for that
// revision of the file, and leaves the file itself alone: the shelf is a list,
// not the library.
func TestRemoveBookDropsTheRowAndItsReadingState(t *testing.T) {
	a := New(newStore(t))
	a.ImportDir(filepath.Join("..", "book", "testdata", "samples"))
	if len(a.Library.Entries) != 3 {
		t.Fatalf("shelf lists %d books, want the 3 samples", len(a.Library.Entries))
	}
	gone := a.Library.Entries[0]
	kept := a.Library.Entries[1]
	goneKey := store.Key(gone.Path, gone.Size, gone.Modified)
	a.Store.SetPosition(goneKey, store.Position{Chapter: 2, Progress: 0.3})
	a.Store.AddBookmark(goneKey, store.Bookmark{Label: "here"})
	a.Store.AddNote(goneKey, store.Note{Text: "passage", Comment: "note"})
	a.Shelf.Selected = 2

	if !a.RemoveBook(gone.Path) {
		t.Fatal("RemoveBook reported nothing removed")
	}
	if got := len(a.Library.Entries); got != 2 {
		t.Fatalf("shelf lists %d books, want 2", got)
	}
	for _, row := range a.Library.Entries {
		if samePath(row.Path, gone.Path) {
			t.Error("the removed book is still on the shelf")
		}
	}
	if _, ok := a.Store.Position(goneKey); ok {
		t.Error("the removed book kept its position")
	}
	if rows := a.Store.BookmarksOf(goneKey); len(rows) != 0 {
		t.Errorf("bookmarks = %+v, want none", rows)
	}
	if rows := a.Store.NotesOf(goneKey); len(rows) != 0 {
		t.Errorf("notes = %+v, want none", rows)
	}
	if got := a.Shelf.Selected; got != 1 {
		t.Errorf("the chosen row is %d, want it on the last book left", got)
	}
	// The other books, and their files, are untouched.
	if _, err := os.Stat(kept.Path); err != nil {
		t.Errorf("the book still on the shelf: %v", err)
	}
	if _, err := os.Stat(gone.Path); err != nil {
		t.Errorf("the removed book's file is gone from disk: %v", err)
	}
	// Removing the same path again changes nothing.
	if a.RemoveBook(gone.Path) {
		t.Error("RemoveBook removed a book that was already gone")
	}

	// The shelf is the store's list: a restart agrees with what is left.
	a.ReloadShelf()
	if got := len(a.Library.Entries); got != 2 {
		t.Errorf("after a reload the shelf lists %d books, want 2", got)
	}
}

// A book whose file went away keeps its row under the name the store remembers,
// so it can still be taken off the shelf: the removal finds the row by path, not
// by a key of a file that is no longer there to hash.
func TestRemoveBookWhoseFileIsGone(t *testing.T) {
	st := newStore(t)
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
		t.Fatalf("the shelf lists %d rows, want the missing book's", len(a.Library.Entries))
	}

	if !a.RemoveBook(sample) {
		t.Fatal("RemoveBook left the row of a book whose file is gone")
	}
	if got := len(a.Library.Entries); got != 0 {
		t.Errorf("the shelf lists %d rows after the removal, want none", got)
	}
	if got := st.Recent(0); len(got) != 0 {
		t.Errorf("the store still remembers %+v", got)
	}
	// The store is the shelf: the next listing does not bring the book back.
	a.ReloadShelf()
	if got := len(a.Library.Entries); got != 0 {
		t.Errorf("a reload listed %d rows, want none", got)
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

// A relayout builds a new index of the same book, and the query the reader
// typed still belongs to it: the results come back instead of the panel going
// empty under a word it never answered.
func TestSetIndexSearchesAgain(t *testing.T) {
	bk, err := book.Open(filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := search.NewIndex(bk, doc)
	if err != nil {
		t.Fatal(err)
	}
	a := New(newStore(t))
	a.SetIndex(ix)
	a.Search.Query = "阿"
	a.Search.OnSearch("阿")
	if len(a.Search.Hits) == 0 {
		t.Fatalf("searching the sample found nothing: %q", a.Search.Status)
	}
	want := a.Search.Status

	a.SetIndex(ix) // the relayout's fresh index of the same document
	if len(a.Search.Hits) == 0 {
		t.Errorf("the results vanished when the index was replaced: %q", a.Search.Status)
	}
	if got := a.Search.Status; got != want {
		t.Errorf("status = %q after the relayout, want %q", got, want)
	}

	// An empty field has nothing to run again, and a book with no index says so.
	a.Search.Query = "  "
	a.SetIndex(ix)
	if len(a.Search.Hits) != 0 || a.Search.Status != "" {
		t.Errorf("an empty query searched: %d hits, %q", len(a.Search.Hits), a.Search.Status)
	}
	a.Search.Query = "阿"
	a.SetIndex(nil)
	if a.Search.Status != locales.SearchNoIndex() {
		t.Errorf("status = %q without an index, want %q", a.Search.Status, locales.SearchNoIndex())
	}
}

// A page-shaped refresh on a scrolling column names no place at all: nothing is
// marked as the row standing at the reading position, rather than whatever a
// zero location resolves to.
func TestRefreshOnAScrollLayoutMarksNothing(t *testing.T) {
	a, bk, _, _ := bookmarkFixture(t)
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	scroll, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 600,
		reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	a.RefreshAt(bk, scroll, mustLoc(t, scroll, 0))
	if !a.BookmarkHere() {
		t.Fatal("the top of the column was not bookmarked")
	}
	if !a.Bookmarks.Entries[0].Current {
		t.Errorf("the row at the reading place is not current: %+v", a.Bookmarks.Entries[0])
	}

	a.Refresh(bk, scroll, 0)
	if a.hasLoc {
		t.Errorf("a page-shaped refresh on a scroll layout claims a place: %+v", a.loc)
	}
	for _, e := range a.Bookmarks.Entries {
		if e.Current {
			t.Errorf("row %q is marked current after a refresh that names no place", e.Title)
		}
	}
}

// mustLoc is the location at the top offset y of a layout, for the fixtures.
func mustLoc(t *testing.T, layout *render.Layout, y float32) render.Location {
	t.Helper()
	loc, ok := layout.LocationAt(y)
	if !ok {
		t.Fatalf("no location at %.0f", y)
	}
	return loc
}

// Closing the book drops what belonged to it: the rows, the query, the hits and
// the index, so the next book starts from an empty panel and never shows, jumps
// to or saves into the one before it.
func TestClosingTheBookClearsThePanels(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	a.Refresh(bk, layout, 0)
	from, to := passageOn(t, layout, 0)
	if !a.BookmarkHere() {
		t.Fatal("the page was not bookmarked")
	}
	if _, ok := a.AddNote(from, to, "一段文字"); !ok {
		t.Fatal("AddNote stored nothing")
	}
	a.Search.Query = "阿"
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := search.NewIndex(bk, doc)
	if err != nil {
		t.Fatal(err)
	}
	a.SetIndex(ix)
	a.Search.OnSearch("阿")
	if len(a.Search.Hits) == 0 || len(a.Bookmarks.Entries) == 0 || len(a.Notes.Entries) == 0 {
		t.Fatal("the fixture holds nothing to clear")
	}

	a.Refresh(nil, nil, 0) // the host's closeBook

	if a.BookKey != "" {
		t.Errorf("BookKey = %q after closing the book", a.BookKey)
	}
	if a.Index != nil {
		t.Error("the index of the closed book is still published")
	}
	if len(a.Bookmarks.Entries) != 0 || len(a.Notes.Entries) != 0 || len(a.Toc.Entries) != 0 {
		t.Errorf("panels still list rows: %d bookmarks, %d notes, %d contents",
			len(a.Bookmarks.Entries), len(a.Notes.Entries), len(a.Toc.Entries))
	}
	if a.Notes.SelectedID() != "" || a.Notes.Draft != "" {
		t.Errorf("a note is still chosen: %q, draft %q", a.Notes.SelectedID(), a.Notes.Draft)
	}
	if a.Search.Query != "" || len(a.Search.Hits) != 0 || a.Search.Status != "" {
		t.Errorf("the search survived the book: query %q, %d hits, %q",
			a.Search.Query, len(a.Search.Hits), a.Search.Status)
	}
	if a.BookmarkHere() {
		t.Error("BookmarkHere bookmarked with no book open")
	}
	if _, ok := a.AddNote(from, to, "文字"); ok {
		t.Error("AddNote stored a note with no book open")
	}
}

// A book whose file is there but is no longer a book this reader can describe —
// a truncated download, a directory where a book was — keeps its row too, so the
// shelf never loses a book quietly.
func TestShelfListsABookItCannotDescribe(t *testing.T) {
	st := newStore(t)
	path := filepath.Join(t.TempDir(), "broken.epub")
	if err := os.WriteFile(path, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(st)
	// Opening it from the shelf remembers it before the host reads the file,
	// so the store has the book even though the shelf cannot describe it yet.
	a.open(path)
	a.ReloadShelf()
	if len(a.Library.Entries) != 1 {
		t.Fatalf("the shelf lists %d rows, want the book it cannot describe", len(a.Library.Entries))
	}
	if got := a.Library.Entries[0]; !samePath(got.Path, path) || got.Title != "broken" {
		t.Errorf("row %+v, want the remembered book under its store title", got)
	}
}
