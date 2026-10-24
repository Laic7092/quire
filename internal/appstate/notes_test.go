package appstate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// passageOn returns two locations on a page, from its first lines down a few
// of them: a passage a reader could have dragged over.
func passageOn(t *testing.T, layout *render.Layout, page int) (from, to render.Location) {
	t.Helper()
	pg := layout.Page(page)
	if pg == nil {
		t.Fatal("no page to select a passage from")
	}
	a, ok := pg.HitTest(paint.Point{X: 90, Y: 70})
	if !ok {
		t.Fatal("no location at the page's first lines")
	}
	b, ok := pg.HitTest(paint.Point{X: 300, Y: 130})
	if !ok {
		t.Fatal("no location a few lines down")
	}
	return a, b
}

// A note keeps the passage, its ends and the reader's comment; it shows up as
// a current row, survives a restart and is removed by id.
func TestNoteRoundTrip(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	page := min(2, layout.PageCount()-1)
	a.Refresh(bk, layout, page)

	changes := 0
	a.OnNotes = func() { changes++ }

	from, to := passageOn(t, layout, page)
	id, ok := a.AddNote(from, to, "  一段被划住的文字  ")
	if !ok || id == "" {
		t.Fatal("AddNote stored nothing")
	}
	if changes != 1 {
		t.Errorf("OnNotes ran %d times after adding, want 1", changes)
	}
	if len(a.Notes.Entries) != 1 {
		t.Fatalf("panel lists %d notes, want 1", len(a.Notes.Entries))
	}
	e := a.Notes.Entries[0]
	if e.Text != "一段被划住的文字" {
		t.Errorf("text = %q, want the trimmed passage", e.Text)
	}
	if !e.HasLoc {
		t.Errorf("note is not placed: %+v", e)
	}
	if !e.Current {
		t.Errorf("note starting on the reading page is not current: %+v", e)
	}
	if !strings.Contains(e.Subtitle, locales.BookmarkPage(page+1)) {
		t.Errorf("subtitle = %q, want the page %d", e.Subtitle, page+1)
	}
	if a.Notes.SelectedID() != id || a.Notes.Draft != "" {
		t.Errorf("new note is chosen = %q, draft = %q", a.Notes.SelectedID(), a.Notes.Draft)
	}

	// A comment is saved trimmed and comes back with the note.
	if !a.SaveNote(id, "  我的批注  ") {
		t.Fatal("SaveNote reported the note was not there")
	}
	if got := a.Notes.Entries[0].Comment; got != "我的批注" {
		t.Errorf("comment = %q, want the trimmed draft", got)
	}
	if changes != 2 {
		t.Errorf("OnNotes ran %d times after saving, want 2", changes)
	}

	if err := a.Store.Flush(); err != nil {
		t.Fatal(err)
	}
	b := New(a.Store)
	b.BookKey = a.BookKey
	b.Refresh(bk, layout, page)
	if len(b.Notes.Entries) != 1 {
		t.Fatalf("reopened panel lists %d notes, want 1", len(b.Notes.Entries))
	}
	if got := b.Notes.Entries[0].Comment; got != "我的批注" {
		t.Errorf("reopened comment = %q", got)
	}

	var jumped render.Location
	a.Jump = func(loc render.Location) { jumped = loc }
	a.Notes.OnJump(e)
	if jumped.Chapter != e.From.Chapter || jumped.Node != e.From.Node || jumped.Offset != e.From.Offset {
		t.Errorf("jumped to %+v, want the passage's start %+v", jumped, e.From)
	}

	if !a.RemoveNote(id) {
		t.Fatal("RemoveNote reported the note was not there")
	}
	if len(a.Notes.Entries) != 0 {
		t.Errorf("panel still lists %d notes", len(a.Notes.Entries))
	}
	if changes != 3 {
		t.Errorf("OnNotes ran %d times after removing, want 3", changes)
	}
}

// Nothing selected is nothing to store, and a note is never made twice for the
// same passage.
func TestAddNoteRejectsEmptyText(t *testing.T) {
	a, bk, layout, _ := bookmarkFixture(t)
	a.Refresh(bk, layout, 0)
	from, to := passageOn(t, layout, 0)
	if _, ok := a.AddNote(from, to, "   "); ok {
		t.Fatal("AddNote stored an empty passage")
	}
	if len(a.Notes.Entries) != 0 {
		t.Errorf("panel lists %d notes after an empty add", len(a.Notes.Entries))
	}

	first, ok := a.AddNote(from, to, "正文")
	if !ok {
		t.Fatal("AddNote stored nothing")
	}
	again, ok := a.AddNote(from, to, "正文")
	if !ok || again != first {
		t.Errorf("a second add made %q, want the existing %q", again, first)
	}
	if len(a.Notes.Entries) != 1 {
		t.Errorf("panel lists %d notes, want 1", len(a.Notes.Entries))
	}
}

// In a scrolling column there are no pages, so a note's subtitle is the
// chapter and the progress into the book, and its ends survive a resolution
// through the flow.
func TestNoteInScrollLayout(t *testing.T) {
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
	from, ok := layout.LocationAt(1200)
	if !ok {
		t.Fatal("no location in the flow")
	}
	to, ok := layout.LocationAt(1600)
	if !ok {
		t.Fatal("no location further down the flow")
	}
	a := New(newStore(t))
	a.Opened(bk, path)
	a.RefreshAt(bk, layout, from)

	if _, ok := a.AddNote(from, to, "滚动模式里的一段"); !ok {
		t.Fatal("AddNote stored nothing")
	}
	e := a.Notes.Entries[0]
	if !e.HasLoc || !e.Current {
		t.Errorf("scroll note is not placed and current: %+v", e)
	}
	if !strings.Contains(e.Subtitle, "%") {
		t.Errorf("subtitle = %q, want a progress", e.Subtitle)
	}
	if !samePlace(layout, e.From, from) || !samePlace(layout, e.To, to) {
		t.Errorf("note ends = %+v/%+v, want %+v/%+v", e.From, e.To, from, to)
	}
}
