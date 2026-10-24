package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
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
	a.status = locales.NoteAdded()
}

// refreshNoteMarks rebuilds the rectangles of every note for the current
// layout. The engine shapes each passage, so this runs when a layout or a set
// of notes arrives, never while drawing. A note with an end the layout cannot
// place is skipped: half a passage marked would be worse than none.
func (a *app) refreshNoteMarks() {
	a.noteQuads = a.noteQuads[:0]
	if a.layout == nil || a.pan == nil {
		return
	}
	for _, e := range a.pan.Notes.Entries {
		if !e.HasLoc {
			continue
		}
		a.noteQuads = append(a.noteQuads, a.layout.Selection(e.From, e.To)...)
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
