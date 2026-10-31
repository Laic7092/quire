package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/acquire"
	"github.com/laix/quire/internal/icons"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/source"
	"github.com/laix/quire/internal/sourceconf"
	"github.com/laix/quire/locales"
)

// sourceSheet is the book-source search and download dialog. It is owned by
// *app and built by sourceDialog at the end of the view.
type sourceSheet struct {
	shown bool

	query  string
	filter source.ID // "" means all sources

	results []sourceRow
	list    ui.ListState

	searching bool
	searchID  int
	searchCtx context.Context
	// searchCancel stops the search in flight; it is nil while no search runs.
	searchCancel context.CancelFunc

	statuses map[source.ID]sourceStatus

	// Download state.
	downloading bool
	dlBook      source.Book
	dlProgress  source.Progress
	dlCancel    context.CancelFunc
	dlSaved     acquire.Saved
	dlErr       error

	// Settings state.
	settingsOpen  bool
	settingsFor   source.ID
	settingFields []sourceSettingField
}

type sourceSettingField struct {
	key   string
	value string
}

type sourceRow struct {
	book    source.Book
	elapsed time.Duration
	err     error
}

type sourceStatus struct {
	err     error
	elapsed time.Duration
}

const (
	sheetWidth  = 720
	sheetHeight = 560
)

// knownSourceIDs lists the sources the UI knows about, in sidebar order.
var knownSourceIDs = []source.ID{"zlib", "legado", "wenku8"}

// sourceName returns the human name of id: the registered source's Name() if
// available, otherwise a localized fallback.
func sourceName(a *app, id source.ID) string {
	if a.sources == nil {
		return fallbackSourceName(id)
	}
	if s, ok := a.sources.Get(id); ok {
		return s.Name()
	}
	return fallbackSourceName(id)
}

func fallbackSourceName(id source.ID) string {
	switch id {
	case "zlib":
		return locales.SourceZLibrary()
	case "legado":
		return locales.SourceLegado()
	case "wenku8":
		return locales.SourceWenku8()
	}
	return string(id)
}

// sourceSheetIcon returns the sidebar icon for a source.
func sourceSheetIcon(id source.ID) *ui.SVG {
	switch id {
	case "legado":
		return icons.BookOpen
	case "wenku8":
		return icons.Book
	}
	return icons.Globe
}

// sourceDialog builds the source search sheet over whatever view is showing.
func (a *app) sourceDialog(c *ui.Context, t *ui.Theme) {
	if a.find == nil || !a.find.shown {
		return
	}
	pal := a.chrome
	bg := reader.UIColor(pal.Popover)
	text := reader.UIColor(pal.Text)
	muted := reader.UIColor(pal.Muted)
	onToolbar := reader.UIColor(pal.OnToolbar)

	ui.Modal(c, &a.find.shown, func() {
		card := ui.Box(c).Width(sheetWidth).Height(sheetHeight).Radius(14).
			Background(bg).Border(1, reader.UIColor(pal.Line)).
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.32)).Clip()
		card.Children(func() {
			a.sourceSheetHeader(c, t, text, muted, onToolbar)
			a.sourceSheetFilter(c, t, text, muted, onToolbar)
			a.sourceSheetStatus(c, t, muted)
			a.sourceSheetResults(c, t, text, muted, onToolbar)
		})
	})
	if !a.find.shown {
		a.find.closeSheet()
	}
}

func (a *app) sourceSheetHeader(c *ui.Context, t *ui.Theme, text, muted, onToolbar ui.Color) {
	pal := a.chrome
	ui.Row(c).FillWidth().Height(54).Padding(0, 16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.SourceSheetTitle()).FontSize(15).FontWeight(700).TextColor(text).Grow(1)
		closeBtn := ui.Box(c).Width(28).Height(28).Radius(14).Center().
			Cursor(ui.CursorPointer).Label(locales.SourceSheetDone())
		if closeBtn.Hovered() {
			closeBtn.Background(t.SurfaceHover)
		}
		closeBtn.OnClick(func() { a.find.shown = false })
		closeBtn.Children(func() {
			ui.Icon(c, icons.Close).Size(16, 16).TextColor(muted)
		})
	})

	ui.Row(c).FillWidth().Height(38).Padding(0, 16, 10, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		field := ui.SearchField(c, &a.find.query).Placeholder(locales.SourceSheetSearchPlaceholder()).FillWidth().Label(locales.Search())
		if field.Submitted() {
			a.find.startSearch(a)
		}
		searchBtn := ui.Box(c).Height(32).Padding(0, 14).Radius(8).Center().
			Background(reader.UIColor(pal.Accent)).Cursor(ui.CursorPointer).
			Label(locales.Search())
		searchBtn.OnClick(func() { a.find.startSearch(a) })
		searchBtn.Children(func() {
			ui.Text(c, locales.Search()).FontSize(13).FontWeight(600).TextColor(reader.UIColor(pal.OnAccent))
		})
	})
}

