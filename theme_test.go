package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
)

// TestThemeSwitchKeepsTheBook pins a paper change to a repaint. The document
// stands, so the search index, the images and the reader's place stand with it,
// and the relayout the job runs keeps every page where it was: the switch costs
// the same on a novel as on a shelf of them.
func TestThemeSwitchKeepsTheBook(t *testing.T) {
	a := newWindow(t)
	a.layPaper = paperOf(reader.Current())
	doc, base := a.doc, a.layout
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	a.setTheme(reader.ThemeBold)
	tt.Frame()
	if got := reader.Current().Theme; got != reader.ThemeBold {
		t.Fatalf("paper is %q after a theme change, want %q", got, reader.ThemeBold)
	}
	if a.doc != doc {
		t.Fatal("a theme change rebuilt the document")
	}
	// The frame changed the palette, so ensureLayout has a job to do; it is a
	// paint-only one when the page geometry stands.
	paper := paperOf(reader.Current())
	if paper == a.layPaper {
		t.Fatal("the frame kept the old palette, so no repaint was asked for")
	}
	if base.Document() != doc {
		t.Fatal("the layout on screen no longer belongs to the book")
	}

	relaid, err := reader.Relayout(context.Background(), base, a.fonts, a.layW, a.layH, a.opts)
	if err != nil {
		t.Fatal(err)
	}
	if relaid.Document() != doc {
		t.Fatal("the relayout did not reuse the document")
	}
	if relaid.PageCount() != base.PageCount() {
		t.Fatalf("page count moved on a theme change: %d -> %d", base.PageCount(), relaid.PageCount())
	}
	if got, want := relaid.Typography().Color, reader.Current().Ink; got != want {
		t.Fatalf("relaid pages print in %v, want the paper's ink %v", got, want)
	}
	if got, want := relaid.Typography().LinkColor, reader.Current().Link; got != want {
		t.Fatalf("relaid pages link in %v, want the paper's link colour %v", got, want)
	}
	// Same pages means the reader's place, its selection and the panels' marks
	// are still the right ones: the job publishes the layout and stops there.
	if loc, ok := base.LocationAt(0); ok {
		if again, ok := relaid.LocationAt(0); !ok || again != loc {
			t.Fatalf("location at page 0 moved: %v -> %v", loc, again)
		}
	}
}
