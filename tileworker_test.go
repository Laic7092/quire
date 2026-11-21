package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/reader"
)

// The column's tiles are drawn by the window's worker, off the frame's thread:
// the frames before they come back replay the display lists, and the frames
// after composite the pixels — neither waits for the other.
func TestTilesComeFromTheWorker(t *testing.T) {
	a := benchScrollApp(t)
	a.tileWork = newTileWorker(a.fonts)
	if a.tileWork == nil {
		t.Skip("no faces to draw with")
	}
	defer a.tileWork.close()
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 390, 844)
	tt.SetScale(3)
	reader.CountGlyphs(true)
	t.Cleanup(func() { reader.CountGlyphs(false) })

	nudge := func() {
		// Something has to change for a frame to build the view again.
		a.scrollY += 1
		a.scrollGoal = a.scrollY
		tt.Frame()
	}
	nudge()
	if len(a.tiles.entry) != 0 {
		t.Fatal("a tile was there before the worker drew one")
	}

	deadline := time.Now().Add(30 * time.Second)
	for len(a.tiles.entry) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		nudge()
	}
	if len(a.tiles.entry) == 0 {
		t.Fatal("no tile came back from the worker")
	}
	for i, e := range a.tiles.entry {
		w, h := e.bmp.Size()
		if e.bytes != w*h*4 {
			t.Fatalf("tile %d says %d bytes for %dx%d pixels", i, e.bytes, w, h)
		}
		if w <= 0 || h <= 0 {
			t.Fatalf("tile %d came back empty", i)
		}
	}
}
