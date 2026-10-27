package appstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// bookmarkFixture opens the sample book through the App and builds a paged
// layout, the state a window is in once a book is on screen.
func bookmarkFixture(t *testing.T) (*App, *book.Book, *render.Layout, string) {
	t.Helper()
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
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 600, 400,
		reader.Options{FontSize: reader.DefaultFontSize})
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageCount() < 3 {
		t.Skip("the sample book fits on too few pages")
	}
	a := New(newStore(t))
	key := a.Opened(bk, path)
	if key == "" {
		t.Fatal("no key for the opened book")
	}
	return a, bk, layout, path
}

// A bookmark is made at the page being read, shows up as a current row, is
// dropped by a second toggle, and survives a restart through the store.
func TestBookmarkRoundTrip(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	page := layout.PageCount() / 2
	a.Refresh(bk, layout, page)
	loc, ok := layout.LocationOfPage(page)
	if !ok {
		t.Fatal("no location for the chosen page")
	}

	if bookmarked, changed := a.ToggleBookmark(loc); !bookmarked || !changed {
		t.Fatalf("the page was not bookmarked: %v %v", bookmarked, changed)
	}
	if len(a.Bookmarks.Entries) != 1 {
		t.Fatalf("panel lists %d rows, want 1", len(a.Bookmarks.Entries))
	}
	e := a.Bookmarks.Entries[0]
	if !e.Current || !e.HasLoc {
		t.Errorf("row is not current and placed: %+v", e)
	}
	if want := locales.BookmarkPage(page + 1); e.Detail != want {
		t.Errorf("detail = %q, want %q", e.Detail, want)
	}
	if !a.Bookmarked(loc) {
		t.Error("Bookmarked says the page is not bookmarked")
	}

	// The store holds it, so a new App over the same file finds it and marks
	// the row current again.
	if err := a.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	b := New(a.Store)
	b.BookKey = a.BookKey
	b.Refresh(bk, layout, page)
	if len(b.Bookmarks.Entries) != 1 || !b.Bookmarks.Entries[0].Current {
		t.Fatalf("reopened panel = %+v", b.Bookmarks.Entries)
	}
	var jumped render.Location
	b.Jump = func(loc render.Location) { jumped = loc }
	b.Bookmarks.OnJump(b.Bookmarks.Entries[0])
	if jumped == (render.Location{}) {
		t.Error("the reopened row did not jump")
	}

	// A layout of another type size keeps the bookmark: the store's Position
	// names a node and an anchor, and PageFor resolves it again.
	if bookmarked, changed := a.ToggleBookmark(loc); bookmarked || !changed {
		t.Fatalf("the second toggle left bookmarked=%v changed=%v, want it removed", bookmarked, changed)
	}
	if len(a.Bookmarks.Entries) != 0 {
		t.Fatalf("panel lists %d rows after the toggle, want none", len(a.Bookmarks.Entries))
	}
	if a.Bookmarked(loc) {
		t.Error("Bookmarked still says the page is bookmarked")
	}
}

// The panel's add control adds the reading place once; pressing it again at a
// bookmarked place adds nothing, so the list cannot fill with duplicates.
func TestBookmarkHereDoesNotDuplicate(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	page := layout.PageCount() - 1
	a.Refresh(bk, layout, page)

	if !a.BookmarkHere() {
		t.Fatal("BookmarkHere added nothing")
	}
	if a.BookmarkHere() {
		t.Error("BookmarkHere added the same place twice")
	}
	if len(a.Bookmarks.Entries) != 1 {
		t.Fatalf("panel lists %d rows, want 1", len(a.Bookmarks.Entries))
	}

	id := a.Bookmarks.Entries[0].ID
	if !a.RemoveBookmark(id) {
		t.Fatal("RemoveBookmark reported the row was not there")
	}
	if len(a.Bookmarks.Entries) != 0 {
		t.Errorf("panel still lists %d rows", len(a.Bookmarks.Entries))
	}
}

// A scrolling column has no pages, so a bookmark is worded by its progress and
// jumps back to the same content offset.
func TestBookmarkInScrollLayout(t *testing.T) {
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
	loc, ok := layout.LocationAt(layout.ContentHeight() / 2)
	if !ok {
		t.Fatal("no location halfway down the book")
	}
	a := New(newStore(t))
	a.Opened(bk, path)
	a.RefreshAt(bk, layout, loc)

	if !a.BookmarkHere() {
		t.Fatal("the scrolling place was not bookmarked")
	}
	e := a.Bookmarks.Entries[0]
	if !e.Current || !e.HasLoc {
		t.Errorf("row is not current and placed: %+v", e)
	}
	if e.Detail == "" || e.Detail == locales.BookmarkPage(1) {
		t.Errorf("scroll bookmark detail = %q, want a progress", e.Detail)
	}

	var jumped render.Location
	a.Jump = func(loc render.Location) { jumped = loc }
	a.Bookmarks.OnJump(e)
	if !samePlace(layout, jumped, loc) {
		t.Errorf("jumped to %+v, want %+v", jumped, loc)
	}

	if !a.Bookmarked(loc) {
		t.Error("Bookmarked does not find the row at the reading place")
	}
}

