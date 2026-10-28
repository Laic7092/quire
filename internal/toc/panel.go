package toc

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/widgets"
	"github.com/laix/quire/locales"
)

// indent is how far one level indents a row, in DIPs.
const indent = 14

// maxIndentLevels caps the indentation a row can ask for. A navigation document
// with a pathologically deep tree would otherwise push its titles out of the
// panel; Entry.Level keeps the real depth either way.
const maxIndentLevels = 6

// Panel draws the table of contents: one row per chapter or per navigation
// target, indented by level, with the page the row points at and the row being
// read marked.
//
// The panel owns no goroutine, reads no environment and never touches a Window:
// the host builds Entries with Build, assigns OnJump, and rebuilds Entries after
// a relayout. View redraws from the fields alone, so the panel is safe to keep
// in the app's state.
type Panel struct {
	Entries  []Entry
	Selected int // the row under the pointer last clicked, -1 for none
	// OnJump is called with a clicked row. The row carries the location to
	// jump to (Loc/HasLoc), which is what the host hands to Layout.OffsetOf or
	// PageOf: only it knows which mode the layout is in.
	OnJump func(e Entry)

	// List keeps the scroll position. Call ScrollToCurrent after rebuilding
	// Entries to open the panel at the chapter being read.
	List ui.ListState
}

// SelectCurrent marks the row of the chapter being read as chosen and reports
// whether there is one.
func (p *Panel) SelectCurrent() bool {
	for i, e := range p.Entries {
		if e.Current {
			p.Selected = i
			return true
		}
	}
	return false
}

// ScrollToCurrent scrolls the list so that the chapter being read shows. It is
// meant for the frame that opens the panel: once the user scrolls, the list
// keeps its own place.
func (p *Panel) ScrollToCurrent() bool {
	for i, e := range p.Entries {
		if e.Current {
			p.List.ScrollTo(i, ui.Center)
			return true
		}
	}
	return false
}

// View draws the panel. It must be called on the UI thread, inside a build pass,
// and may be called any number of times: every frame is rebuilt from the
// panel's fields.
func (p *Panel) View(c *ui.Context) ui.Element {
	t := c.Theme()
	if len(p.Entries) == 0 {
		return widgets.Empty(c, locales.TocEmpty(), locales.TocEmptyHint())
	}
	if p.Selected >= len(p.Entries) {
		p.Selected = -1
	}
	if p.Selected < -1 {
		p.Selected = -1
	}
	p.List.Key = func(i int) any {
		if i < 0 || i >= len(p.Entries) {
			return i
		}
		// One chapter can own several rows, so the chapter alone is not an
		// identity: the anchor is what tells two rows of it apart.
		return [2]int{p.Entries[i].Chapter, int(p.Entries[i].Anchor)}
	}

	col := ui.Column(c).Fill()
	col.Children(func() {
		ui.Row(c).Padding(8, 12, 6, 12).Children(func() {
			ui.Text(c, locales.TocCount(len(p.Entries))).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Divider(c)
		ui.List(c, &p.List, len(p.Entries), func(i int) {
			p.row(c, t, i)
		}).Grow(1).Padding(2, 6)
	})
	return col
}

// row draws row i: a level indent, the title and the page. The row darkens as
// the pointer rests on it, and a click jumps to what it names.
func (p *Panel) row(c *ui.Context, t *ui.Theme, i int) {
	e := p.Entries[i]
	// A row jumps when the layout resolved its target. Without a layout only
	// a remembered page is something to jump to.
	disabled := !e.HasLoc && e.Page < 0

	row := ui.Row(c).FillWidth().Height(30).AlignItems(ui.Center).Gap(6).Radius(6).
		Padding(0, 8, 0, 8+indentOf(e))
	switch {
	case e.Current:
		row.Background(t.Accent.Alpha(0.14))
	case i == p.Selected:
		row.Background(t.Selection.Alpha(0.5))
	default:
		row.Background(widgets.RowBackground(c, row.Hovered(), false))
	}
	row.Disabled(disabled)
	row.OnClick(func() {
		p.Selected = i
		p.jump(i)
	})
	row.Children(func() {
		if e.Current {
			ui.Box(c).Width(3).Height(14).Radius(1.5).Background(t.Accent)
		}
		text := t.Text
		switch {
		case disabled:
			text = t.TextMuted
		case e.Current:
			text = t.Accent
		case e.Group:
			text = t.TextMuted
		}
		weight := 400
		if e.Group || e.Current {
			weight = 500
		}
		ui.Text(c, titleOf(e)).FontSize(12).FontWeight(weight).Grow(1).SingleLine().TextColor(text)
		if e.Page >= 0 {
			ui.Text(c, pageLabel(e)).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// jump reports a click on row i to the host, unless the row has nowhere to go.
func (p *Panel) jump(i int) {
	if i < 0 || i >= len(p.Entries) || p.OnJump == nil {
		return
	}
	e := p.Entries[i]
	if !e.HasLoc && e.Page < 0 {
		return
	}
	p.OnJump(e)
}

// indentOf is the left indent of a row's title, in DIPs.
func indentOf(e Entry) float32 {
	level := e.Level
	if level < 0 {
		level = 0
	}
	if level > maxIndentLevels {
		level = maxIndentLevels
	}
	return float32(level) * indent
}

// titleOf is the text of a row. A title the book left empty still names its
// chapter, so the row is never a blank line the reader cannot place. Any other
// title is drawn as the book wrote it; Build does not rewrite them.
func titleOf(e Entry) string {
	if strings.TrimSpace(e.Title) != "" {
		return e.Title
	}
	return locales.ChapterNumber(e.Chapter + 1)
}

// pageLabel is the page text of a row: the one page it starts on, or the range
// it spans. Entry carries only the first page and the count, so a
// "current page / pages" position has no place here.
func pageLabel(e Entry) string {
	if e.Pages <= 1 {
		return strconv.Itoa(e.Page + 1)
	}
	// first and last page, both 1-based, separated by an en dash.
	return strconv.Itoa(e.Page+1) + "–" + strconv.Itoa(e.Page+e.Pages)
}
