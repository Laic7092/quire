package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// passageLocations is two places on the page being read, down the first lines:
// a passage a reader could have dragged over.
func passageLocations(t *testing.T, a *app) (from, to render.Location) {
	t.Helper()
	pg := a.layout.Page(a.page)
	if pg == nil {
		t.Fatal("no page to take a passage from")
	}
	var ok bool
	if from, ok = pg.HitTest(paint.Point{X: 90, Y: 70}); !ok {
		t.Fatal("no location at the first lines")
	}
	if to, ok = pg.HitTest(paint.Point{X: 300, Y: 130}); !ok {
		t.Fatal("no location a few lines down")
	}
	return from, to
}

// selectPassage drags over the first lines of the page the reader is on.
func selectPassage(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	box, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page box is not labelled: %q", tt.Texts())
	}
	tt.Press(box.X+90, box.Y+80)
	tt.Move(box.X+180, box.Y+96)
	tt.Release(box.X+180, box.Y+96)
	tt.Frame()
	if strings.TrimSpace(a.selectedText()) == "" {
		t.Fatal("the drag selected nothing")
	}
}

// A selection becomes a note from the page's menu: the panel opens at it, the
// passage is painted, and the comment written in the editor is stored.
func TestSelectionBecomesANote(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	selectPassage(t, a, tt)
	want := a.selectedText()

	box, _ := tt.Find(locales.PageLabel())
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if menu := strings.Join(tt.Menu(), "|"); !strings.Contains(menu, locales.AddNote()) {
		t.Fatalf("the page menu has no add-note item: %q", menu)
	}
	if err := tt.ChooseMenuItem(locales.AddNote()); err != nil {
		t.Fatalf("add note: %v (menu %q)", err, strings.Join(tt.Menu(), "|"))
	}
	tt.Frame()

	if a.pan.Sidebar != appstate.SidebarNotes {
		t.Errorf("panel %q after adding a note, want the notes panel", a.pan.Sidebar)
	}
	if len(a.pan.Notes.Entries) != 1 {
		t.Fatalf("panel lists %d notes, want 1", len(a.pan.Notes.Entries))
	}
	e := a.pan.Notes.Entries[0]
	if e.Text != want {
		t.Errorf("passage = %q, want the selected %q", e.Text, want)
	}
	if !e.HasLoc {
		t.Errorf("the note is not placed: %+v", e)
	}
	if len(a.noteQuads) == 0 {
		t.Error("the passage is not painted")
	}
	if a.pan.Notes.SelectedID() != e.ID {
		t.Errorf("chosen note = %q, want the new %q", a.pan.Notes.SelectedID(), e.ID)
	}

	// The editor takes a comment and stores it with the note.
	if err := tt.Click(locales.NoteComment()); err != nil {
		t.Fatalf("comment field: %v (%q)", err, tt.Texts())
	}
	tt.Type("我的想法")
	tt.Frame()
	if err := tt.Click(locales.NoteSave()); err != nil {
		t.Fatalf("save note: %v (%q)", err, tt.Texts())
	}
	notes := a.st.NotesOf(a.pan.BookKey)
	if len(notes) != 1 || notes[0].Comment != "我的想法" {
		t.Errorf("stored notes = %+v", notes)
	}

	// Deleting the chosen note drops it from the panel, the store and the page.
	tt.Frame()
	if err := tt.Click(locales.DeleteNote()); err != nil {
		t.Fatalf("delete note: %v (%q)", err, tt.Texts())
	}
	tt.Frame()
	if len(a.pan.Notes.Entries) != 0 {
		t.Errorf("panel still lists %d notes", len(a.pan.Notes.Entries))
	}
	if len(a.st.NotesOf(a.pan.BookKey)) != 0 {
		t.Error("the store still holds the deleted note")
	}
	if len(a.noteQuads) != 0 {
		t.Errorf("%d passage rectangles are still painted", len(a.noteQuads))
	}
}

// With nothing selected there is nothing to turn into a note, so the menu item
// is disabled.
func TestAddNoteNeedsASelection(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	box, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page box is not labelled: %q", tt.Texts())
	}
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.AddNote()); err == nil {
		t.Error("the menu added a note with nothing selected")
	}
	if len(a.pan.Notes.Entries) != 0 {
		t.Errorf("panel lists %d notes after an empty add", len(a.pan.Notes.Entries))
	}
}

// A chosen note can be edited without its passage being lost, and the panel
// keeps the draft while the reader turns pages.
func TestNoteCommentSurvivesAPageTurn(t *testing.T) {
	a := newWindow(t)
	a.pan.Reading = a.currentLoc
	from, to := passageLocations(t, a)
	if _, ok := a.pan.AddNote(from, to, "被划住的一段"); !ok {
		t.Fatal("AddNote stored nothing")
	}
	a.pan.Show(appstate.SidebarNotes)
	id := a.pan.Notes.SelectedID()

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if err := tt.Click(locales.NoteComment()); err != nil {
		t.Fatalf("comment field: %v (%q)", err, tt.Texts())
	}
	tt.Type("半句话")
	tt.Frame()

	// Turning the page rebuilds the whole window, panel included.
	a.turn(1)
	a.afterPage()
	tt.Frame()
	if a.pan.Notes.SelectedID() != id {
		t.Errorf("chosen note = %q after the turn, want %q", a.pan.Notes.SelectedID(), id)
	}
	if a.pan.Notes.Draft != "半句话" {
		t.Errorf("draft = %q after the turn, want the typed comment", a.pan.Notes.Draft)
	}
}