// A toggle says what it did: a click the open book cannot answer changes
// nothing, so the host must not report a bookmark as removed.
func TestToggleBookmarkReportsWhatItDid(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	a.Refresh(bk, layout, 0)
	loc, ok := layout.LocationOfPage(0)
	if !ok {
		t.Fatal("no location for the first page")
	}

	store := a.Store
	a.Store = nil
	if bookmarked, changed := a.ToggleBookmark(loc); bookmarked || changed {
		t.Fatalf("toggle without a store = %v %v, want nothing done", bookmarked, changed)
	}
	a.Store = store

	if bookmarked, changed := a.ToggleBookmark(loc); !bookmarked || !changed {
		t.Fatalf("the add = %v %v, want the place bookmarked and changed", bookmarked, changed)
	}
	if bookmarked, changed := a.ToggleBookmark(loc); bookmarked || !changed {
		t.Fatalf("the remove = %v %v, want it unbookmarked and changed", bookmarked, changed)
	}
}

// Deleting the chosen bookmark chooses the row that took its place, so the
// panel keeps a row chosen instead of falling back to none after a delete.
func TestRemoveBookmarkChoosesTheNeighbour(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	for page := 0; page < 3; page++ {
		a.Refresh(bk, layout, page)
		if !a.BookmarkHere() {
			t.Fatalf("page %d was not bookmarked", page)
		}
	}
	if len(a.Bookmarks.Entries) != 3 {
		t.Fatalf("panel lists %d rows, want 3", len(a.Bookmarks.Entries))
	}
	a.Bookmarks.Selected = 1
	want := a.Bookmarks.Entries[2].ID
	if !a.RemoveBookmark(a.Bookmarks.Entries[1].ID) {
		t.Fatal("RemoveBookmark reported the row was not there")
	}
	if len(a.Bookmarks.Entries) != 2 {
		t.Fatalf("panel lists %d rows after the delete, want 2", len(a.Bookmarks.Entries))
	}
	if got := a.Bookmarks.SelectedID(); got != want {
		t.Errorf("chosen row = %q after the delete, want the neighbour %q", got, want)
	}
	if a.Bookmarks.Selected != 1 {
		t.Errorf("Selected = %d after the delete, want the row that moved up", a.Bookmarks.Selected)
	}

	// The last row goes: nothing is left to choose.
	if !a.RemoveBookmark(a.Bookmarks.Entries[1].ID) || !a.RemoveBookmark(a.Bookmarks.Entries[0].ID) {
		t.Fatal("a remaining row was not removed")
	}
	if a.Bookmarks.SelectedID() != "" {
		t.Errorf("chosen row = %q after the list emptied", a.Bookmarks.SelectedID())
	}
}

// Nothing is bookmarked or annotated against the layout of the book that was
// open before: opening a book moves the key, and the panels wait for the
// refresh that names a place of the new one.
func TestANewBookRefusesThePreviousLayout(t *testing.T) {
	a, bk, layout, path := bookmarkFixture(t)
	a.Refresh(bk, layout, 0)
	from, to := passageOn(t, layout, 0)

	// Opening the book again is what the host does: the key moves at once, the
	// first layout of it comes later.
	a.Opened(bk, path)
	if a.BookmarkHere() {
		t.Error("BookmarkHere stored a place of the previous layout")
	}
	if _, ok := a.AddNote(from, to, "旧布局里的一段"); ok {
		t.Error("AddNote stored a passage of the previous layout")
	}
	if got := a.Store.BookmarksOf(a.BookKey); len(got) != 0 {
		t.Errorf("the store holds %d bookmarks made against a stale layout", len(got))
	}

	// Once the panels are refreshed for it, the same calls work again.
	a.Refresh(bk, layout, 0)
	if !a.BookmarkHere() {
		t.Error("BookmarkHere refused a place of the layout in hand")
	}
	if _, ok := a.AddNote(from, to, "这一段"); !ok {
		t.Error("AddNote refused a passage of the layout in hand")
	}
}

// Bookmarks belong to the book they were made in: opening another book lists
// its own, never the rows of the one before, and switching back brings them.
func TestBookmarksAreKeptPerBook(t *testing.T) {
	a, bk, layout, path := bookmarkFixture(t)
	a.Refresh(bk, layout, 0)
	if !a.BookmarkHere() {
		t.Fatal("the first page was not bookmarked")
	}
	keyA := a.BookKey

	// The same file reached under another path is another book to the store.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "another-copy.epub")
	if err := os.WriteFile(other, data, 0o644); err != nil {
		t.Fatal(err)
	}
	keyB := a.Opened(bk, other)
	if keyB == keyA {
		t.Fatalf("both paths share the key %q", keyA)
	}
	a.Refresh(bk, layout, 0)
	if len(a.Bookmarks.Entries) != 0 {
		t.Errorf("the second book lists %d rows of the first: %+v", len(a.Bookmarks.Entries), a.Bookmarks.Entries)
	}
	if !a.BookmarkHere() {
		t.Fatal("the second book's first page was not bookmarked")
	}
	if len(a.Store.BookmarksOf(keyA)) != 1 || len(a.Store.BookmarksOf(keyB)) != 1 {
		t.Errorf("store holds %d rows for the first book and %d for the second, want one each",
			len(a.Store.BookmarksOf(keyA)), len(a.Store.BookmarksOf(keyB)))
	}

	// Back to the first book: its own row is listed again.
	a.BookKey = keyA
	a.Refresh(bk, layout, 0)
	if len(a.Bookmarks.Entries) != 1 {
		t.Fatalf("the first book lists %d rows after switching back, want 1", len(a.Bookmarks.Entries))
	}
	if !a.Bookmarks.Entries[0].Current {
		t.Errorf("the first book's row is not at the reading place: %+v", a.Bookmarks.Entries[0])
	}
}
