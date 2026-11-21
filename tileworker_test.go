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
	a.tileWork = newTileWorker(a.fonts, a.paint)
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

// Each tile comes back with pixels of its own: the offscreen window hands out
// its buffer, which the next tile drawn over it rewrites.
func TestTilesComeBackWithPixelsOfTheirOwn(t *testing.T) {
	a := benchScrollApp(t)
	w := newTileWorker(a.fonts, a.paint)
	if w == nil {
		t.Skip("no faces to draw with")
	}
	defer w.close()
	tile := a.layout.Tile(0)
	if tile == nil {
		t.Skip("no tile")
	}
	job := tileJob{index: 0, tile: tile, w: tile.Size().Width, h: tile.Height(), strip: a.tileStrip(), ratio: 1, fade: 1, pal: a.palette()}
	first := w.draw(job)
	second := w.draw(job)
	if first == nil || second == nil {
		t.Fatal("no pixels")
	}
	if &first.Pix[0] == &second.Pix[0] {
		t.Fatal("both tiles came back in the same buffer")
	}
}

// The GPU path draws each tile into a canvas of the renderer's, and the frame
// after it shows them without replaying a glyph.
func TestScrollCompositesTilesFromCanvases(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.desk = &desk{tileDraw: tileDrawGPU}
	a.instant = true
	if !a.tileGPU() {
		t.Fatal("the GPU path is not the one in use")
	}
	reader.CountGlyphs(true)
	t.Cleanup(func() { reader.CountGlyphs(false) })
	tt.Frame()
	if len(a.gpu.entry) == 0 {
		t.Fatal("no tile was drawn in a canvas")
	}
	a.paint.ResetGlyphStats()
	tt.Frame()
	if got := a.paint.GlyphStats().Glyphs; got != 0 {
		t.Fatalf("the frame replayed %d glyphs over tiles it had", got)
	}
}

// The setting switches the way every window draws its tiles, and the tiles
// drawn the other way go.
func TestTileDrawSettingSwitchesEveryWindow(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.desk = &desk{tileDraw: tileDrawWorker}
	a.desk.add(a)
	a.instant = true
	tt.Frame()
	if a.tileGPU() {
		t.Fatal("the GPU path is on before the setting says so")
	}
	a.setTileDraw(tileDrawGPU)
	if !a.tileGPU() {
		t.Fatal("the setting did not reach the window")
	}
	if a.tiles.entry != nil || a.gpu.entry != nil {
		t.Fatal("a tile drawn the other way is still there")
	}
}

// The GPU path draws a tile into a canvas and warms it: the worker walks the
// tiles the column is heading for for their glyphs, and never rasterizes one
// into pixels — a screenful of allocations the frame has to collect later, for
// a tile the renderer draws once anyway.
func TestTheGPUpathWarmsAndDrawsNoTile(t *testing.T) {
	a, tt := newScrollWindow(t)
	// The window was drawn before the setting existed, as a window that opened
	// before one was picked is: whatever it started goes, and the frames after
	// it are the ones the test is about.
	if a.tileWork != nil {
		a.tileWork.close()
		a.tileWork = nil
	}
	a.desk = &desk{tileDraw: tileDrawGPU}
	a.instant = true
	a.dropTiles()
	reader.CountGlyphs(true)
	t.Cleanup(func() { reader.CountGlyphs(false) })
	tt.Frame()
	a.scrollTo(a.tileH * 2)
	tt.Frame()
	tt.Frame()
	if len(a.tiles.entry) != 0 {
		t.Fatalf("%d tiles were rastered by a worker that is not the way in use", len(a.tiles.entry))
	}
	if len(a.gpu.entry) == 0 {
		t.Fatal("no tile was drawn in a canvas")
	}
	if a.tileWork == nil || a.tileWork.up == nil {
		t.Skip("no faces to warm with")
	}
	if a.tileWork.up.Covers() == 0 {
		t.Fatal("the worker warmed no glyph, so the frame still rasterizes them")
	}
}
