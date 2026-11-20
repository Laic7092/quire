package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laic7092/layx/render"
)

// longBookApp is the reader on a book with enough chapters for a preview to
// leave some of them out: a page away from the reading place is a page the
// preview cannot draw.
func longBookApp(t *testing.T, chapters int) (*app, float32, float32) {
	t.Helper()
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i := 1; i <= chapters; i++ {
		fmt.Fprintf(&sb, "第%d章 风起\n", i)
		for j := 0; j < 6; j++ {
			fmt.Fprintf(&sb, "\u3000\u3000这是第%d章的第%d段，主角又一次踏上了修炼之路。\n", i, j+1)
		}
	}
	path := filepath.Join(t.TempDir(), "book.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := b.BuildWith(reader.DefaultFontSize, book.Options{ThemeLinks: true})
	if err != nil {
		t.Fatal(err)
	}
	opts := reader.DefaultOptions()
	w := float32(pageMaxWidth)
	h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, w, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		fonts:  fonts,
		paint:  reader.NewUIPainter(nil, fonts),
		book:   b,
		doc:    doc,
		docAt:  reader.DefaultFontSize,
		opts:   opts,
		theme:  reader.ThemeQuiet,
		layout: layout,
		layW:   w,
		layH:   h,
		page:   1,
	}
	return a, w, h
}

// A resize lays the chapters on screen out for the sheet being dragged to
// straight away, so the text reflows under the pointer instead of sitting at its
// old measure until the whole book lands. The page it shows is the page the
// reader was reading: the preview is found by location, not by page number,
// which means nothing for a sheet the book has not been numbered at yet.
func TestResizePreviewReflowsThePageAtOnce(t *testing.T) {
	a := newWindow(t)
	paperW := float32(pageMaxWidth)
	paperH := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	a.layW, a.layH = paperW, paperH
	place, ok := a.currentLoc()
	if !ok {
		t.Fatal("the reading place is not known")
	}
	base := a.layout
	label := a.pageLabel()

	// A narrower sheet, as a drag would ask for.
	narrow := paperW - 90
	a.beginResize(narrow, paperH, a.opts)
	if a.preview == nil {
		t.Fatal("no preview was built for the new sheet")
	}
	if a.layout != base {
		t.Fatal("the preview replaced the published layout")
	}
	if got := a.preview.Viewport().Width; got != narrow {
		t.Fatalf("preview is %v wide, want the sheet being dragged to (%v)", got, narrow)
	}

	l, page := a.sheetPlan(a.page)
	if l != a.preview {
		t.Fatal("the sheet still draws the published layout")
	}
	// The preview page starts at the same words: the reading place is a
	// location, and it survives the new measure.
	want, ok := a.preview.PageOf(place)
	if !ok {
		t.Fatal("the preview does not hold the reading place")
	}
	if page != want {
		t.Fatalf("the sheet shows preview page %d, want the one holding the place (%d)", page, want)
	}
	if got, ok := a.preview.LocationOfPage(page); !ok || got.Chapter != place.Chapter {
		t.Fatalf("preview page %d starts at %+v, want chapter %d", page, got, place.Chapter)
	}
	// The veils and the selection follow the layout being drawn, not the one
	// being replaced.
	if a.marksOn != a.preview {
		t.Error("the marks were not rebuilt for the preview")
	}
	// The reading place is remembered for the layout that lands later, and the
	// page number the reader sees does not move while the sheet does.
	if !a.jumpAt || a.jumpLoc != place {
		t.Errorf("the place was not kept for the whole book: jumpAt=%v loc=%+v, want %+v",
			a.jumpAt, a.jumpLoc, place)
	}
	if got := a.pageLabel(); got != label {
		t.Errorf("the page number moved on a resize: %q -> %q", label, got)
	}
}

// A page the preview does not hold — the book jumped elsewhere while the resize
// was in flight — is drawn from the published layout rather than from the wrong
// chapter of the range.
func TestResizePreviewFallsBackOutsideItsChapters(t *testing.T) {
	a, paperW, paperH := longBookApp(t, 40)
	base := a.layout
	a.buildPreview(paperW-90, paperH, a.opts)
	if a.preview == nil {
		t.Skip("the book has too few pages to leave the preview's chapters")
	}
	// A page in a chapter the preview does not cover: the preview holds the
	// reading place's neighbours only.
	var page int
	found := false
	for far := len(a.book.Chapters) - 1; far >= 0 && !found; far-- {
		loc := render.Location{Chapter: far}
		if _, ok := a.preview.PageOf(loc); ok {
			continue
		}
		if p, ok := base.PageOf(loc); ok {
			page, found = p, true
		}
	}
	if !found {
		t.Skip("the book is shorter than the preview's range")
	}
	l, got := a.sheetPlan(page)
	if l != base {
		t.Fatalf("a page outside the preview's chapters was drawn from the preview (page %d)", got)
	}
}

