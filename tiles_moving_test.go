package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A column that moves rasteres its tiles smaller, and the frames after it stops
// draw them again at the screen's own ratio, one a frame.
func TestTilesAreSmallerWhileTheColumnMoves(t *testing.T) {
	a := benchScrollApp(t)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 390, 844)
	tt.SetScale(3)
	tt.Frame()
	if len(a.tiles.entry) == 0 {
		t.Fatal("a still frame built no tile")
	}
	for i, e := range a.tiles.entry {
		if e.ratio != 3 {
			t.Fatalf("a still column rastered tile %d at %v", i, e.ratio)
		}
	}

	a.tiles = tileCache{}
	a.scrollY += 2 * a.tileH
	tt.Frame()
	if len(a.tiles.entry) == 0 {
		t.Fatal("a moving frame built no tile")
	}
	for i, e := range a.tiles.entry {
		if e.ratio != tileMovingScale {
			t.Fatalf("a moving column rastered tile %d at %v", i, e.ratio)
		}
	}

	// Stopped: one a frame is dropped, to be drawn again at the screen's ratio.
	n := len(a.tiles.entry)
	a.tileMoving = false
	a.tiles.tick++
	a.sharpenTiles(0, a.layout.TileCount()-1, 3)
	if len(a.tiles.entry) != n-1 {
		t.Fatalf("a stopped column sharpened %d tiles, want 1", n-len(a.tiles.entry))
	}

	// And none while it moves.
	a.tileMoving = true
	a.sharpenTiles(0, a.layout.TileCount()-1, 3)
	if len(a.tiles.entry) != n-1 {
		t.Fatal("a moving column sharpened a tile")
	}
}
