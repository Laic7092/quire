package main

import (
	"context"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
)

// The view builds headlessly, as MyGo's tester draws frames without a window.
func TestViewBuildsWithoutABook(t *testing.T) {
	a := &app{fontSize: reader.DefaultFontSize, status: "打开一本 EPUB 开始阅读"}
	tt := ui.NewTester(a.view, 900, 700)
	if !tt.HasText("打开") {
		t.Fatalf("toolbar not built: %q", tt.Texts())
	}
	if !tt.HasText("— / —") {
		t.Errorf("empty page counter missing: %q", tt.Texts())
	}
	if !tt.HasText("A-") || !tt.HasText("A+") {
		t.Errorf("font buttons missing: %q", tt.Texts())
	}
	// Keys must not panic while nothing is open.
	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyEqual)
}

// With a laid-out book the tester exercises the real drawing callback and the
// page-turning controls.
func TestViewShowsALaidOutPage(t *testing.T) {
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := book.Open("internal/book/testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 900, 600, reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}

	a := &app{
		fonts:    fonts,
		paint:    reader.NewUIPainter(nil, fonts),
		book:     bk,
		doc:      doc,
		docAt:    reader.DefaultFontSize,
		layout:   layout,
		fontSize: reader.DefaultFontSize,
		layW:     900,
		layH:     600,
	}
	tt := ui.NewTester(a.view, 1000, 760)
	pages := strconv.Itoa(layout.PageCount())
	if !tt.HasText("1 / " + pages) {
		t.Fatalf("page counter missing: %q", tt.Texts())
	}
	if !tt.HasText(bk.Chapters[0].Title) {
		t.Errorf("chapter title missing: %q", tt.Texts())
	}

	if layout.PageCount() > 1 {
		if err := tt.Click("下一页"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("2 / " + pages) {
			t.Errorf("page did not turn: %q", tt.Texts())
		}
		if err := tt.Click("上一页"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("1 / " + pages) {
			t.Errorf("page did not turn back: %q", tt.Texts())
		}
	}

	// The window will not own a background job in a tester (a.win is nil), but
	// the font keys still update the state without panicking.
	tt.Key(0, ui.KeyEqual)
	if a.fontSize <= reader.DefaultFontSize {
		t.Errorf("font size %g after +", a.fontSize)
	}
	tt.Key(0, ui.KeyMinus)
	if a.fontSize != reader.DefaultFontSize {
		t.Errorf("font size %g after -", a.fontSize)
	}
}
