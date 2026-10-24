package library

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/locales"
)

// TestPanelDrawsAndOpens runs the panel headless: it shows titles and
// details, filters as the query changes, and a click on a row opens that
// book.
func TestPanelDrawsAndOpens(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Alpha.txt", "alpha", testTime())
	writeFile(t, dir, "beta.txt", "beta", testTime())

	lib := &Library{}
	entries, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib.Entries = entries
	opened := ""
	reloaded := 0
	p := &Panel{
		Lib:      lib,
		Selected: -1,
		OnOpen:   func(path string) { opened = path },
		OnReload: func() { reloaded++ },
	}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 360, 480)

	if !tt.HasText("Alpha") || !tt.HasText("beta") {
		t.Fatalf("titles not shown: %v", tt.Texts())
	}
	if !tt.HasText("TXT") {
		t.Fatalf("row detail not shown: %v", tt.Texts())
	}
	if !tt.HasText(locales.ShelfCount(2)) {
		t.Fatalf("count not shown: %v", tt.Texts())
	}

	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	if opened != filepath.Join(dir, "Alpha.txt") {
		t.Fatalf("click opened %q", opened)
	}
	if p.Selected != 0 {
		t.Fatalf("click chose row %d, want 0", p.Selected)
	}

	// The query filters the list without touching the disk.
	p.Query = "BETA"
	tt.Frame()
	if tt.HasText("Alpha") {
		t.Fatalf("query BETA still shows Alpha: %v", tt.Texts())
	}
	if !tt.HasText("beta") || !tt.HasText(locales.ShelfCountFiltered(1, 2)) {
		t.Fatalf("filtered list wrong: %v", tt.Texts())
	}

	// A query that matches nothing explains itself.
	p.Query = "nothing"
	tt.Frame()
	if !tt.HasText(locales.ShelfNoMatch("nothing")) {
		t.Fatalf("empty result shows no hint: %v", tt.Texts())
	}

	// Refresh reports to the caller instead of scanning inside View.
	p.Query = ""
	tt.Frame()
	if err := tt.Click(locales.ShelfRefresh()); err != nil {
		t.Fatal(err)
	}
	if reloaded != 1 {
		t.Fatalf("refresh clicked %d times", reloaded)
	}
}

// TestPanelEmptyStates draws a hint instead of a blank panel.
func TestPanelEmptyStates(t *testing.T) {
	p := &Panel{Selected: -1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 300, 300)
	if !tt.HasText(locales.ShelfEmpty()) {
		t.Fatalf("no library: %v", tt.Texts())
	}

	p.Lib = &Library{}
	tt.Frame()
	if !tt.HasText(locales.ShelfEmptyHint()) {
		t.Fatalf("empty shelf: %v", tt.Texts())
	}
}

func testTime() time.Time { return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) }
