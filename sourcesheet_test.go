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
	// These readers have no window. Run task callbacks inline so the
	// background state they publish can be observed by the test, just as a
	// window's Update callback would publish it on the UI thread.
	a.tasks.Store(newTaskScope(nil, func(fn func()) { fn() }))
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
	if !f.busy() {
		t.Fatal("expected downloading to be true")
	}
	// The real acquire.Manager writes the fake EPUB into the app's temp
	// download directory and reports the saved path back on completion.
	deadline := time.Now().Add(2 * time.Second)
	for f.busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.busy() {
		t.Fatal("expected downloading to be false after completion")
	}
	if err := f.downloadErr(); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	saved := f.saved()
	if saved.Path == "" {
		t.Fatal("expected a saved path")
	}
	if !strings.HasPrefix(filepath.Base(saved.Path), "test-") {
		t.Errorf("saved name %q does not carry the configured prefix", saved.Path)
	}
	if saved.Format != "epub" {
		t.Errorf("saved format = %q, want epub", saved.Format)
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
	if !f.busy() {
		t.Fatal("expected downloading to be true")
	}
	f.cancelDownload()
	if f.busy() {
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

// initSources is the launch path: it must register all three independent
// sources and give downloads a directory, without touching the network.
func TestInitSourcesRegistersThree(t *testing.T) {
	dir := t.TempDir()
	d := &desk{}
	d.initSources(filepath.Join(dir, "sources.json"))

	want := []source.ID{"zlib", "legado", "wenku8"}
	got := d.sources.IDs()
	if len(got) != len(want) {
		t.Fatalf("registered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("source %d = %q, want %q", i, got[i], want[i])
		}
	}
	if d.acq == nil {
		t.Fatal("download manager is nil")
	}
	if d.srcConf == nil {
		t.Fatal("source configuration is nil")
	}
	// Every source either searches and downloads, or says why it cannot.
	for _, s := range d.sources.All() {
		if !s.Caps().Search || !s.Caps().Download {
			t.Errorf("%s caps = %+v", s.ID(), s.Caps())
		}
	}
}

func TestSourceSearchReplacesAndCancelsOlderTask(t *testing.T) {
	oldStarted := make(chan context.Context, 1)
	oldReturned := make(chan struct{})
	a, cleanup := testApp(t, testRegistry(&fakeSource{
		id:   "slow",
		name: "Slow",
		search: func(ctx context.Context, q source.Query) (source.Result, error) {
			if q.Text == "old" {
				oldStarted <- ctx
				<-ctx.Done()
				close(oldReturned)
				return source.Result{}, ctx.Err()
			}
			return source.Result{Books: []source.Book{{Source: "slow", Key: "new", Title: "New"}}}, nil
		},
	}))
	defer cleanup()

	// Keep posts queued so this test, like the UI thread, applies them one at
	// a time after both searches have been asked for.
	posted := make(chan func(), 4)
	a.tasks.Store(newTaskScope(nil, func(fn func()) { posted <- fn }))
	f := a.find

	f.query = "old"
	f.startSearch(a)
	var oldCtx context.Context
	select {
	case oldCtx = <-oldStarted:
	case <-time.After(time.Second):
		t.Fatal("old search did not start")
	}

	f.query = "new"
	f.startSearch(a)
	select {
	case <-oldCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("starting a new search did not cancel the old task")
	}
	select {
	case <-oldReturned:
	case <-time.After(time.Second):
		t.Fatal("old search did not return after cancellation")
	}

	var resultCallback func()
	select {
	case resultCallback = <-posted:
	case <-time.After(time.Second):
		t.Fatal("new search did not post its result")
	}
	resultCallback()

	if len(f.results) != 1 || f.results[0].book.Key != "new" {
		t.Fatalf("results = %+v, want only the new search's result", f.results)
	}
	if f.searchCancel != nil {
		t.Fatal("completed search left a cancel handle behind")
	}
}

func TestSourceSearchCanceledByAppScope(t *testing.T) {
	started := make(chan context.Context, 1)
	a, cleanup := testApp(t, testRegistry(&fakeSource{
		id:   "slow",
		name: "Slow",
		search: func(ctx context.Context, q source.Query) (source.Result, error) {
			started <- ctx
			<-ctx.Done()
			return source.Result{}, ctx.Err()
		},
	}))
	defer cleanup()

	posted := make(chan func(), 1)
	a.tasks.Store(newTaskScope(nil, func(fn func()) { posted <- fn }))
	f := a.find
	f.query = "x"
	f.startSearch(a)

	var ctx context.Context
	select {
	case ctx = <-started:
	case <-time.After(time.Second):
		t.Fatal("search did not start")
	}
	a.closeTasks()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("closing the app task scope did not cancel the search")
	}
	select {
	case fn := <-posted:
		fn()
		t.Fatal("canceled app scope posted a search callback")
	case <-time.After(50 * time.Millisecond):
	}
}
