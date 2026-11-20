package bookmarks

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/locales"
)

func panelEntries() []Entry {
	return []Entry{
		{ID: "a", Title: "第一卷 风起", Detail: locales.BookmarkPage(3),
			Loc: render.Location{Chapter: 0, Node: 1, Offset: 0}, HasLoc: true},
		{ID: "b", Title: "第一章 初见", Detail: locales.BookmarkPage(9),
			Loc: render.Location{Chapter: 1, Node: 2, Offset: 4}, HasLoc: true, Current: true},
		{ID: "c", Title: "找不到的一处", HasLoc: false},
	}
}

func TestPanelDrawsRows(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: 1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 420)
	tt.Frame()

	if !tt.HasText(locales.BookmarkCount(3)) {
		t.Errorf("count is missing: %q", tt.Texts())
	}
	for _, e := range p.Entries {
		if !tt.HasText(e.Title) {
			t.Errorf("row %q is missing", e.Title)
		}
	}
	if !tt.HasText(locales.BookmarkPage(3)) || !tt.HasText(locales.BookmarkPage(9)) {
		t.Errorf("page details are missing: %q", tt.Texts())
	}
}

func TestPanelEmptyState(t *testing.T) {
	p := &Panel{}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 320)
	tt.Frame()
	if !tt.HasText(locales.EmptyBookmarks()) || !tt.HasText(locales.EmptyBookmarksHint()) {
		t.Errorf("empty state is missing: %q", tt.Texts())
	}
}

func TestPanelJumpOnClick(t *testing.T) {
	var jumped []Entry
	p := &Panel{Entries: panelEntries(), Selected: -1, OnJump: func(e Entry) { jumped = append(jumped, e) }}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 420)
	tt.Frame()

	if err := tt.Click("第一章 初见"); err != nil {
		t.Fatal(err)
	}
	if p.Selected != 1 {
		t.Errorf("Selected = %d after a click on row 1, want 1", p.Selected)
	}
	if len(jumped) != 1 || jumped[0].ID != "b" {
		t.Fatalf("jumped to %v, want bookmark b", jumped)
	}

	// A row the layout cannot place may be chosen, but does not jump.
	if err := tt.Click("找不到的一处"); err != nil {
		t.Fatal(err)
	}
	if len(jumped) != 1 {
		t.Errorf("an unplaced row jumped: %v", jumped)
	}
}

func TestPanelAddAndRemove(t *testing.T) {
	added, removed := 0, ""
	entries := panelEntries()
	entries[1].Current = false
	p := &Panel{Entries: entries, Selected: 0}
	p.OnAdd = func() { added++ }
	p.OnRemove = func(e Entry) { removed = e.ID }
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 420)
	tt.Frame()

	if err := tt.Click(locales.AddBookmark()); err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Errorf("add control ran %d times, want 1", added)
	}

	// The delete control shows on the chosen row only, so its label is unique.
	if err := tt.Click(locales.DeleteBookmark()); err != nil {
		t.Fatal(err)
	}
	if removed != "a" {
		t.Errorf("removed %q, want the chosen bookmark a", removed)
	}
}

// The add control has nothing to add while a row stands at the reading
// position, so it is disabled.
func TestPanelAddDisabledAtCurrent(t *testing.T) {
	added := 0
	p := &Panel{Entries: panelEntries(), Selected: -1, OnAdd: func() { added++ }}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 420)
	tt.Frame()
	if err := tt.Click(locales.AddBookmark()); err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("the add control ran at a bookmarked place")
	}
}

// The delete control's handler takes the row out of Entries, which is the
// list's own count: it must run after the build, or the list goes on building
// rows the panel no longer has, and the frame panics.
func TestPanelRemoveShrinksRowsAfterBuild(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: 0}
	p.OnRemove = func(e Entry) {
		for i := range p.Entries {
			if p.Entries[i].ID == e.ID {
				p.Entries = append(p.Entries[:i], p.Entries[i+1:]...)
				return
			}
		}
	}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 420)
	tt.Frame()
	if err := tt.Click(locales.DeleteBookmark()); err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 2 {
		t.Fatalf("panel lists %d rows after the delete, want 2", len(p.Entries))
	}
	if !tt.HasText(locales.BookmarkCount(2)) || tt.HasText("第一卷 风起") {
		t.Errorf("the frame did not show the remaining rows: %q", tt.Texts())
	}
}
