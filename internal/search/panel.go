package search

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/widgets"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// Panel is the reader's search panel. It owns only plain Go state and rebuilds its
// view every frame from it; callbacks are invoked on the UI thread and the host
// starts goroutines for indexing and searching.
type Panel struct {
	Query    string
	Hits     []Hit
	Selected int
	Status   string

	// OnSearch is called after the query field changes or Enter is pressed.
	// The host runs the search in a goroutine and publishes Hits back through
	// the window.
	OnSearch func(query string)
	// OnJump is called with the location of a clicked hit. The host turns it
	// into a page or a scroll offset, depending on the layout's mode.
	OnJump func(loc render.Location)
	// PageOf maps a hit to a page number. When nil, or when the layout has no
	// pages (a scroll layout), rows show no page.
	PageOf func(h Hit) (int, bool)

	list ui.ListState
}

// View draws the search field, a status line, and one row per hit.
func (p *Panel) View(c *ui.Context) ui.Element {
	t := c.Theme()
	if p.Selected >= len(p.Hits) {
		p.Selected = len(p.Hits) - 1
	}
	if p.Selected < -1 {
		p.Selected = -1
	}

	search := false
	col := ui.Column(c).Fill()
	col.Children(func() {
		ui.Row(c).Padding(8, 10).Children(func() {
			field := ui.SearchField(c, &p.Query).Label(locales.SearchFieldLabel()).Grow(1)
			if field.Changed() || field.Submitted() {
				search = true
			}
		})

		if line := p.statusText(); line != "" {
			ui.Row(c).Padding(0, 12, 6, 12).Children(func() {
				ui.Text(c, line).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		}
		ui.Divider(c)

		switch {
		case strings.TrimSpace(p.Query) == "":
			widgets.Empty(c, locales.SearchEmptyTitle(), locales.SearchEmptyHint())
		case len(p.Hits) == 0:
			widgets.Empty(c, p.statusText(), locales.SearchNoHitsHint())
		default:
			p.list.Key = func(i int) any { return i }
			ui.List(c, &p.list, len(p.Hits), func(i int) { p.row(c, i) }).Grow(1).Padding(2, 6)
		}
	})

	if search && p.OnSearch != nil {
		p.OnSearch(p.Query)
	}
	return col
}

// statusText is the line under the field: the host's status, or the count of
// hits, or what to do next.
func (p *Panel) statusText() string {
	if p.Status != "" {
		return p.Status
	}
	switch {
	case strings.TrimSpace(p.Query) == "":
		return ""
	case len(p.Hits) == 0:
		return locales.SearchNotFoundShort()
	default:
		return locales.SearchResults(len(p.Hits))
	}
}

// row draws one hit: the chapter and its page, then the snippet with the match
// marked. The row darkens as the pointer rests on it, and a click jumps there.
func (p *Panel) row(c *ui.Context, i int) {
	h := p.Hits[i]
	t := c.Theme()

	label := strings.TrimSpace(h.Title)
	if label == "" {
		label = locales.SearchChapter(h.Chapter + 1)
	}
	page, hasPage := 0, false
	if p.PageOf != nil {
		page, hasPage = p.PageOf(h)
	}

	chosen := i == p.Selected
	row := ui.Column(c).FillWidth().Gap(2).Padding(6, 8).Radius(6)
	row.Background(widgets.RowBackground(c, row.Hovered(), chosen))
	row.OnClick(func() {
		p.Selected = i
		if p.OnJump != nil {
			p.OnJump(h.Loc)
		}
	})
	row.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(500).Grow(1).SingleLine()
			if hasPage {
				ui.Text(c, locales.SearchPage(page+1)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		if snippet := strings.TrimSpace(h.Snippet); snippet != "" {
			ui.Text(c, snippet).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
		}
	})
}