// The scroll column is placed by location too: a resize preview shows the words
// the published column was showing, at the new measure.
func TestResizePreviewPlacesTheColumn(t *testing.T) {
	a := newWindow(t)
	w := float32(pageMaxWidth)
	h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	col, err := reader.BuildLayout(context.Background(), a.doc, a.fonts, w, h,
		reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	a.layout, a.layScroll, a.layW, a.layH = col, true, w, h
	a.tileH = col.Tile(0).Height()
	a.scrollY = 900
	place, ok := a.currentLoc()
	if !ok {
		t.Fatal("the column's place is not known")
	}
	a.buildPreview(w-80, h, a.opts)
	if a.preview == nil {
		t.Fatal("no preview was built for the new sheet")
	}
	column, y, tileH := a.columnPlan()
	if column != a.preview {
		t.Fatal("the column still draws the published layout")
	}
	if tileH != a.previewH {
		t.Fatalf("tile height %v, want the preview's %v", tileH, a.previewH)
	}
	want, ok := a.preview.OffsetOf(place)
	if !ok {
		t.Fatal("the preview does not hold the column's place")
	}
	if diff := y - want; diff > 1 || diff < -1 {
		t.Fatalf("the preview column stands at %v, want %v (the place it shows)", y, want)
	}
}

// A resize waits for the sheet to hold still before the whole book is laid out:
// the chapters on screen follow every size, and the book follows the last one, so
// dragging the window costs one layout instead of one per pixel.
func TestResizeWaitsForTheSheetToHoldStill(t *testing.T) {
	a := newWindow(t)
	w := float32(pageMaxWidth)
	h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	now := time.Now()
	a.now = func() time.Time { return now }
	a.layW, a.layH = w, h

	// The first frame of the drag asks for the chapters on screen.
	if got := a.resizeStep(w-40, h); got != resizePreview {
		t.Fatalf("first frame of the drag: step %v, want the preview", got)
	}
	// The next size, 30 ms later, is still the same drag: no whole-book layout.
	now = now.Add(30 * time.Millisecond)
	if got := a.resizeStep(w-80, h); got != resizePreview {
		t.Fatalf("30 ms into the drag: step %v, want the preview", got)
	}
	// The sheet has held still for the quiet period: the whole book goes ahead.
	now = now.Add(resizeDelay)
	if got := a.resizeStep(w-80, h); got != resizeGo {
		t.Fatalf("after the quiet period: step %v, want the whole book", got)
	}
	// And it is asked for once: the sheet is the one the job was started for.
	a.layW, a.layH = w-80, h
	if got := a.resizeStep(w-80, h); got != resizeIdle {
		t.Fatalf("once the job is under way: step %v, want nothing", got)
	}
	// A drag back to the size the published layout already has is answered by
	// the pages on screen: nothing is laid out.
	a.layW, a.layH = w, h
	if got := a.resizeStep(w, h); got != resizeIdle {
		t.Fatalf("back at the published size: step %v, want nothing", got)
	}
}

// A cancelled job never reports its context error as a failure of the book: the
// reader left the sheet it was laying out, and the frame loop gets the machine
// back.
func TestCancelledLayoutIsDropped(t *testing.T) {
	a := &app{}
	ctx, cancel := context.WithCancel(context.Background())
	a.layCancel = cancel
	a.loading = true
	job := a.job
	a.cancelLayout()
	if ctx.Err() == nil {
		t.Error("the job's context was not cancelled")
	}
	if a.loading {
		t.Error("a cancelled job is still marked as loading")
	}
	if a.job == job {
		t.Error("a cancelled job's publish would still be taken as current")
	}
	if a.layCancel != nil {
		t.Error("the cancel function outlived the job it stops")
	}
	a.cancelLayout() // nothing in flight: no panic, no change
	if a.job != job+1 {
		t.Errorf("cancelling twice bumped the job: %d -> %d", job, a.job)
	}
}