func (a *app) sourceSheetFilter(c *ui.Context, t *ui.Theme, text, muted, onToolbar ui.Color) {
	pal := a.chrome
	sources := a.registeredSourceIDs()
	labels := make([]string, 0, len(sources)+1)
	ids := make([]source.ID, 0, len(sources)+1)
	labels = append(labels, locales.SourceSheetAll())
	ids = append(ids, "")
	for _, id := range sources {
		labels = append(labels, sourceName(a, id))
		ids = append(ids, id)
	}

	ui.ScrollHorizontal(c).FillWidth().Height(38).Padding(0, 12).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			for i, id := range ids {
				chosen := a.find.filter == id
				chip := ui.Box(c).Height(28).Padding(0, 12).Radius(14).Center().
					Cursor(ui.CursorPointer).Label(labels[i])
				ink := muted
				if chosen {
					chip.Background(reader.UIColor(pal.Text))
					ink = onToolbar
				} else if chip.Hovered() {
					chip.Background(t.SurfaceHover)
				}
				chip.OnClick(func() { a.find.filter = id })
				chip.Children(func() {
					ui.Text(c, labels[i]).FontSize(12).FontWeight(600).TextColor(ink)
				})
				// A settings gear for configurable or importable sources.
				if id != "" && a.sources != nil {
					_, cfg := a.sources.Configurable(id)
					_, imp := a.sources.Importer(id)
					if cfg || imp {
						gear := ui.Box(c).Width(22).Height(22).Radius(11).Center().
							Cursor(ui.CursorPointer).Label(locales.SourceSheetSettings())
						if gear.Hovered() {
							gear.Background(t.SurfaceHover)
						}
						gid := id
						gear.OnClick(func() { a.showSourceSettings(gid) })
						gear.Children(func() {
							ui.Icon(c, icons.Gear).Size(12, 12).TextColor(ink)
						})
					}
				}
			}
		})
	})
}

func (a *app) sourceSheetStatus(c *ui.Context, t *ui.Theme, muted ui.Color) {
	f := a.find
	if !f.searching && len(f.statuses) == 0 && len(f.results) == 0 {
		return
	}
	ui.Box(c).FillWidth().Height(1).Background(muted.Alpha(0.15))
	ui.Row(c).FillWidth().Height(28).Padding(0, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		if f.searching {
			ui.Spinner(c).Size(14, 14)
			ui.Text(c, locales.SourceSheetSearching()).FontSize(11).TextColor(muted)
		}
		ui.Spacer(c)
		// Per-source status chips.
		for _, id := range a.registeredSourceIDs() {
			st, ok := f.statuses[id]
			if !ok && f.filter != "" && f.filter != id {
				continue
			}
			if !ok {
				continue
			}
			name := sourceName(a, id)
			var msg string
			switch {
			case st.err != nil:
				if errors.Is(st.err, source.ErrAuthRequired) {
					msg = locales.SourceSheetNeedsLogin(name)
				} else {
					msg = locales.SourceSheetSourceError(name, st.err.Error())
				}
			case st.elapsed > 0:
				msg = fmt.Sprintf("%s · %s", name, locales.SourceSheetElapsed(int(st.elapsed.Milliseconds())))
			default:
				msg = name
			}
			ui.Text(c, msg).FontSize(10).TextColor(muted).SingleLine()
		}
	})
	ui.Box(c).FillWidth().Height(1).Background(muted.Alpha(0.15))
}

