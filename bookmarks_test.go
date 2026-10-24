package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
)

// The ribbon in the reading bar marks the page and unmarks it: the label, the
// icon fill and the panel rows all follow the state of the reading position.
func TestBookmarkRibbonToggles(t *testing.T) {
	a := newWindow(t)
	a.pan.Reading = a.currentLoc
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	if _, ok := tt.Find(locales.AddBookmark()); !ok {
		t.Fatalf("no bookmark ribbon in the bar: %q", tt.Texts())
	}
	if err := tt.Click(locales.AddBookmark()); err != nil {
		t.Fatalf("bookmark ribbon: %v (%q)", err, tt.Texts())
	}
	if len(a.pan.Bookmarks.Entries) != 1 {
		t.Fatalf("panel lists %d bookmarks, want 1", len(a.pan.Bookmarks.Entries))
	}
	if !a.bookmarkedAt() {
		t.Error("bookmarkedAt says the page is not bookmarked")
	}
	if got := a.st.BookmarksOf(a.pan.BookKey); len(got) != 1 {
		t.Errorf("store holds %d bookmarks, want 1", len(got))
	}

	// The rebuilt bar offers the opposite command, and takes it.
	tt.Frame()
	if _, ok := tt.Find(locales.RemoveBookmark()); !ok {
		t.Fatalf("the ribbon did not turn into a remove control: %q", tt.Texts())
	}
	if err := tt.Click(locales.RemoveBookmark()); err != nil {
		t.Fatalf("remove bookmark: %v (%q)", err, tt.Texts())
	}
	if len(a.pan.Bookmarks.Entries) != 0 {
		t.Errorf("panel still lists %d bookmarks", len(a.pan.Bookmarks.Entries))
	}
}

// ⌘D is the keyboard way to the same ribbon.
func TestBookmarkShortcut(t *testing.T) {
	a := newWindow(t)
	a.pan.Reading = a.currentLoc
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyD)
	if len(a.pan.Bookmarks.Entries) != 1 {
		t.Fatalf("⌘D added %d bookmarks, want 1", len(a.pan.Bookmarks.Entries))
	}
	tt.Key(ui.Cmd, ui.KeyD)
	if len(a.pan.Bookmarks.Entries) != 0 {
		t.Errorf("⌘D again left %d bookmarks", len(a.pan.Bookmarks.Entries))
	}
}

// A panel row jumps to the place it names, and its delete control removes the
// bookmark without jumping anywhere.
func TestBookmarkPanelJumpsAndDeletes(t *testing.T) {
	a := newWindow(t)
	a.pan.Reading = a.currentLoc
	// Bookmark a page whose chapter has its own name, so the row's title cannot
	// be confused with the book name in the bar.
	page := 0
	for page < a.layout.PageCount() {
		chapter := reader.ChapterOfPage(a.layout, len(a.book.Chapters), page)
		if a.book.Chapters[chapter].Title != a.book.Title {
			break
		}
		page++
	}
	if page >= a.layout.PageCount() {
		t.Skip("the sample book's first chapter has no own title")
	}
	a.page = page
	a.pan.Refresh(a.book, a.layout, a.page)
	a.bookmarkHere()
	if len(a.pan.Bookmarks.Entries) != 1 {
		t.Fatalf("panel lists %d bookmarks, want 1", len(a.pan.Bookmarks.Entries))
	}
	a.page = 0
	a.pan.Refresh(a.book, a.layout, a.page)
	a.pan.Show(appstate.SidebarBookmarks)

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	row := a.pan.Bookmarks.Entries[0].Title
	if err := tt.Click(row); err != nil {
		t.Fatalf("bookmark row: %v (%q)", err, tt.Texts())
	}
	if a.page != page {
		t.Errorf("jumped to page %d, want page %d", a.page, page)
	}

	tt.Frame() // the chosen row shows its delete control
	if err := tt.Click(locales.DeleteBookmark()); err != nil {
		t.Fatalf("delete bookmark: %v (%q)", err, tt.Texts())
	}
	if len(a.pan.Bookmarks.Entries) != 0 {
		t.Errorf("panel still lists %d bookmarks", len(a.pan.Bookmarks.Entries))
	}
	if got := a.st.BookmarksOf(a.pan.BookKey); len(got) != 0 {
		t.Errorf("store still holds %d bookmarks", len(got))
	}
}
