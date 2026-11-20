package appstate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/laic7092/layx/render"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/search"
	"github.com/laix/quire/locales"
)

func lazySearchFixture(t *testing.T) (*book.Book, *render.Document) {
	t.Helper()
	bk, err := book.Open(filepath.Join("..", "book", "testdata", "samples", "aq-zhuan.epub"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	return bk, doc
}

// Installing a builder does not walk the book. The first query does, and a
// second query of the same document reuses the index it published.
func TestIndexBuilderIsLazyAndRunsOncePerDocument(t *testing.T) {
	bk, doc := lazySearchFixture(t)
	a := New(nil)
	calls := 0
	a.SetIndexBuilder(func() (*search.Index, error) {
		calls++
		return search.NewIndex(bk, doc)
	})

	if calls != 0 {
		t.Fatalf("builder ran %d times before a search", calls)
	}
	if a.Index != nil {
		t.Fatal("the lazy index is already published")
	}
	if !a.CanSearch() {
		t.Fatal("the lazy builder does not make the document searchable")
	}

	a.Search.Query = "阿"
	a.Search.OnSearch(a.Search.Query)
	if calls != 1 {
		t.Fatalf("builder ran %d times for the first search, want 1", calls)
	}
	if a.Index == nil {
		t.Fatal("the first search did not publish an index")
	}
	if len(a.Search.Hits) == 0 {
		t.Fatalf("searching the sample found nothing: %q", a.Search.Status)
	}

	a.Search.OnSearch(a.Search.Query)
	if calls != 1 {
		t.Fatalf("builder ran %d times after the index was published, want 1", calls)
	}
}

// A build that outlives the book it was started for must not publish its index
// into the next book or keep the old search state on screen.
func TestStaleIndexBuildCannotReplaceANewerBook(t *testing.T) {
	bk, doc := lazySearchFixture(t)
	a := New(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	applied := make(chan struct{}, 1)
	a.Post = func(fn func()) {
		fn()
		applied <- struct{}{}
	}
	a.SetIndexBuilder(func() (*search.Index, error) {
		close(started)
		<-release
		return search.NewIndex(bk, doc)
	})
	a.Search.Query = "阿"
	a.Search.OnSearch(a.Search.Query)
	waitSignal(t, started, "the index builder to start")

	a.Refresh(nil, nil, 0) // close the book while it builds
	close(release)
	waitSignal(t, applied, "the stale build to apply")

	if a.Index != nil || a.CanSearch() {
		t.Error("the closed book still has a search index source")
	}
	if a.Search.Query != "" || len(a.Search.Hits) != 0 || a.Search.Status != "" {
		t.Errorf("the old search survived the book: query %q, %d hits, %q",
			a.Search.Query, len(a.Search.Hits), a.Search.Status)
	}
}

// A build that finishes after the reader has typed another query runs the new
// query, not the one that started the build. The one build is shared by both
// queries instead of walking the document twice.
func TestStaleIndexBuildRunsTheNewestQuery(t *testing.T) {
	bk, doc := lazySearchFixture(t)
	a := New(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	applied := make(chan struct{}, 2)
	a.Post = func(fn func()) {
		fn()
		applied <- struct{}{}
	}
	calls := 0
	a.SetIndexBuilder(func() (*search.Index, error) {
		calls++
		close(started)
		<-release
		return search.NewIndex(bk, doc)
	})

	a.Search.Query = "阿"
	a.Search.OnSearch(a.Search.Query)
	waitSignal(t, started, "the index builder to start")

	const newer = "definitely not in this book"
	a.Search.Query = newer
	a.Search.OnSearch(newer) // waits for the build in flight instead of starting another
	close(release)
	waitSignal(t, applied, "the build to apply")
	waitSignal(t, applied, "the newest search to apply")

	if calls != 1 {
		t.Fatalf("builder ran %d times, want one build for the document", calls)
	}
	if a.Index == nil {
		t.Fatal("the finished build did not publish its index")
	}
	if len(a.Search.Hits) != 0 {
		t.Errorf("the old query's %d hits won over the newer query", len(a.Search.Hits))
	}
	if want := locales.SearchNotFound(newer); a.Search.Status != want {
		t.Errorf("status = %q, want %q", a.Search.Status, want)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Replacing the index source for a relayout keeps the query that already ran:
// the new document's index is built on that query's behalf, not eagerly when
// the builder was installed.
func TestNewDocumentBuilderRerunsAnActiveQuery(t *testing.T) {
	bk, doc := lazySearchFixture(t)
	a := New(nil)
	firstCalls := 0
	a.SetIndexBuilder(func() (*search.Index, error) {
		firstCalls++
		return search.NewIndex(bk, doc)
	})
	a.Search.Query = "阿"
	a.Search.OnSearch(a.Search.Query)
	if firstCalls != 1 || len(a.Search.Hits) == 0 {
		t.Fatalf("first search: %d builds, %d hits, %q",
			firstCalls, len(a.Search.Hits), a.Search.Status)
	}

	secondCalls := 0
	a.SetIndexBuilder(func() (*search.Index, error) {
		secondCalls++
		return search.NewIndex(bk, doc)
	})
	if secondCalls != 1 {
		t.Fatalf("new document builder ran %d times, want 1", secondCalls)
	}
	if len(a.Search.Hits) == 0 {
		t.Errorf("the active query lost its results after the relayout: %q", a.Search.Status)
	}
}
