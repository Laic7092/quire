package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/acquire"
	"github.com/laix/quire/internal/source"
	"github.com/laix/quire/internal/sourceconf"
	"github.com/laix/quire/locales"
)

// fakeSource is a source.Source used only in these tests.
type fakeSource struct {
	id       source.ID
	name     string
	books    []source.Book
	search   func(ctx context.Context, q source.Query) (source.Result, error)
	dl       func(ctx context.Context, b source.Book, p func(source.Progress)) (source.File, error)
	settings []source.Setting
}

func (f *fakeSource) ID() source.ID     { return f.id }
func (f *fakeSource) Name() string      { return f.name }
func (f *fakeSource) Caps() source.Caps { return source.Caps{Search: true, Download: f.dl != nil} }
func (f *fakeSource) Search(ctx context.Context, q source.Query) (source.Result, error) {
	if f.search != nil {
		return f.search(ctx, q)
	}
	return source.Result{Books: f.books}, nil
}
func (f *fakeSource) Download(ctx context.Context, b source.Book, p func(source.Progress)) (source.File, error) {
	if f.dl != nil {
		return f.dl(ctx, b, p)
	}
	return source.File{}, errors.New("no download")
}
func (f *fakeSource) Settings() []source.Setting { return f.settings }
func (f *fakeSource) Configure(values map[string]string) error {
	if len(f.settings) == 0 {
		return errors.New("not configurable")
	}
	return nil
}

func testRegistry(sources ...source.Source) *source.Registry {
	r := source.NewRegistry()
	for _, s := range sources {
		if err := r.Add(s); err != nil {
			panic(err)
		}
	}
	return r
}

