// Package notes draws the reader's notes panel: one row per passage the reader
// marked, with the words they wrote about it, and an editor for the comment of
// the chosen row.
//
// A note is a selection kept: its two ends, the passage itself and an optional
// comment. The panel draws them but never makes one — only the page knows what
// is selected — and never reads the store: the host resolves each stored note
// against the current layout and fills Entries. View rebuilds every element
// from the fields on each pass and starts no goroutine, so it is safe to call
// any number of times per frame.
package notes

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/internal/icons"
	"github.com/laix/quire/internal/widgets"
	"github.com/laix/quire/locales"
)

// Entry is one note as the panel shows it.
type Entry struct {
	// ID is the store's identity for the note. The panel hands it back to
	// OnSave and OnRemove and never parses it.
	ID string
	// Subtitle is the chapter and the place, already worded: "第一章 · Page 9".
	Subtitle string
	// Text is the passage the note was made from, as the reader selected it.
	Text string
	// Comment is the reader's own words, empty for a bare highlight.
	Comment string
	// From and To are the ends of the passage in the current layout, and
	// HasLoc reports whether both could be placed. A note the layout cannot
	// place is drawn muted and does not jump; it can still be edited or
	// removed.
	From, To render.Location
	HasLoc   bool
	// Current marks the note whose passage starts at the reading position, so
	// the panel shows which marks are around the reader.
	Current bool
}

// Panel is the notes panel's state.
type Panel struct {
	Entries  []Entry
	Selected int // the row whose comment the editor is editing, -1 for none
	// Draft is the comment being edited. It is the panel's own field rather
	// than the row's, so a rebuild (a page turn, a save) never eats what the
	// reader has typed: the editor binds to it and OnSave publishes it.
	Draft string

	// OnJump runs with a clicked row. The host turns From into a page or a
	// scroll offset, whichever its layout uses.
	OnJump func(e Entry)
	// OnSave runs when the reader saves the editor, with the comment trimmed.
	OnSave func(e Entry, comment string)
	// OnRemove runs when the reader deletes the chosen note.
	OnRemove func(e Entry)

	// List keeps the panel's scroll position and its rows' identity. Keying it
	// by note ID follows a row when notes above it are removed.
	List ui.ListState
}

// SelectedID returns the ID of the selected row, or "" when none is selected.
func (p *Panel) SelectedID() string {
	if p.Selected < 0 || p.Selected >= len(p.Entries) {
		return ""
	}
	return p.Entries[p.Selected].ID
}

// Select chooses the row with id and loads its comment into the editor,
// replacing any draft. It reports whether the row exists.
func (p *Panel) Select(id string) bool {
	for i, e := range p.Entries {
		if e.ID != id {
			continue
		}
		p.Selected = i
		p.Draft = e.Comment
		return true
	}
	return false
}

// ScrollToCurrent scrolls the list so that the note at the reading position
// shows. It is meant for the frame that opens the panel: once the reader
// scrolls, the list keeps its own place.
func (p *Panel) ScrollToCurrent() bool {
	for i, e := range p.Entries {
		if e.Current {
			p.List.ScrollTo(i, ui.Center)
			return true
		}
	}
	return false
}

// View draws the panel: a line with the count, one row per note, and — while a
// row is chosen — the editor for its comment. It must be called on the UI
// thread, inside a build pass, and may be called any number of times: every
// frame is rebuilt from the panel's fields.
func (p *Panel) View(c *ui.Context) ui.Element {
	t := c.Theme()
	if p.Selected >= len(p.Entries) {
		p.Selected = -1
	}
	if p.Selected < -1 {
		p.Selected = -1
	}
	if p.Selected < 0 {
		p.Draft = ""
	}

	col := ui.Column(c).Fill()
	col.Children(func() {
		ui.Row(c).Padding(8, 12, 6, 12).Children(func() {
			ui.Text(c, locales.NoteCount(len(p.Entries))).FontSize(11).
				TextColor(t.TextMuted).SingleLine()
		})
		ui.Divider(c)

		if len(p.Entries) == 0 {
			widgets.Empty(c, locales.EmptyNotes(), locales.EmptyNotesHint())
			return
		}
		p.List.Key = func(i int) any {
			if i < 0 || i >= len(p.Entries) {
				return i
			}
			return p.Entries[i].ID
		}
		ui.List(c, &p.List, len(p.Entries), func(i int) {
			p.row(c, t, i)
		}).Grow(1).Padding(2, 6)

		if e, ok := p.selected(); ok {
			ui.Divider(c)
			p.editor(c, e)
		}
	})
	return col
}

