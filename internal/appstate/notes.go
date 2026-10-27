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
//
// Both ends must resolve exactly — through the node or the anchor — because the
// host paints the passage between them: a coarse fallback, the recorded
// progress or the chapter start, would mark a region the reader never wrote
// about. A bookmark may fall back that way; a passage may not.
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
			e.HasLoc = exactPlace(howFrom) && exactPlace(howTo)
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
	if p.Selected < 0 {
		// Nothing is chosen, so no comment is being edited: a draft left here
		// would be one written for a row that is gone.
		p.Draft = ""
	}
}

// exactPlace reports whether Locate found the recorded place itself rather than
// falling back to the progress or the chapter it was in.
func exactPlace(how string) bool {
	return how == "node" || how == "anchor"
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
// from and to are the ends of the selection in gesture order, which may be
// bottom-up. They are stored in reading order, so the same passage dragged the
// other way is one note and not two, and a row jumps to the top of the passage
// rather than to the end the drag started at.
func (a *App) AddNote(from, to render.Location, text string) (string, bool) {
	text = strings.TrimSpace(text)
	layout := a.currentLayout()
	if a.Store == nil || a.BookKey == "" || layout == nil || text == "" {
		return "", false
	}
	from, to = orderedEnds(layout, from, to)
	start, ok := store.PositionAt(layout, from)
	if !ok {
		return "", false
	}
	end, ok := store.PositionAt(layout, to)
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

// orderedEnds puts the two ends of a selection in reading order. The content
// offset is the coordinate Progress and the store's positions use, so it orders
// the ends in a paged layout and a scrolling column alike; two ends on one line
// share that offset, so the node and the rune offset settle them. When either
// end cannot be placed the gesture order is kept.
func orderedEnds(layout *render.Layout, from, to render.Location) (render.Location, render.Location) {
	fy, okFrom := layout.OffsetOf(from)
	ty, okTo := layout.OffsetOf(to)
	if !okFrom || !okTo {
		return from, to
	}
	switch {
	case fy < ty:
		return from, to
	case fy > ty:
		return to, from
	}
	if beforeInBook(layout, to, from) {
		return to, from
	}
	return from, to
}

// beforeInBook reports whether x reads before y, from its chapter, node and
// rune offset: the builder numbers nodes in reading order, so that triple is
// the book's order even where two places share a content offset.
func beforeInBook(layout *render.Layout, x, y render.Location) bool {
	if x.Anchor != render.NoAnchor {
		if r, ok := layout.Locate(x.Anchor); ok {
			x.Chapter, x.Node = r.Chapter, r.Node
		}
	}
	if y.Anchor != render.NoAnchor {
		if r, ok := layout.Locate(y.Anchor); ok {
			y.Chapter, y.Node = r.Chapter, r.Node
		}
	}
	switch {
	case x.Chapter != y.Chapter:
		return x.Chapter < y.Chapter
	case x.Node != y.Node:
		return x.Node < y.Node
	}
	return x.Offset < y.Offset
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
// whether the note was there. The note that takes its place is chosen, so the
// editor moves on to it instead of closing after every delete.
func (a *App) RemoveNote(id string) bool {
	if a.Store == nil || a.BookKey == "" || id == "" {
		return false
	}
	index := indexOfNote(a.Notes.Entries, id)
	chosen := a.Notes.SelectedID() == id
	if !a.Store.RemoveNote(a.BookKey, id) {
		return false
	}
	a.Store.SaveSoon(0)
	a.rebuildNotes(a.book)
	if chosen {
		a.selectNoteAt(index)
	}
	a.notesChanged()
	return true
}

// selectNoteAt chooses the note at index i, clamped to the rows left. Select
// loads its comment into the editor, so what is on screen keeps naming the row
// that is chosen.
func (a *App) selectNoteAt(i int) {
	if i < 0 || len(a.Notes.Entries) == 0 {
		return
	}
	a.Notes.Select(a.Notes.Entries[min(i, len(a.Notes.Entries)-1)].ID)
}

// indexOfNote is the row index of the note with id, -1 when the panel does not
// list it.
func indexOfNote(rows []notes.Entry, id string) int {
	for i, e := range rows {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// notesChanged tells the host that the set of notes changed, so it can rebuild
// what it derives from them, such as the highlight rectangles it paints on the
// page.
func (a *App) notesChanged() {
	if a.OnNotes != nil {
		a.OnNotes()
	}
}
