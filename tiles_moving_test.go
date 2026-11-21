package main

import (
	"testing"
	"time"

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

// The tail of a glide moves the column less than a pixel a frame: the column is
// taken as moving while it glides, and still only after it has been still for
// tileStillDelay.
func TestAGlidesTailIsStillMoving(t *testing.T) {
	a := benchScrollApp(t)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 390, 844)
	tt.SetScale(3)

	at := time.Unix(1000, 0)
	a.now = func() time.Time { return at }
	tt.Frame()

	// A glide, on a frame that moved the column by less than a device pixel —
	// which is what the tail does.
	a.scrollTo(a.scrollY + 40)
	a.scrollY += 0.2
	tt.Frame()
	if !a.tileMoving {
		t.Fatal("a gliding column counts as still")
	}

	// The glide lands, and the frames in between are the ones that used to
	// sharpen.
	a.scrollAt = time.Time{}
	a.flingV = 0
	for i := 0; i < 10; i++ {
		at = at.Add(16 * time.Millisecond)
		tt.Frame()
		if !a.tileMoving {
			t.Fatalf("the column was taken as still %d frames after it stopped", i+1)
		}
	}
	// And once it has stood still, the tiles are drawn again at its ratio.
	at = at.Add(tileStillDelay)
	tt.Frame()
	if a.tileMoving {
		t.Fatal("a column still for longer than tileStillDelay still counts as moving")
	}
}
