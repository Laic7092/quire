package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/notes"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
)

// addNoteFromSelection turns the text the reader dragged over into a note and
// opens the notes panel at it, where the comment can be written. The page's
// context menu calls it; the selection stays on the page so the new mark can
// be seen against it.
func (a *app) addNoteFromSelection() {
	if a.pan == nil || a.layout == nil {
		return
	}
	text := a.selectedText()
	if text == "" {
		return
	}
	from, to := a.sel.Ends()
	if _, ok := a.pan.AddNote(from, to, text); !ok {
		return
	}
	a.pan.Show(appstate.SidebarNotes)
	a.say(locales.NoteAdded())
}

// wireNoteActions lets the notes panel report the result of saving or deleting a
// note in the same folio line as the rest of the reader's actions. appstate wires
// the operations themselves; this only adds the visible answer.
func (a *app) wireNoteActions() {
	if a.pan == nil {
		return
	}
	a.pan.Notes.OnSave = func(e notes.Entry, comment string) {
		if a.pan.SaveNote(e.ID, comment) {
			a.say(locales.NoteSaved())
			return
		}
		a.say(locales.NoteSaveFailed())
	}
	a.pan.Notes.OnRemove = func(e notes.Entry) {
		if a.pan.RemoveNote(e.ID) {
			a.say(locales.NoteRemoved())
		}
	}
}

// refreshNoteMarks rebuilds the rectangles of every note for the current
// layout. The engine shapes each passage, so this runs when a layout or a set
// of notes arrives, never while drawing. A note with an end the layout cannot
// place is skipped: half a passage marked would be worse than none.
func (a *app) refreshNoteMarks() {
	a.noteQuads = a.noteQuads[:0]
	l := a.showing()
	if l == nil || a.pan == nil {
		return
	}
	for _, e := range a.pan.Notes.Entries {
		if !e.HasLoc {
			continue
		}
		a.noteQuads = append(a.noteQuads, l.Selection(e.From, e.To)...)
	}
}

// drawNoteMarks fills the notes' rectangles over one page of the sheet. It runs
// inside the box's Draw callback and only fills rectangles it already has: the
// search hits and the selection are painted over them, so what the reader is
// acting on now stays on top.
func (a *app) drawNoteMarks(p *ui.Painter, box ui.Rect, page int) {
	if len(a.noteQuads) == 0 {
		return
	}
	a.paint.FillQuads(p, box, page, a.noteQuads, reader.Current().Note)
}

// drawScrollNoteMarks is drawNoteMarks for a scrolling column: the quads carry
// content coordinates, so they are cut to the viewport and moved by the column's
// own offset, the way the hits and the selection are (see FillScrollQuads).
func (a *app) drawScrollNoteMarks(p *ui.Painter, view ui.Rect, y float32) {
	if len(a.noteQuads) == 0 {
		return
	}
	a.paint.FillScrollQuads(p, view, a.noteQuads, reader.Current().Note, y)
}
