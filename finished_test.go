package main

import (
	"testing"
	"time"

	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/store"
)

// A position marked finished counts as read even though Progress names the top
// of the last page and is below 1. The shelf's "finished" filter must see it,
// and the "reading" filter must not.
func TestFinishedBooksCountAsRead(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	finished := library.Entry{Path: "/books/finished.epub", Title: "Finished", Size: 4, Modified: time.Unix(10, 0)}
	reading := library.Entry{Path: "/books/reading.epub", Title: "Reading", Size: 5, Modified: time.Unix(20, 0)}
	st.SetPosition(store.Key(finished.Path, finished.Size, finished.Modified),
		store.Position{Progress: 0.6, Finished: true})
	st.SetPosition(store.Key(reading.Path, reading.Size, reading.Modified),
		store.Position{Progress: 0.6})

	a := &app{st: st}
	if got := a.readProgress(finished); got != 1 {
		t.Errorf("finished book progress = %v, want 1", got)
	}
	if got := a.readProgress(reading); got <= 0 || got >= 1 {
		t.Errorf("part-read book progress = %v, want between 0 and 1", got)
	}

	a.pan = appstate.New(st)
	a.pan.Library = &library.Library{Entries: []library.Entry{finished, reading}}

	a.libFilter = 2
	if got := a.shelf(); len(got) != 1 || got[0].Title != finished.Title {
		t.Errorf("finished shelf = %v, want only %q", titles(got), finished.Title)
	}
	a.libFilter = 1
	if got := a.shelf(); len(got) != 1 || got[0].Title != reading.Title {
		t.Errorf("reading shelf = %v, want only %q", titles(got), reading.Title)
	}
}

func titles(entries []library.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title
	}
	return out
}