func (a *app) sourceSheetResults(c *ui.Context, t *ui.Theme, text, muted, onToolbar ui.Color) {
	f := a.find
	rows := f.filteredResults()

	if len(rows) == 0 {
		ui.Column(c).Grow(1).Center().Padding(32).Gap(10).Children(func() {
			if f.searching {
				ui.Spinner(c).Size(24, 24)
			} else {
				ui.Icon(c, icons.Search).Size(36, 36).TextColor(muted.Alpha(0.5))
			}
			msg := locales.SourceSheetEmpty()
			switch {
			case f.filter != "" && a.sourceNotConfigured(f.filter):
				msg = locales.SourceSheetNotConfigured(sourceName(a, f.filter))
			case strings.TrimSpace(f.query) != "" && !f.searching:
				msg = locales.SourceSheetNoResults()
			}
			ui.Text(c, msg).FontSize(13).TextColor(muted).TextAlign(ui.Center)
		})
		return
	}

	if f.downloading {
		a.sourceDownloadBar(c, t, text, muted)
	}
	if !f.downloading && f.dlSaved.Path != "" {
		a.sourceDownloadDoneBar(c, t, text, muted)
	}

	list := ui.List(c, &f.list, len(rows)).Grow(1).Padding(0, 8).
		ItemKey(func(i int) any {
			return string(rows[i].book.Source) + "\x00" + rows[i].book.Key
		})
	list.Rows(func(row ui.ListRow) {
		a.sourceResultRow(c, row, rows[row.Index], text, muted, onToolbar)
	})
	if list.Submitted() && f.list.Selected != nil && *f.list.Selected >= 0 && *f.list.Selected < len(rows) {
		f.download(a, rows[*f.list.Selected].book)
	}
}

func (a *app) sourceResultRow(c *ui.Context, row ui.ListRow, r sourceRow, text, muted, onToolbar ui.Color) {
	pal := a.chrome
	t := c.Theme()
	book := r.book

	selected := row.Selected()
	item := ui.Box(c).FillWidth().Padding(8, 12).Radius(8).
		Cursor(ui.CursorPointer).Label(book.Title)
	if selected {
		item.Background(reader.UIColor(pal.Pressed))
	} else if item.Hovered() {
		item.Background(t.SurfaceHover)
	}
	if item.RowDoubleClicked() {
		a.find.download(a, book)
	}

	item.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			// Cover placeholder: first letter of the title.
			cover := ui.Box(c).Width(44).Height(64).Radius(4).Background(t.Surface).Center()
			cover.Border(1, t.Border.Alpha(0.4))
			cover.Children(func() {
				label := "?"
				for _, ch := range book.Title {
					label = string(ch)
					break
				}
				ui.Text(c, label).FontSize(18).FontWeight(600).TextColor(muted)
			})

			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, book.Title).FontSize(14).FontWeight(600).TextColor(text).SingleLine()
				if book.Author != "" {
					ui.Text(c, book.Author).FontSize(12).TextColor(muted).SingleLine()
				}
				meta := sourceMeta(a, book)
				if meta != "" {
					ui.Text(c, meta).FontSize(11).TextColor(muted).SingleLine()
				}
				if book.Description != "" {
					desc := book.Description
					if len([]rune(desc)) > 120 {
						desc = string([]rune(desc)[:120]) + "…"
					}
					ui.Text(c, desc).FontSize(11).TextColor(muted.Alpha(0.85)).
						MaxWidth(sheetWidth - 140).MaxLines(2)
				}
			})

			if !a.find.downloading {
				dl := ui.Box(c).Height(28).Padding(0, 12).Radius(8).Center().
					Background(reader.UIColor(pal.Accent)).Cursor(ui.CursorPointer).
					Label(locales.SourceSheetDownload())
				dl.OnClick(func() { a.find.download(a, book) })
				dl.Children(func() {
					ui.Text(c, locales.SourceSheetDownload()).FontSize(12).FontWeight(600).
						TextColor(reader.UIColor(pal.OnAccent))
				})
			}
		})
	})
}

