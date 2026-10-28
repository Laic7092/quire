package main

import (
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