// selected returns the chosen row.
func (p *Panel) selected() (Entry, bool) {
	if p.Selected < 0 || p.Selected >= len(p.Entries) {
		return Entry{}, false
	}
	return p.Entries[p.Selected], true
}

// row draws row i: the chapter and place, the passage, and the comment under
// it. A click chooses the row, loads its comment into the editor and jumps to
// the passage.
func (p *Panel) row(c *ui.Context, t *ui.Theme, i int) {
	e := p.Entries[i]
	chosen := i == p.Selected
	row := ui.Row(c).FillWidth().AlignItems(ui.Start).Radius(6).Padding(3, 4)
	switch {
	case e.Current:
		row.Background(t.Accent.Alpha(0.14))
	case chosen:
		row.Background(t.Selection.Alpha(0.5))
	default:
		row.Background(widgets.RowBackground(c, row.Hovered(), false))
	}
	row.Children(func() {
		body := ui.Column(c).Grow(1).MinWidth(0).Gap(3).Padding(3, 4).
			Cursor(ui.CursorPointer)
		if !e.HasLoc {
			body.Cursor(ui.CursorDefault)
		}
		body.OnClick(func() {
			p.choose(i)
			if e.HasLoc && p.OnJump != nil {
				p.OnJump(e)
			}
		})
		body.Children(func() {
			if e.Subtitle != "" {
				ink := t.TextMuted
				if e.Current {
					ink = t.Accent
				}
				ui.Text(c, e.Subtitle).FontSize(10).TextColor(ink).SingleLine()
			}
			quote := strings.TrimSpace(e.Text)
			if quote != "" {
				ui.Text(c, quote).FontSize(12).TextColor(t.Text).MaxLines(3)
			}
			if comment := strings.TrimSpace(e.Comment); comment != "" {
				ui.Text(c, comment).FontSize(11).TextColor(t.TextMuted).MaxLines(2)
			}
		})
	})
}

// choose makes row i the chosen one and loads its comment into the editor.
// Choosing the row already chosen keeps the draft, so a click on the page's
// note does not throw away what the reader has typed.
func (p *Panel) choose(i int) {
	if i < 0 || i >= len(p.Entries) || i == p.Selected {
		return
	}
	p.Selected = i
	p.Draft = p.Entries[i].Comment
}

// editor draws the comment field and the controls of the chosen row. The save
// control is disabled while the draft matches the stored comment, so an
// accidental click cannot rewrite a note with what it already says.
func (p *Panel) editor(c *ui.Context, e Entry) {
	save := strings.TrimSpace(p.Draft) != strings.TrimSpace(e.Comment)
	ui.Column(c).FillWidth().Gap(6).Padding(8, 10).Children(func() {
		ui.TextArea(c, &p.Draft).Label(locales.NoteComment()).
			Placeholder(locales.NoteComment()).Height(76)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if p.OnRemove != nil {
				// OnClick, not Clicked: deleting republishes the panel's rows, and
				// the build pass that shows them has to be over first.
				widgets.IconButton(c, icons.Trash, locales.DeleteNote(), locales.DeleteNote()).
					OnClick(func() { p.OnRemove(e) })
			}
			ui.Spacer(c)
			btn := widgets.SmallButton(c, locales.NoteSave(), locales.NoteSave()).Disabled(!save)
			if p.OnSave != nil {
				// The draft is read when the click is handled, after this pass, and
				// taken as saved only then.
				btn.OnClick(func() {
					comment := strings.TrimSpace(p.Draft)
					p.OnSave(e, comment)
					p.Draft = comment
				})
			}
		})
	})
}

// Dirty reports whether the draft differs from the chosen note's stored
// comment, which is when there is something to save. It is exported for the
// host and the tests.
func (p *Panel) Dirty() bool {
	e, ok := p.selected()
	if !ok {
		return false
	}
	return strings.TrimSpace(p.Draft) != strings.TrimSpace(e.Comment)
}