func (a *app) sourceDownloadBar(c *ui.Context, t *ui.Theme, text, muted ui.Color) {
	pal := a.chrome
	f := a.find
	ui.Column(c).FillWidth().Padding(10, 16).Gap(6).Background(reader.UIColor(pal.Pressed)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, locales.SourceSheetDownloading()).FontSize(13).FontWeight(600).TextColor(text).Grow(1)
			if f.dlProgress.Total > 0 {
				p := float64(f.dlProgress.Done) / float64(f.dlProgress.Total)
				ui.Text(c, fmt.Sprintf("%d%%", int(p*100))).FontSize(11).TextColor(muted)
			}
			cancel := ui.Box(c).Height(26).Padding(0, 10).Radius(6).Center().
				Background(reader.UIColor(pal.Line)).Cursor(ui.CursorPointer).Label(locales.SourceSheetCancel())
			cancel.OnClick(func() { f.cancelDownload() })
			cancel.Children(func() {
				ui.Text(c, locales.SourceSheetCancel()).FontSize(12).FontWeight(600).TextColor(text)
			})
		})
		if f.dlProgress.Total > 0 {
			ui.Progress(c, float64(f.dlProgress.Done)/float64(f.dlProgress.Total)).FillWidth().Height(4)
		} else {
			ui.Progress(c, -1).FillWidth().Height(4)
		}
	})
}

// sourceNotConfigured reports whether id is configurable and has no saved values.
func (a *app) sourceNotConfigured(id source.ID) bool {
	if a.sources == nil || a.srcConf == nil {
		return false
	}
	cfg, ok := a.sources.Configurable(id)
	if !ok {
		return false
	}
	if len(cfg.Settings()) == 0 {
		return false
	}
	saved := a.srcConf.Get(string(id))
	hasValue := false
	for _, s := range cfg.Settings() {
		if saved[s.Key] != "" {
			hasValue = true
			break
		}
	}
	return !hasValue
}

func (a *app) sourceDownloadDoneBar(c *ui.Context, t *ui.Theme, text, muted ui.Color) {
	pal := a.chrome
	ui.Row(c).FillWidth().Height(40).Padding(0, 16).Gap(10).AlignItems(ui.Center).
		Background(reader.UIColor(pal.Pressed)).Children(func() {
		ui.Icon(c, icons.Check).Size(16, 16).TextColor(reader.UIColor(pal.Accent))
		ui.Text(c, locales.SourceSheetDownloaded(a.find.dlSaved.Title)).FontSize(13).FontWeight(600).TextColor(text).Grow(1)
		openBtn := ui.Box(c).Height(26).Padding(0, 10).Radius(6).Center().
			Background(reader.UIColor(pal.Accent)).Cursor(ui.CursorPointer).Label(locales.SourceSheetOpen())
		openBtn.OnClick(func() { a.openPath(a.find.dlSaved.Path) })
		openBtn.Children(func() {
			ui.Text(c, locales.SourceSheetOpen()).FontSize(12).FontWeight(600).TextColor(reader.UIColor(pal.OnAccent))
		})
	})
}

// sourceMeta builds the "source · format · size" line for a result.
func sourceMeta(a *app, book source.Book) string {
	parts := []string{locales.SourceSheetFrom(sourceName(a, book.Source))}
	if book.Format != "" {
		parts = append(parts, strings.ToUpper(book.Format))
	}
	if size := source.SizeLabel(book.Size); size != "" {
		parts = append(parts, size)
	}
	return strings.Join(parts, " · ")
}

