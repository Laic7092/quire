package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/laix/quire/internal/library"
)

// The cover cache follows the file it was decoded from: the same book in the same
// revision is decoded once, and a file replaced on disk — even by one of the same
// size — is read again rather than shown with the picture of the file before it.
func TestCoverCacheFollowsTheFile(t *testing.T) {
	entries, err := library.Scan("internal/book/testdata/samples")
	if err != nil {
		t.Fatal(err)
	}
	var entry *library.Entry
	for i := range entries {
		if len(entries[i].Cover) > 0 {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		t.Skip("the sample books have no cover")
	}

	a := &app{}
	first := a.coverBitmap(*entry)
	if first == nil {
		t.Skipf("the cover of %s does not decode", entry.Path)
	}
	if again := a.coverBitmap(*entry); again != first {
		t.Error("the same cover was decoded twice")
	}
	if got := len(a.covers); got != 1 {
		t.Errorf("the cache holds %d covers after reading one twice, want 1", got)
	}

	// The same path and size, another revision: the shelf shows the new picture.
	moved := *entry
	moved.Modified = moved.Modified.Add(time.Hour)
	if got := a.coverBitmap(moved); got == nil {
		t.Fatal("the replaced book has no cover")
	}
	if got := len(a.covers); got != 2 {
		t.Errorf("the cache holds %d covers after the book was replaced, want 2", got)
	}
}

// coverCacheTestEntry builds a distinct cache entry. Its cover is deliberately
// not an image: a decode failure still exercises the cache and keeps the test
// independent of the sample books.
func coverCacheTestEntry(i int) library.Entry {
	return library.Entry{
		Path:     fmt.Sprintf("book-%03d.epub", i),
		Size:     int64(i + 1),
		Modified: time.Unix(int64(i), 0).UTC(),
		Cover:    []byte("not an image"),
	}
}

func coverCacheContains(a *app, e library.Entry) bool {
	key := coverKey{path: e.Path, size: e.Size, modified: e.Modified}
	_, ok := a.covers[key]
	return ok
}

func TestCoverCacheCachesDecodeErrors(t *testing.T) {
	a := &app{}
	e := coverCacheTestEntry(0)
	if got := a.coverBitmap(e); got != nil {
		t.Fatalf("an undecodable cover returned a bitmap")
	}
	if !coverCacheContains(a, e) {
		t.Fatal("the failed decode was not cached")
	}
	if got := a.coverBitmap(e); got != nil {
		t.Fatalf("the cached decode error did not stay nil")
	}
	if got := len(a.covers); got != 1 {
		t.Fatalf("the cache holds %d entries after reading one broken cover twice, want 1", got)
	}
}

func TestCoverCacheEvictsOldestEntry(t *testing.T) {
	a := &app{}
	for i := 0; i < coverCacheLimit; i++ {
		a.coverBitmap(coverCacheTestEntry(i))
	}
	if got := len(a.covers); got != coverCacheLimit {
		t.Fatalf("the cache holds %d covers after filling it, want %d", got, coverCacheLimit)
	}

	newest := coverCacheTestEntry(coverCacheLimit)
	a.coverBitmap(newest)
	if got := len(a.covers); got != coverCacheLimit {
		t.Fatalf("the cache holds %d covers after eviction, want %d", got, coverCacheLimit)
	}
	if coverCacheContains(a, coverCacheTestEntry(0)) {
		t.Error("the least recently used cover survived eviction")
	}
	if !coverCacheContains(a, newest) {
		t.Error("the newly decoded cover is missing")
	}
	if !coverCacheContains(a, coverCacheTestEntry(1)) {
		t.Error("eviction removed more than the oldest cover")
	}
}

func TestCoverCacheKeepsRecentlyUsedEntry(t *testing.T) {
	a := &app{}
	for i := 0; i < coverCacheLimit; i++ {
		a.coverBitmap(coverCacheTestEntry(i))
	}

	recent := coverCacheTestEntry(0)
	a.coverBitmap(recent) // a cache hit makes this the most recently used
	a.coverBitmap(coverCacheTestEntry(coverCacheLimit))

	if !coverCacheContains(a, recent) {
		t.Error("a recently used cover was evicted")
	}
	if coverCacheContains(a, coverCacheTestEntry(1)) {
		t.Error("the next least recently used cover was not evicted")
	}
	if got := len(a.covers); got != coverCacheLimit {
		t.Errorf("the cache holds %d covers, want %d", got, coverCacheLimit)
	}
}
