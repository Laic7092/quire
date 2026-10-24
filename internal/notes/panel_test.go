package notes

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

func panelEntries() []Entry {
	return []Entry{
		{ID: "a", Subtitle: "第一章 · " + locales.BookmarkPage(3),
			Text: "第一段被划住的文字。", HasLoc: true,
			From: render.Location{Chapter: 0, Node: 1, Offset: 0},
			To:   render.Location{Chapter: 0, Node: 1, Offset: 12}},
		{ID: "b", Subtitle: "第二章 · " + locales.BookmarkPage(9),
			Text: "第二段。", Comment: "这里说得对", Current: true, HasLoc: true,
			From: render.Location{Chapter: 1, Node: 2, Offset: 0},
			To:   render.Location{Chapter: 1, Node: 2, Offset: 4}},
		{ID: "c", Subtitle: "找不到的一则", Text: "旧笔记", HasLoc: false},
	}
}

func TestPanelDrawsRows(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: -1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 460)
	tt.Frame()

	if !tt.HasText(locales.NoteCount(3)) {
		t.Errorf("count is missing: %q", tt.Texts())
	}
	for _, e := range p.Entries {
		if !tt.HasText(e.Text) {
			t.Errorf("passage %q is missing", e.Text)
		}
	}
	if !tt.HasText("这里说得对") {
		t.Errorf("comment is missing: %q", tt.Texts())
	}
}

func TestPanelEmptyState(t *testing.T) {
	p := &Panel{}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 320)
	tt.Frame()
	if !tt.HasText(locales.EmptyNotes()) || !tt.HasText(locales.EmptyNotesHint()) {
		t.Errorf("empty state is missing: %q", tt.Texts())
	}
}

func TestPanelClickSelectsLoadsAndJumps(t *testing.T) {
	var jumped []Entry
	p := &Panel{Entries: panelEntries(), Selected: -1}
	p.OnJump = func(e Entry) { jumped = append(jumped, e) }
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 460)
	tt.Frame()

	if err := tt.Click("第一段被划住的文字。"); err != nil {
		t.Fatal(err)
	}
	if p.Selected != 0 {
		t.Errorf("Selected = %d after a click on row 0, want 0", p.Selected)
	}
	if p.Draft != "" {
		t.Errorf("draft = %q, want the row's empty comment", p.Draft)
	}
	if len(jumped) != 1 || jumped[0].ID != "a" {
		t.Fatalf("jumped to %v, want note a", jumped)
	}

	// A row the layout cannot place may be chosen, but does not jump.
	if err := tt.Click("旧笔记"); err != nil {
		t.Fatal(err)
	}
	if len(jumped) != 1 {
		t.Errorf("an unplaced note jumped: %v", jumped)
	}
	if p.Selected != 2 {
		t.Errorf("Selected = %d after a click on row 2, want 2", p.Selected)
	}
}

func TestPanelSelectByID(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: -1}
	if !p.Select("b") {
		t.Fatal("Select did not find note b")
	}
	if p.Selected != 1 || p.Draft != "这里说得对" {
		t.Errorf("Selected = %d, draft = %q", p.Selected, p.Draft)
	}
	if p.Dirty() {
		t.Error("a freshly selected note is dirty")
	}
	p.Draft = "改一改"
	if !p.Dirty() {
		t.Error("a changed draft is not dirty")
	}
	if p.Select("missing") {
		t.Error("Select found a note that does not exist")
	}
}

func TestPanelSavesAndDeletes(t *testing.T) {
	saved := ""
	removed := ""
	p := &Panel{Entries: panelEntries(), Selected: -1}
	p.OnSave = func(e Entry, comment string) { saved = e.ID + ":" + comment }
	p.OnRemove = func(e Entry) { removed = e.ID }
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 360, 520)
	tt.Frame()

	if !p.Select("a") {
		t.Fatal("Select did not find note a")
	}
	p.Draft = "  我的想法  "
	tt.Frame()
	if err := tt.Click(locales.NoteSave()); err != nil {
		t.Fatalf("save: %v (%q)", err, tt.Texts())
	}
	if saved != "a:我的想法" {
		t.Errorf("saved %q, want the trimmed draft", saved)
	}

	if err := tt.Click(locales.DeleteNote()); err != nil {
		t.Fatalf("delete: %v (%q)", err, tt.Texts())
	}
	if removed != "a" {
		t.Errorf("removed %q, want note a", removed)
	}
}

// The save control does nothing while the draft still says what the note
// already says.
func TestPanelSaveDisabledWithoutChange(t *testing.T) {
	called := false
	p := &Panel{Entries: panelEntries(), Selected: -1}
	p.OnSave = func(Entry, string) { called = true }
	if !p.Select("b") {
		t.Fatal("Select did not find note b")
	}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 360, 520)
	tt.Frame()
	if err := tt.Click(locales.NoteSave()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("the save control ran with nothing to save")
	}
}
