// Package bookmarks draws the reader's bookmarks panel: one row per place the
// reader kept, with the chapter it falls in and the page or the progress that
// names it, and the controls that jump to one, add the current place and drop
// a row.
//
// The panel owns only plain data. It never reads the store, never builds a
// layout and starts no goroutine: the host resolves each stored bookmark
// against the current layout, fills Entries, and assigns the callbacks. View
// rebuilds every element from the fields on each pass and keeps nothing but
// the widget state of the list, so it is safe to call any number of times per
// frame.
package bookmarks

import (
	"github.com/egoist/mygo/ui"
	uimobile "github.com/egoist/mygo/ui/mobile"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/internal/icons"
	"github.com/laix/quire/internal/widgets"
	"github.com/laix/quire/locales"
)

// Entry is one bookmark as the panel shows it.
type Entry struct {
	// ID is the store's identity for the row. The panel only hands it back to
	// OnRemove: it never parses it.
	ID string
	// Title is what names the row: the reader's own label when the bookmark
	// has one, otherwise the chapter it falls in.
	Title string
	// Detail is where it is, already worded: "Page 12" on paper, "43% of the
	// book" in a scrolling column, empty when the layout could not tell.
	Detail string
	// Loc is where the row jumps to, and HasLoc reports whether the current
	// layout could resolve it. A row the layout cannot place is drawn muted
	// and does not jump; its delete control still works.
	Loc    render.Location
	HasLoc bool
	// Current marks the row standing at the reading position, so the panel
	// shows where in the book the reader is now.
	Current bool
}

// Panel is the bookmarks panel's state.
type Panel struct {
	Entries  []Entry
	Selected int // the row last clicked, -1 for none

	// OnJump runs with the row the reader clicked. The host turns Loc into a
	// page or a scroll offset, whichever its layout uses.
	OnJump func(e Entry)
	// OnRemove runs when the reader presses a row's delete control.
	OnRemove func(e Entry)
	// OnAdd runs when the reader presses the panel's add control. A nil OnAdd
	// hides the control: the panel never adds anything itself.
	OnAdd func()

	// List keeps the panel's scroll position and its rows' identity. Keying it
	// by bookmark ID follows a row when bookmarks above it are removed.
	List ui.ListState

	// Touch gives the rows a finger's height and makes the delete control a
	// swipe rather than a pointer's hover: a phone has no pointer to rest on a
	// row, and its long press marks text rather than opening a menu.
	Touch bool
}

// SelectedID returns the ID of the selected row, or "" when none is selected.
func (p *Panel) SelectedID() string {
	if p.Selected < 0 || p.Selected >= len(p.Entries) {
		return ""
	}
	return p.Entries[p.Selected].ID
}

// ScrollToCurrent scrolls the list so that the bookmark at the reading
// position shows. It is meant for the frame that opens the panel: once the
// reader scrolls, the list keeps its own place.
func (p *Panel) ScrollToCurrent() bool {
	for i, e := range p.Entries {
		if e.Current {
			p.List.ScrollTo(i, ui.Center)
			return true
		}
	}
	return false
}

// View draws the panel: a line with the bookmark count and the add control, a
// divider, and one row per bookmark. It must be called on the UI thread,
// inside a build pass, and may be called any number of times: every frame is
// rebuilt from the panel's fields.
func (p *Panel) View(c *ui.Context) ui.Element {
	t := c.Theme()
	if p.Selected >= len(p.Entries) {
		p.Selected = -1
	}
	if p.Selected < -1 {
		p.Selected = -1
	}

	col := ui.Column(c).Fill()
	col.Children(func() {
		ui.Row(c).Padding(8, 12, 6, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.BookmarkCount(len(p.Entries))).FontSize(11).
				TextColor(t.TextMuted).Grow(1).SingleLine()
			if p.OnAdd != nil {
				// OnClick, not Clicked: a bookmark added while the list below is
				// being built would grow the rows the list is counting.
				widgets.IconButton(c, icons.Plus, locales.AddBookmark(), locales.AddBookmark()).
					Disabled(len(p.Entries) > 0 && p.current()).
					OnClick(p.OnAdd)
			}
		})
		ui.Divider(c)

		if len(p.Entries) == 0 {
			widgets.Empty(c, locales.EmptyBookmarks(), locales.EmptyBookmarksHint())
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
	})
	return col
}

// current reports whether a row already stands at the reading position, which
// is when there is nothing for the add control to add.
func (p *Panel) current() bool {
	for _, e := range p.Entries {
		if e.Current {
			return true
		}
	}
	return false
}

// row draws row i: the ribbon, the title and the place. A phone swipes the row
// aside to reveal its delete action; a pointer rests on it and gets the same
// control beside the row. Either way a click on the row itself jumps, so one
// gesture never does both.
func (p *Panel) row(c *ui.Context, t *ui.Theme, i int) {
	e := p.Entries[i]
	build := func() { p.rowContent(c, t, i) }
	if p.Touch && p.OnRemove != nil {
		uimobile.SwipeRow(c, nil, uimobile.SwipeOptions{
			Trailing: []uimobile.SwipeAction{{
				Label:       locales.DeleteBookmark(),
				Destructive: true,
				OnTap:       func() { p.OnRemove(e) },
			}},
		}, build)
		return
	}
	build()
}

// rowContent is the row itself, in the row builder the swipe wraps.
func (p *Panel) rowContent(c *ui.Context, t *ui.Theme, i int) {
	e := p.Entries[i]
	chosen := i == p.Selected
	height := float32(46)
	if p.Touch {
		height = 56
	}
	row := ui.Row(c).FillWidth().Height(height).AlignItems(ui.Center).Gap(2).
		Radius(6).Padding(0, 4)
	switch {
	case e.Current:
		row.Background(t.Accent.Alpha(0.14))
	case chosen:
		row.Background(t.Selection.Alpha(0.5))
	default:
		row.Background(widgets.RowBackground(c, row.Hovered(), false))
	}

	row.Children(func() {
		body := ui.Row(c).Grow(1).FillHeight().AlignItems(ui.Center).Gap(8).
			Padding(0, 4).Cursor(ui.CursorPointer)
		if !e.HasLoc {
			body.Cursor(ui.CursorDefault)
		}
		body.OnClick(func() {
			p.Selected = i
			if e.HasLoc && p.OnJump != nil {
				p.OnJump(e)
			}
		})
		body.Children(func() {
			ink := t.TextMuted
			if e.Current {
				ink = t.Accent
			}
			ui.Icon(c, icons.Bookmark).Size(13, 13).TextColor(ink)
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				text := t.Text
				weight := 500
				switch {
				case !e.HasLoc:
					text = t.TextMuted
					weight = 400
				case e.Current:
					text = t.Accent
				}
				ui.Text(c, e.Title).FontSize(12).FontWeight(weight).TextColor(text).
					SingleLine()
				if e.Detail != "" {
					ui.Text(c, e.Detail).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
			})
		})

		if p.OnRemove != nil && !p.Touch && (row.Hovered() || chosen) {
			// OnClick, not Clicked: the delete runs after the build, so the row
			// it takes out of Entries is not one the list is still building.
			widgets.IconButton(c, icons.Trash, locales.DeleteBookmark(), locales.DeleteBookmark()).
				OnClick(func() { p.OnRemove(e) })
		}
	})
}