// registeredSourceIDs returns the registered source IDs in the order the UI lists them.
func (a *app) registeredSourceIDs() []source.ID {
	if a.sources == nil {
		return nil
	}
	ids := a.sources.IDs()
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// filteredResults returns the current results filtered by find.filter.
func (f *sourceSheet) filteredResults() []sourceRow {
	if f.filter == "" {
		return f.results
	}
	out := make([]sourceRow, 0, len(f.results))
	for _, r := range f.results {
		if r.book.Source == f.filter {
			out = append(out, r)
		}
	}
	return out
}

// openSheet shows the sheet and optionally pre-selects a source filter.
func (f *sourceSheet) openSheet(filter source.ID) {
	f.shown = true
	f.filter = filter
	if f.statuses == nil {
		f.statuses = make(map[source.ID]sourceStatus)
	}
}

// closeSheet clears the transient state when the modal closes.
func (f *sourceSheet) closeSheet() {
	if f.searchCancel != nil {
		f.searchCancel()
		f.searchCancel = nil
	}
	f.cancelDownload()
	f.settingsOpen = false
	f.query = ""
	f.filter = ""
	f.results = nil
	f.statuses = nil
	f.searching = false
	f.dlSaved = acquire.Saved{}
	f.dlErr = nil
}

// startSearch cancels any running search and starts a new one.
func (f *sourceSheet) startSearch(a *app) {
	q := strings.TrimSpace(f.query)
	if q == "" {
		return
	}
	if f.searchCancel != nil {
		f.searchCancel()
		f.searchCancel = nil
	}
	f.searchID++
	id := f.searchID
	f.searching = true
	f.results = nil
	f.statuses = make(map[source.ID]sourceStatus)
	f.dlErr = nil
	f.dlSaved = acquire.Saved{}

	ctx, cancel := context.WithCancel(context.Background())
	f.searchCtx = ctx
	f.searchCancel = cancel

	go func() {
		outcomes := a.sources.Search(ctx, source.Query{Text: q, Page: 1})
		if win := a.showWindow(); win != nil {
			win.Update(func() {
				defer a.refresh(win)
				if id != f.searchID {
					return
				}
				f.applyOutcomes(a, outcomes)
				f.searching = false
				f.searchCancel = nil
			})
		}
	}()
}

// applyOutcomes turns registry outcomes into result rows and per-source status.
func (f *sourceSheet) applyOutcomes(a *app, outcomes []source.Outcome) {
	f.results = nil
	f.statuses = make(map[source.ID]sourceStatus)
	for _, o := range outcomes {
		id := o.Source.ID()
		f.statuses[id] = sourceStatus{err: o.Err, elapsed: o.Elapsed}
		if o.Err != nil {
			continue
		}
		for _, b := range o.Result.Books {
			if strings.TrimSpace(b.Title) == "" {
				continue
			}
			f.results = append(f.results, sourceRow{book: b, elapsed: o.Elapsed})
		}
	}
}

// download starts downloading book from its source.
func (f *sourceSheet) download(a *app, book source.Book) {
	if f.downloading {
		return
	}
	if a.acq == nil {
		return
	}
	src, ok := a.sources.Get(book.Source)
	if !ok {
		return
	}
	f.downloading = true
	f.dlBook = book
	f.dlProgress = source.Progress{}
	f.dlErr = nil
	f.dlSaved = acquire.Saved{}

	ctx, cancel := context.WithCancel(context.Background())
	f.dlCancel = cancel

	progressMu := &sync.Mutex{}
	var lastUpdate time.Time
	throttle := 100 * time.Millisecond

	go func() {
		saved, err := a.acq.Save(ctx, src, book, func(p source.Progress) {
			progressMu.Lock()
			f.dlProgress = p
			now := time.Now()
			shouldUpdate := lastUpdate.IsZero() || now.Sub(lastUpdate) >= throttle
			progressMu.Unlock()
			if !shouldUpdate {
				return
			}
			if win := a.showWindow(); win != nil {
				win.Update(func() {
					progressMu.Lock()
					lastUpdate = time.Now()
					progressMu.Unlock()
					defer a.refresh(win)
				})
			}
		})
		finish := func() {
			f.downloading = false
			f.dlCancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				f.dlErr = err
				a.notice = locales.SourceSheetDownloadFailed(err.Error())
				return
			}
			f.dlSaved = saved
			a.notice = locales.SourceSheetDownloaded(saved.Title)
			if saved.Path != "" {
				if a.pan != nil {
					a.pan.ImportDir(filepath.Dir(saved.Path))
				}
				if a.desk != nil {
					a.desk.refreshShelves(a)
				}
			}
		}
		if win := a.showWindow(); win != nil {
			win.Update(func() {
				defer a.refresh(win)
				finish()
			})
		} else {
			finish()
		}
	}()
}

func (f *sourceSheet) cancelDownload() {
	if f.dlCancel != nil {
		f.dlCancel()
		f.dlCancel = nil
	}
	f.downloading = false
}

