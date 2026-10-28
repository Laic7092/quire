package search

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/widgets"
	"github.com/laix/quire/locales"
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
	// shownQuery is the query the list was last positioned for. A new one
	// starts the list at the top again: the rows are a different set, so the
	// old scroll position names nothing in them.
	shownQuery string
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

		// The field has written what the reader typed back to Query, so the rest
		// of the pass reads this frame's query, not the previous one.
		p.resetScroll()
		empty := strings.TrimSpace(p.Query) == ""
		noHits := !empty && len(p.Hits) == 0
		// A query with no hits words its status as the empty state's title, so
		// the line under the field would say the same thing twice.
		if line := p.statusText(); line != "" && !noHits {
			ui.Row(c).Padding(0, 12, 6, 12).Children(func() {
				ui.Text(c, line).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		}
		ui.Divider(c)

		switch {
		case empty:
			widgets.Empty(c, locales.SearchEmptyTitle(), locales.SearchEmptyHint())
		case noHits:
			widgets.Empty(c, p.statusText(), locales.SearchNoHitsHint())
		default:
			p.list.Key = func(i int) any {
				if i < 0 || i >= len(p.Hits) {
					return i
				}
				// A row belongs to its hit, not to its place in the list, so a
				// re-run of the same query keeps the reader's place among results
				// that are still there.
				h := p.Hits[i]
				return [3]int{h.Chapter, int(h.Loc.Node), h.Loc.Offset}
			}
			ui.List(c, &p.list, len(p.Hits), func(i int) { p.row(c, i) }).Grow(1).Padding(2, 6)
		}
	})

	if search && p.OnSearch != nil {
		p.OnSearch(p.Query)
	}
	return col
}

// resetScroll starts the list at the top when the query changed: the hits of
// the previous query mean nothing to the new one, and the list would otherwise
// keep the old scroll offset and open the new results in the middle.
func (p *Panel) resetScroll() {
	q := strings.TrimSpace(p.Query)
	if q == p.shownQuery {
		return
	}
	p.shownQuery = q
	if q != "" {
		p.list.ScrollTo(0, ui.Start)
	}
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
