package appstate

import (
	"sort"
	"strings"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/notes"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx/render"
)

// rebuildNotes republishes the notes panel from the store for the open book and
// the layout the panels were refreshed with.
//
// It is the only place a stored note becomes a row: the layout resolves each
// recorded Position to a place, which is what the row jumps to and what its
// subtitle is worded from. A note the layout cannot place at both ends stays in
// the list, muted and not clickable, rather than vanishing from the reader's
// book; its comment can still be edited and the note still removed.
func (a *App) rebuildNotes(bk *book.Book) {
	p := &a.Notes
	selected := p.SelectedID()
	p.Entries = p.Entries[:0]
	p.Selected = -1
	if a.Store == nil || a.BookKey == "" {
		return
	}
	for _, n := range a.Store.NotesOf(a.BookKey) {
		e := notes.Entry{ID: n.ID, Text: n.Text, Comment: n.Comment}
		chapter := n.From.Chapter
		if a.layout != nil {
			from, howFrom := store.Locate(a.layout, n.From)
			to, howTo := store.Locate(a.layout, n.To)
			e.From, e.To = from, to
			e.HasLoc = howFrom != "none" && howTo != "none"
			if e.HasLoc {
				chapter = from.Chapter
			}
		}
		e.Subtitle = a.noteSubtitle(bk, n, chapter, e.HasLoc)
		if e.HasLoc && a.hasLoc {
			e.Current = samePlace(a.layout, e.From, a.loc)
		}
		p.Entries = append(p.Entries, e)
	}
	sortNotes(p.Entries)
	for i, e := range p.Entries {
		if e.ID == selected {
			p.Selected = i
			break
		}
	}
}

// noteSubtitle is the line above a row's passage: the chapter it is in and, on
// paper, the page it starts on; in a scrolling column, how far into the book it
// sits.
func (a *App) noteSubtitle(bk *book.Book, n store.Note, chapter int, resolved bool) string {
	title := annotationTitle(bk, "", chapter)
	if detail := a.annotationDetail(n.From, resolved); detail != "" {
		return title + " · " + detail
	}
	return title
}

// sortNotes orders rows the way the book reads: the chapter of the passage's
// start, then the node and rune offset inside it. Notes the layout could not
// place keep their stored order at the end.
func sortNotes(rows []notes.Entry) {
	sort.SliceStable(rows, func(i, j int) bool {
		x, y := rows[i], rows[j]
		if x.HasLoc != y.HasLoc {
			return x.HasLoc
		}
		if !x.HasLoc {
			return false
		}
		if x.From.Chapter != y.From.Chapter {
			return x.From.Chapter < y.From.Chapter
		}
		if x.From.Node != y.From.Node {
			return x.From.Node < y.From.Node
		}
		return x.From.Offset < y.From.Offset
	})
}

// AddNote stores the passage between two document locations as a note with the
// text the reader selected, chooses its row and opens the panel. It is how a
// selection becomes a note; the page's context menu calls it.
//
// from and to are the ends of the selection in gesture order, which the store
// keeps as they are: the engine orders them itself when it paints the marks.
func (a *App) AddNote(from, to render.Location, text string) (string, bool) {
	text = strings.TrimSpace(text)
	if a.Store == nil || a.BookKey == "" || a.layout == nil || text == "" {
		return "", false
	}
	start, ok := store.PositionAt(a.layout, from)
	if !ok {
		return "", false
	}
	end, ok := store.PositionAt(a.layout, to)
	if !ok {
		return "", false
	}
	n := a.Store.AddNote(a.BookKey, store.Note{From: start, To: end, Text: text})
	a.Store.SaveSoon(0)
	a.rebuildNotes(a.book)
	a.Notes.Select(n.ID)
	a.notesChanged()
	return n.ID, true
}

// SaveNote writes the reader's comment onto the note with id, trimming the
// surrounding space. An empty comment leaves a bare highlight.
func (a *App) SaveNote(id, comment string) bool {
	if a.Store == nil || a.BookKey == "" || id == "" {
		return false
	}
	for _, n := range a.Store.NotesOf(a.BookKey) {
		if n.ID != id {
			continue
		}
		n.Comment = strings.TrimSpace(comment)
		if !a.Store.UpdateNote(a.BookKey, n) {
			return false
		}
		a.Store.SaveSoon(0)
		a.rebuildNotes(a.book)
		a.notesChanged()
		return true
	}
	return false
}

// RemoveNote drops the note with id and republishes the panel. It reports
// whether the note was there.
func (a *App) RemoveNote(id string) bool {
	if a.Store == nil || a.BookKey == "" || id == "" {
		return false
	}
	if !a.Store.RemoveNote(a.BookKey, id) {
		return false
	}
	a.Store.SaveSoon(0)
	a.rebuildNotes(a.book)
	a.notesChanged()
	return true
}

// notesChanged tells the host that the set of notes changed, so it can rebuild
// what it derives from them, such as the highlight rectangles it paints on the
// page.
func (a *App) notesChanged() {
	if a.OnNotes != nil {
		a.OnNotes()
	}
}