// sourceSheetSettings builds the settings panel for the current source.
func (a *app) sourceSheetSettings(c *ui.Context, t *ui.Theme, text, muted, onToolbar ui.Color) {
	if a.find == nil || !a.find.settingsOpen {
		return
	}
	id := a.find.settingsFor
	name := sourceName(a, id)
	pal := a.chrome
	bg := reader.UIColor(pal.Popover)

	ui.Modal(c, &a.find.settingsOpen, func() {
		panel := ui.Box(c).Width(420).Radius(14).Background(bg).
			Border(1, reader.UIColor(pal.Line)).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.32))
		panel.Children(func() {
			ui.Row(c).FillWidth().Height(50).Padding(0, 16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(15).FontWeight(700).TextColor(text).Grow(1)
				closeBtn := ui.Box(c).Width(28).Height(28).Radius(14).Center().
					Cursor(ui.CursorPointer).Label(locales.SourceSheetDone())
				if closeBtn.Hovered() {
					closeBtn.Background(t.SurfaceHover)
				}
				closeBtn.OnClick(func() { a.find.settingsOpen = false })
				closeBtn.Children(func() {
					ui.Icon(c, icons.Close).Size(16, 16).TextColor(muted)
				})
			})

			ui.Box(c).FillWidth().Height(1).Background(muted.Alpha(0.15))

			ui.Column(c).FillWidth().Padding(12, 16, 16, 16).Gap(10).Children(func() {
				a.sourceSettingsForm(c, t, text, muted)
			})
		})
	})
}

func (a *app) sourceSettingsForm(c *ui.Context, t *ui.Theme, text, muted ui.Color) {
	pal := a.chrome
	id := a.find.settingsFor

	// Configurable fields.
	if cfg, ok := a.sources.Configurable(id); ok {
		settings := cfg.Settings()
		if len(settings) == 0 {
			ui.Text(c, locales.SourceSheetNotConfigured(sourceName(a, id))).FontSize(13).TextColor(muted)
		} else {
			for i := range settings {
				s := settings[i]
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					ui.Text(c, s.Label).FontSize(11).FontWeight(600).TextColor(text)
					if s.Help != "" {
						ui.Text(c, s.Help).FontSize(10).TextColor(muted)
					}
					field := ui.TextInput(c, &a.find.settingFields[i].value).Placeholder(s.Placeholder).FillWidth()
					if s.Secret {
						_ = field // No password field in ui; store as plain in the value map.
					}
					_ = field
				})
				if s.Secret {
					ui.Text(c, locales.SourceSheetSecretNote()).FontSize(10).TextColor(muted)
				}
			}
		}
	}

	// Importer buttons.
	if _, ok := a.sources.Importer(id); ok {
		ui.Row(c).Gap(8).Padding(6, 0, 0, 0).Children(func() {
			importBtn := ui.Box(c).Height(32).Padding(0, 12).Radius(8).Center().
				Background(reader.UIColor(pal.Accent)).Cursor(ui.CursorPointer).
				Label(locales.SourceSheetImport())
			importBtn.OnClick(func() { a.importSourceDefinitions(id) })
			importBtn.Children(func() {
				ui.Text(c, locales.SourceSheetImport()).FontSize(12).FontWeight(600).
					TextColor(reader.UIColor(pal.OnAccent))
			})

			exportBtn := ui.Box(c).Height(32).Padding(0, 12).Radius(8).Center().
				Background(reader.UIColor(pal.Raised)).Cursor(ui.CursorPointer).
				Label(locales.SourceSheetExport())
			exportBtn.OnClick(func() { a.exportSourceDefinitions(id) })
			exportBtn.Children(func() {
				ui.Text(c, locales.SourceSheetExport()).FontSize(12).FontWeight(600).TextColor(text)
			})
		})
	}

	// Save / done.
	ui.Row(c).Gap(8).Justify(ui.End).Padding(10, 0, 0, 0).Children(func() {
		if ui.Button(c, locales.SourceSheetDone()).Clicked() {
			a.find.settingsOpen = false
		}
		if ui.Button(c, locales.SourceSheetSave()).Clicked() {
			a.saveSourceSettings(id)
		}
	})
}

func (a *app) showSourceSettings(id source.ID) {
	if a.find == nil {
		return
	}
	a.find.settingsOpen = true
	a.find.settingsFor = id
	a.find.settingFields = nil
	saved := map[string]string{}
	if s := a.srcConf.Get(string(id)); s != nil {
		saved = s
	}
	if cfg, ok := a.sources.Configurable(id); ok {
		for _, s := range cfg.Settings() {
			val := saved[s.Key]
			if val == "" && s.Value != "" {
				val = s.Value
			}
			a.find.settingFields = append(a.find.settingFields, sourceSettingField{key: s.Key, value: val})
		}
	}
}

