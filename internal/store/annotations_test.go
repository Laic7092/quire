package store

import (
	"path/filepath"
	"testing"
	"time"
)

func place(node uint32, offset int) Position {
	return Position{Chapter: 1, Node: node, Anchor: node + 100, Offset: offset, Progress: 0.5}
}

// A bookmark is stored with an identity and a creation stamp, the same place
// is not stored twice, and its label can be rewritten without losing the
// place.
func TestBookmarkLifecycle(t *testing.T) {
	s, _ := openTemp(t)
	key := "book-1"

	b := s.AddBookmark(key, Bookmark{Position: place(3, 7)})
	if b.ID == "" || b.Created.IsZero() {
		t.Fatalf("AddBookmark returned %+v without identity or stamp", b)
	}
	if got := s.BookmarksOf(key); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("bookmarks = %+v, want the one just added", got)
	}

	// The same place returns the row already there instead of a second one.
	again := s.AddBookmark(key, Bookmark{Position: place(3, 7)})
	if again.ID != b.ID {
		t.Errorf("a second add made %q, want the existing %q", again.ID, b.ID)
	}
	if got := s.BookmarksOf(key); len(got) != 1 {
		t.Errorf("bookmarks = %d rows, want 1", len(got))
	}

	// Renaming keeps the place and the identity.
	b.Label = "读到这里"
	if !s.UpdateBookmark(key, b) {
		t.Fatal("UpdateBookmark reported the row was not there")
	}
	got := s.BookmarksOf(key)
	if len(got) != 1 || got[0].Label != "读到这里" || !samePlace(got[0].Position, place(3, 7)) {
		t.Errorf("renamed bookmark = %+v", got)
	}

	// The slice handed out is a copy: the caller cannot change the store by
	// writing to it.
	got[0].Label = "changed outside"
	if s.BookmarksOf(key)[0].Label != "读到这里" {
		t.Error("BookmarksOf handed out the live slice")
	}

	// Removal drops the whole (now empty) key.
	if !s.RemoveBookmark(key, b.ID) {
		t.Fatal("RemoveBookmark reported the row was not there")
	}
	if s.RemoveBookmark(key, b.ID) {
		t.Error("RemoveBookmark removed a row twice")
	}
	if got := s.BookmarksOf(key); len(got) != 0 {
		t.Errorf("bookmarks = %+v after removal", got)
	}
	if _, ok := s.State().Bookmarks[key]; ok {
		t.Error("an empty bookmark list was left in the state")
	}
}

// Notes carry both ends of the passage, its text and the reader's comment; an
// update stamps Modified but keeps Created.
func TestNoteLifecycle(t *testing.T) {
	s, path := openTemp(t)
	key := "book-2"

	n := s.AddNote(key, Note{From: place(4, 2), To: place(5, 9), Text: "一段话"})
	if n.ID == "" || n.Created.IsZero() || !n.Modified.Equal(n.Created) {
		t.Fatalf("AddNote returned %+v", n)
	}
	if got := s.NotesOf(key); len(got) != 1 || got[0].Text != "一段话" {
		t.Fatalf("notes = %+v", got)
	}
	if dup := s.AddNote(key, Note{From: place(4, 2), To: place(5, 9), Text: "又一" + "段话"}); dup.ID != n.ID {
		t.Errorf("adding the same passage made %q, want %q", dup.ID, n.ID)
	}

	time.Sleep(2 * time.Millisecond) // Modified is a wall clock stamp
	updated := n
	updated.Comment = "这就是所谓「精神胜利法」"
	if !s.UpdateNote(key, updated) {
		t.Fatal("UpdateNote reported the note was not there")
	}
	got := s.NotesOf(key)[0]
	if got.Comment != updated.Comment {
		t.Errorf("comment = %q, want %q", got.Comment, updated.Comment)
	}
	if !got.Created.Equal(n.Created) || !got.Modified.After(n.Created) {
		t.Errorf("stamps = created %v modified %v", got.Created, got.Modified)
	}

	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	back := again.NotesOf(key)
	if len(back) != 1 || back[0].Comment != updated.Comment || back[0].Text != "一段话" {
		t.Errorf("reopened notes = %+v", back)
	}

	if !again.RemoveNote(key, n.ID) {
		t.Fatal("RemoveNote reported the note was not there")
	}
	if got := again.NotesOf(key); len(got) != 0 {
		t.Errorf("notes = %+v after removal", got)
	}
}

// A state file written before bookmarks and notes existed opens with empty
// collections, not nil maps: adding the first row must not panic.
func TestOldStateGetsAnnotationMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"version":1,"books":[],"positions":{},"prefs":{}}`
	if err := writeAtomic(path, []byte(old)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.State().Bookmarks == nil || s.State().Notes == nil {
		t.Fatal("the old state kept nil annotation maps")
	}
	s.AddBookmark("k", Bookmark{Position: place(1, 0)})
	s.AddNote("k", Note{From: place(1, 0), To: place(1, 2)})
	if len(s.BookmarksOf("k")) != 1 || len(s.NotesOf("k")) != 1 {
		t.Error("the first rows were not stored")
	}
}
