package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/store"
)

// benchScrollApp builds a reader showing the sample book as a scrolling
// column, ready for frame benchmarks.
func benchScrollApp(b testing.TB) *app {
	b.Helper()
	fontPath, err := reader.FindFont("")
	if err != nil {
		b.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		b.Fatal(err)
	}
	bk, err := book.Open("internal/book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		b.Fatal(err)
	}
	doc, images, err := bk.Build(reader.DefaultFontSize)
	if err != nil {
		b.Fatal(err)
	}
	st, err := store.Open(b.TempDir() + "/state.json")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	reader.SetPalette(reader.Quiet)
	b.Cleanup(func() { reader.SetPalette(reader.Quiet) })

	lib := &library.Library{Dir: "internal/book/testdata/samples"}
	if err := lib.Reload(); err != nil {
		b.Fatal(err)
	}
	up := reader.NewUIPainter(nil, fonts)
	for id, data := range images {
		up.SetImage(id, data)
	}
	pan := appstate.New(st)
	pan.Library = lib
	pan.Shelf.Lib = lib

	w := float32(pageMaxWidth)
	h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	col, err := reader.BuildLayout(context.Background(), doc, fonts, w, h,
		reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		b.Fatal(err)
	}
	a := &app{
		fonts: fonts, paint: up, book: bk, doc: doc, docAt: reader.DefaultFontSize,
		opts:  reader.Options{FontSize: reader.DefaultFontSize, Scroll: true, Spread: false},
		theme: reader.ThemeQuiet, pan: pan, st: st, instant: true,
		layout: col, layW: w, layH: h, layScroll: true, tileH: h,
		covers: map[coverKey]*ui.Bitmap{},
	}
	a.syncSettingsFields()
	pan.Jump = a.gotoLoc
	pan.Open = func(string) {}
	pan.Cover = a.coverBitmap
	if loc, ok := col.LocationAt(0); ok {
		pan.RefreshAt(bk, col, loc)
	}
	return a
}

// BenchmarkScrollFrame measures one frame of a scrolling glide as the reader
// drives it: the view built anew, the column moved, and the window painted.
func BenchmarkScrollFrame(b *testing.B) {
	a := benchScrollApp(b)
	tt := ui.NewTester(func(c *ui.Context) {
		a.scrollY += 6
		if a.scrollY > a.maxScroll() {
			a.scrollY = 0
		}
		a.scrollGoal = a.scrollY
		a.scrollAhead()
		c.AnimationFrame()
		a.view(c)
	}, 1180, 800)
	tt.Frame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Advance(16 * time.Millisecond)
	}
}