func (a *app) saveSourceSettings(id source.ID) {
	if a.find == nil {
		return
	}
	if cfg, ok := a.sources.Configurable(id); ok {
		values := make(map[string]string, len(a.find.settingFields))
		for _, f := range a.find.settingFields {
			values[f.key] = f.value
		}
		if err := cfg.Configure(values); err != nil {
			a.notice = locales.SourceSheetSourceError(sourceName(a, id), err.Error())
			return
		}
		if err := a.srcConf.Set(string(id), values); err != nil {
			a.notice = locales.SourceSheetSourceError(sourceName(a, id), err.Error())
			return
		}
	}
	a.find.settingsOpen = false
}

func (a *app) importSourceDefinitions(id source.ID) {
	win := a.showWindow()
	if win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  win,
			Title:   locales.SourceSheetImport(),
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.notice = locales.SourceSheetImportFailed(err.Error())
			})
			return
		}
		im, ok := a.sources.Importer(id)
		if !ok {
			return
		}
		added, err := im.Import(data)
		win.Update(func() {
			defer a.refresh(win)
			if err != nil {
				a.notice = locales.SourceSheetImportFailed(err.Error())
				return
			}
			if err := a.srcConf.Set(string(id), map[string]string{"definitions": string(data)}); err != nil {
				a.notice = locales.SourceSheetSourceError(sourceName(a, id), err.Error())
				return
			}
			a.notice = locales.SourceSheetImported(added)
		})
	}()
}

func (a *app) exportSourceDefinitions(id source.ID) {
	win := a.showWindow()
	if win == nil {
		return
	}
	im, ok := a.sources.Importer(id)
	if !ok {
		return
	}
	go func() {
		data, err := im.Export()
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.notice = locales.SourceSheetSourceError(sourceName(a, id), err.Error())
			})
			return
		}
		paths, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:  win,
			Title:   locales.SourceSheetExport(),
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		if err := os.WriteFile(paths, data, 0o600); err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.notice = locales.SourceSheetSourceError(sourceName(a, id), err.Error())
			})
		}
	}()
}

// buildSources returns the concrete source implementations. On this branch it
// returns an empty list so the UI can be built and tested without the source
// packages. During integration, replace the body with:
//
//	return []source.Source{
//		zlib.New(...),
//		legado.New(...),
//		wenku8.New(...),
//	}
func buildSources() []source.Source {
	return nil
}

// initSources loads the source configuration, builds the registry, and
// configures each source from the saved values. Errors are logged but do not
// stop launch.
func (d *desk) initSources(srcPath string) {
	if srcPath == "" {
		if cfgDir, err := os.UserConfigDir(); err == nil {
			srcPath = filepath.Join(cfgDir, "quire", "sources.json")
		}
	}
	downloadDir := ""
	if srcPath != "" {
		downloadDir = filepath.Join(filepath.Dir(srcPath), "books")
	}

	var err error
	d.srcConf, err = sourceconf.Open(srcPath)
	if err != nil {
		log.Printf("sourceconf: %v", err)
		d.srcConf, _ = sourceconf.Open(filepath.Join(os.TempDir(), "quire-sources.json"))
	}
	if d.srcConf == nil {
		d.srcConf, _ = sourceconf.Open(filepath.Join(os.TempDir(), "quire-sources.json"))
	}

	d.sources = source.NewRegistry()
	for _, s := range buildSources() {
		if err := d.sources.Add(s); err != nil {
			log.Printf("source: %v", err)
		}
	}

	d.acq = acquire.New(acquire.Options{Dir: downloadDir, Prefix: "source-"})

	for _, s := range d.sources.All() {
		id := string(s.ID())
		values := d.srcConf.Get(id)
		if values == nil {
			values = map[string]string{}
		}
		if cfg, ok := s.(source.Configurable); ok {
			if err := cfg.Configure(values); err != nil {
				log.Printf("source %s configure: %v", id, err)
			}
		}
		if im, ok := s.(source.Importer); ok && values["definitions"] != "" {
			if _, err := im.Import([]byte(values["definitions"])); err != nil {
				log.Printf("source %s import: %v", id, err)
			}
		}
	}
}
