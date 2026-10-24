package library

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/icons"
	"github.com/laix/layx-reader/internal/widgets"
	"github.com/laix/layx-reader/locales"
)

// Panel draws the shelf as a sidebar: a field that filters the books, the
// buttons that list the shelf again and import a folder, and one row per book
// with its cover, title and details.
//
// Its state is plain data owned by the caller, and View never touches the disk:
// the caller decides what the shelf lists and describes each file off the UI
// thread. The panel keeps only its own ListState between frames and rebuilds
// every element from the model on each pass.
type Panel struct {
	Lib      *Library
	Query    string
	Selected int
	// Status is a line under the field, such as the error of the last reload.
	// While it is empty the panel shows how many books it has.
	Status string
	// Cover returns the decoded cover of a book, or nil to show a placeholder.
	// The caller caches the bitmaps: the panel never decodes one per frame.
	Cover func(entry Entry) *ui.Bitmap
	// OnOpen runs with the path of the book the user clicked.
	OnOpen func(path string)
	// OnReload runs when the user clicks the refresh button. List the shelf
	// again elsewhere (a goroutine) and set Status when it fails.
	OnReload func()
	// OnPickDir runs when the user asks to import another folder of books.
	OnPickDir func()

	list ui.ListState
}

// View builds the shelf panel from the current Library and Query.
func (p *Panel) View(c *ui.Context) ui.Element {
	t := c.Theme()
	rows := p.filtered()
	if p.Selected >= len(rows) {
		p.Selected = -1
	}
	if p.Selected < -1 {
		p.Selected = -1
	}
	// Keying rows by path keeps the list's place when the filter changes the
	// rows under it.
	p.list.Key = func(i int) any {
		if i < 0 || i >= len(rows) {
			return i
		}
		return rows[i].Path
	}

	col := ui.Column(c).Fill()
	col.Children(func() {
		ui.Row(c).Gap(6).Padding(8, 10).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c, &p.Query).Label(locales.ShelfSearchLabel()).Grow(1)
			if p.OnReload != nil {
				if widgets.IconButton(c, icons.Refresh, locales.ShelfRefresh(), locales.ShelfRefreshTip()).Clicked() {
					p.OnReload()
				}
			}
			if p.OnPickDir != nil {
				if widgets.IconButton(c, icons.Folder, locales.ChooseFolder(), locales.ShelfChooseFolderTip()).Clicked() {
					p.OnPickDir()
				}
			}
		})

		ui.Row(c).Padding(0, 12, 6, 12).Children(func() {
			line := p.countLabel(len(rows))
			if p.Status != "" {
				line = p.Status
			}
			ui.Text(c, line).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Divider(c)

		switch {
		case p.total() == 0:
			widgets.Empty(c, locales.ShelfEmpty(), locales.ShelfEmptyHint())
		case len(rows) == 0:
			widgets.Empty(c, locales.ShelfNoMatch(strings.TrimSpace(p.Query)), locales.ShelfNoMatchHint())
		default:
			ui.List(c, &p.list, len(rows), func(i int) {
				p.row(c, t, rows[i], i)
			}).Grow(1).Padding(2, 6)
		}
	})
	return col
}

// filtered returns the entries the current query matches, without reading the
// disk.
func (p *Panel) filtered() []Entry {
	if p.Lib == nil {
		return nil
	}
	return p.Lib.Filter(p.Query)
}

func (p *Panel) countLabel(matched int) string {
	total := p.total()
	if total == 0 {
		return ""
	}
	if strings.TrimSpace(p.Query) == "" {
		return locales.ShelfCount(matched)
	}
	return locales.ShelfCountFiltered(matched, total)
}

// total is how many books the shelf holds; a nil Library holds none.
func (p *Panel) total() int {
	if p.Lib == nil {
		return 0
	}
	return len(p.Lib.Entries)
}

// row draws one book: its cover or a placeholder, its title and a line of
// details. The row darkens as the pointer rests on it, and a click opens the
// book.
func (p *Panel) row(c *ui.Context, t *ui.Theme, entry Entry, i int) {
	chosen := i == p.Selected
	row := ui.Row(c).FillWidth().Gap(10).Padding(6, 8).Radius(6).AlignItems(ui.Center)
	row.Background(widgets.RowBackground(c, row.Hovered(), chosen))
	row.OnClick(func() {
		p.Selected = i
		if p.OnOpen != nil {
			p.OnOpen(entry.Path)
		}
	})
	row.Children(func() {
		cover := (*ui.Bitmap)(nil)
		if p.Cover != nil {
			cover = p.Cover(entry)
		}
		if cover != nil {
			ui.Image(c, cover).Size(36, 50).Fit(ui.Cover).Radius(3)
		} else {
			ui.Box(c).Size(36, 50).Radius(3).Background(t.SurfacePressed).Center().Children(func() {
				ui.Icon(c, icons.Book).Size(16, 16).TextColor(t.TextMuted)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, entry.Title).FontSize(13).FontWeight(500).SingleLine()
			ui.Text(c, detailLabel(entry)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
	})
}

// detailLabel is "Author · EPUB · 1.2 MB", leaving out the author when the book
// has none.
func detailLabel(entry Entry) string {
	parts := make([]string, 0, 3)
	if entry.Author != "" {
		parts = append(parts, entry.Author)
	}
	parts = append(parts, strings.ToUpper(entry.Format), humanSize(entry.Size))
	return strings.Join(parts, " · ")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