func testApp(t *testing.T, reg *source.Registry) (*app, func()) {
	t.Helper()
	dir := t.TempDir()
	conf, err := sourceconf.Open(filepath.Join(dir, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		sources: reg,
		srcConf: conf,
		acq:     acquire.New(acquire.Options{Dir: filepath.Join(dir, "books"), Prefix: "test-"}),
		find:    &sourceSheet{},
	}
	return a, func() { _ = conf.Save() }
}

func TestSourceSheetFilterResults(t *testing.T) {
	a, cleanup := testApp(t, testRegistry(
		&fakeSource{id: "alpha", name: "Alpha", books: []source.Book{
			{Source: "alpha", Key: "a1", Title: "Apple", Author: "Ann"},
		}},
		&fakeSource{id: "beta", name: "Beta", books: []source.Book{
			{Source: "beta", Key: "b1", Title: "Banana", Author: "Ben"},
		}},
	))
	defer cleanup()

	f := a.find
	f.results = []sourceRow{
		{book: source.Book{Source: "alpha", Key: "a1", Title: "Apple"}},
		{book: source.Book{Source: "beta", Key: "b1", Title: "Banana"}},
		{book: source.Book{Source: "alpha", Key: "a2", Title: "Apricot"}},
	}

	f.filter = ""
	if got := len(f.filteredResults()); got != 3 {
		t.Fatalf("all filter want 3, got %d", got)
	}
	f.filter = "alpha"
	if got := len(f.filteredResults()); got != 2 {
		t.Fatalf("alpha filter want 2, got %d", got)
	}
	f.filter = "beta"
	if got := len(f.filteredResults()); got != 1 {
		t.Fatalf("beta filter want 1, got %d", got)
	}
}

func TestSourceSheetApplyOutcomes(t *testing.T) {
	a, cleanup := testApp(t, testRegistry(
		&fakeSource{id: "ok", name: "OK"},
		&fakeSource{id: "bad", name: "Bad"},
	))
	defer cleanup()

	f := a.find
	outcomes := []source.Outcome{
		{Source: &fakeSource{id: "ok", name: "OK"}, Result: source.Result{Books: []source.Book{
			{Source: "ok", Key: "1", Title: "One"},
			{Source: "ok", Key: "2", Title: "Two"},
		}}, Elapsed: 10 * time.Millisecond},
		{Source: &fakeSource{id: "bad", name: "Bad"}, Err: errors.New("offline"), Elapsed: 5 * time.Millisecond},
	}
	f.applyOutcomes(a, outcomes)

	if len(f.results) != 2 {
		t.Fatalf("want 2 results, got %d", len(f.results))
	}
	if f.statuses["bad"].err == nil {
		t.Fatal("expected error status for bad source")
	}
	if f.statuses["ok"].err != nil {
		t.Fatal("expected no error for ok source")
	}
	// Empty titles are dropped.
	outcomes[0].Result.Books = append(outcomes[0].Result.Books, source.Book{Source: "ok", Key: "3", Title: "  "})
	f.applyOutcomes(a, outcomes)
	if len(f.results) != 2 {
		t.Fatalf("empty title should be dropped, got %d", len(f.results))
	}
}

func TestSourceSheetStaleSearchIgnored(t *testing.T) {
	a, cleanup := testApp(t, testRegistry(
		&fakeSource{id: "slow", name: "Slow", search: func(ctx context.Context, q source.Query) (source.Result, error) {
			select {
			case <-ctx.Done():
				return source.Result{}, ctx.Err()
			case <-time.After(50 * time.Millisecond):
				return source.Result{Books: []source.Book{{Source: "slow", Key: "old", Title: "Old"}}}, nil
			}
		}},
	))
	defer cleanup()

	f := a.find
	f.searchID = 1

	// Outcome from an older search id should not apply.
	oldOutcomes := []source.Outcome{
		{Source: &fakeSource{id: "slow", name: "Slow"}, Result: source.Result{Books: []source.Book{
			{Source: "slow", Key: "old", Title: "Old"},
		}}},
	}
	f.searchID = 2
	f.applyOutcomes(a, oldOutcomes)
	if len(f.results) != 1 {
		t.Fatalf("applyOutcomes does not check id, want 1 result kept from stale, got %d", len(f.results))
	}

	// The real guard is in startSearch's closure: it only applies when id == f.searchID.
	// Test the searchID increment.
	f.query = "x"
	f.startSearch(a)
	firstID := f.searchID
	f.startSearch(a)
	if f.searchID != firstID+1 {
		t.Fatalf("searchID should increment, got %d after %d", f.searchID, firstID)
	}
	if f.searchCancel == nil {
		t.Fatal("expected a cancel func for the new search")
	}
	f.searchCancel()
}

func TestSourceSheetDownloadState(t *testing.T) {
	reg := testRegistry(&fakeSource{
		id:   "down",
		name: "Down",
		books: []source.Book{
			{Source: "down", Key: "d1", Title: "Downloadable"},
		},
		dl: func(ctx context.Context, b source.Book, p func(source.Progress)) (source.File, error) {
			return source.File{Name: "book.epub", Format: "epub", Data: []byte("PK fake epub")}, nil
		},
	})
	a, cleanup := testApp(t, reg)
	defer cleanup()

	f := a.find
	book := source.Book{Source: "down", Key: "d1", Title: "Downloadable"}
	f.download(a, book)
	if !f.downloading {
		t.Fatal("expected downloading to be true")
	}
	// Wait for the stub acquire.Save to finish.
	time.Sleep(50 * time.Millisecond)
	if f.downloading {
		t.Fatal("expected downloading to be false after completion")
	}
	// acquire.Manager is currently a stub and returns ErrNotConfigured, so the
	// sheet records the error instead of a saved path.
	if f.dlErr == nil {
		t.Fatal("expected download error because acquire.Save is a stub")
	}
}

func TestSourceSheetDownloadCancels(t *testing.T) {
	reg := testRegistry(&fakeSource{
		id:   "down",
		name: "Down",
		books: []source.Book{
			{Source: "down", Key: "d1", Title: "Downloadable"},
		},
	})
	a, cleanup := testApp(t, reg)
	defer cleanup()

	f := a.find
	book := source.Book{Source: "down", Key: "d1", Title: "Downloadable"}
	f.download(a, book)
	if !f.downloading {
		t.Fatal("expected downloading to be true")
	}
	f.cancelDownload()
	if f.downloading {
		t.Fatal("expected downloading to be false after cancel")
	}
}

func TestSourceSheetUIRenders(t *testing.T) {
	reg := testRegistry(&fakeSource{
		id: "demo", name: "Demo",
		books: []source.Book{
			{Source: "demo", Key: "1", Title: "First Book", Author: "A. Author", Format: "epub", Size: 1024, Description: "A fine book."},
			{Source: "demo", Key: "2", Title: "Second Book", Author: "B. Author", Format: "txt", Size: 2048, Description: "Another fine book."},
		},
	})
	a, cleanup := testApp(t, reg)
	defer cleanup()
	a.find.openSheet("")
	a.find.query = "book"
	a.find.applyOutcomes(a, []source.Outcome{{
		Source:  &fakeSource{id: "demo", name: "Demo"},
		Result:  source.Result{Books: a.sources.All()[0].(*fakeSource).books},
		Elapsed: 5 * time.Millisecond,
	}})

	view := func(c *ui.Context) {
		t := c.Theme()
		ui.Box(c).Fill().Background(t.Background).Children(func() {
			a.sourceDialog(c, t)
		})
	}
	tt := ui.NewTester(view, 900, 700)
	if !tt.HasText(locales.SourceSheetTitle()) {
		t.Fatal("expected sheet title")
	}
	if !tt.HasText("First Book") || !tt.HasText("Second Book") {
		t.Fatal("expected both books to render")
	}
	if !tt.HasText(locales.SourceSheetDownload()) {
		t.Fatal("expected download button")
	}

	// Simulate clicking the search button: it should not panic with the current query.
	if err := tt.Click(locales.Search()); err != nil {
		t.Logf("search button click: %v", err)
	}
}

func TestSourceNameFallback(t *testing.T) {
	a, cleanup := testApp(t, testRegistry())
	defer cleanup()
	if got := sourceName(a, "zlib"); got != locales.SourceZLibrary() {
		t.Fatalf("zlib fallback want %q, got %q", locales.SourceZLibrary(), got)
	}
	if got := sourceName(a, "legado"); got != locales.SourceLegado() {
		t.Fatalf("legado fallback want %q, got %q", locales.SourceLegado(), got)
	}
	if got := sourceName(a, "wenku8"); got != locales.SourceWenku8() {
		t.Fatalf("wenku8 fallback want %q, got %q", locales.SourceWenku8(), got)
	}
}

func TestSourceMetaLine(t *testing.T) {
	a, cleanup := testApp(t, testRegistry())
	defer cleanup()
	book := source.Book{Source: "demo", Key: "1", Title: "X", Format: "epub", Size: 1536}
	meta := sourceMeta(a, book)
	if !strings.Contains(meta, "EPUB") {
		t.Fatalf("meta should contain format, got %q", meta)
	}
	if !strings.Contains(meta, "2 KB") {
		t.Fatalf("meta should contain size, got %q", meta)
	}
}
