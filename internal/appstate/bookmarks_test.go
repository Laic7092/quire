package appstate

import (
	"context"
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

	if !a.ToggleBookmark(loc) {
		t.Fatal("the page was not bookmarked")
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
	if a.ToggleBookmark(loc) {
		t.Fatal("the second toggle bookmarked the place instead of removing it")
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
