package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The monitor measures the frames it shows: what the reader's own pass costs,
// what its draw callback costs, and how long the frames themselves are.
func TestPerfHUDMeasuresTheFrames(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.hud.on = true
	tt.Frame()
	if a.hud.winW == 0 || a.hud.winH == 0 {
		t.Fatalf("the panel does not know the window: %.0fx%.0f", a.hud.winW, a.hud.winH)
	}
	for i := 0; i < 70; i++ {
		tt.Advance(16 * time.Millisecond)
	}
	if a.hud.gap <= 0 {
		t.Error("the panel did not measure a frame")
	}
	if a.hud.buildMs <= 0 {
		t.Error("the panel did not measure a build")
	}
	if a.hud.fps <= 0 {
		t.Error("the panel counted no frames a second")
	}
	if a.hud.scale <= 0 {
		t.Errorf("the panel does not know the screen's ratio: %g", a.hud.scale)
	}
}

// A frame of a glide paints without building: the monitor has to say so, since
// that difference is the whole of what it is for.
func TestPerfHUDSaysWhatAFrameDid(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.instant = false
	a.hud.on = true
	a.scrollBy(600)
	a.services.Invalidate()
	tt.Frame()
	builds := 0
	for i := 0; i < 12; i++ {
		tt.Advance(16 * time.Millisecond)
		if a.hud.built {
			builds++
		}
	}
	if builds > 3 {
		t.Errorf("%d of 12 frames of a glide built the view; the frames of a gesture are meant to paint alone", builds)
	}
}

// The monitor takes no pointer: a tap where it lies is still a tap on the page.
func TestPerfHUDTakesNoPointer(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	a.hud.on = true
	tt.SetSize(390, 844)
	tt.Frame()
	if !a.chromeShown() {
		t.Fatal("the bars are away before the reader touched the page")
	}
	// Where the panel lies on a phone (the right edge, half way down) and where
	// the page reads a tap as "show or hide the bars".
	tt.ClickAt(230, 420)
	if a.chromeShown() {
		t.Error("the tap landed on the monitor instead of the page")
	}
}

// The shortcut shows and hides the monitor, and neither showing it nor hiding
// it costs the reader a rebuild of the book.
func TestPerfHUDShortcutToggles(t *testing.T) {
	a, tt := newScrollWindow(t)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if !a.hud.on {
		t.Fatal("the shortcut did not show the monitor")
	}
	tt.Advance(16 * time.Millisecond)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if a.hud.on {
		t.Fatal("the shortcut did not hide the monitor")
	}
}

// A phone has no key to press: three taps in the middle in quick succession
// open the monitor, and leave the bars where the reader had them.
func TestPerfHUDThreeTapsOnAPhone(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	tt.SetSize(390, 844)
	tt.Frame()
	if a.hud.on {
		t.Fatal("the monitor opened without being asked")
	}
	shown := a.chromeShown()
	for i := 0; i < 2; i++ {
		tt.ClickAt(195, 420)
	}
	if a.hud.on {
		t.Errorf("the monitor opened after two taps")
	}
	tt.ClickAt(195, 420)
	if !a.hud.on {
		t.Error("three taps in the middle did not open the monitor")
	}
	if a.chromeShown() != shown {
		t.Error("the three taps left the bars where they found them")
	}
	tt.ClickAt(195, 420)
	tt.ClickAt(195, 420)
	tt.ClickAt(195, 420)
	if a.hud.on {
		t.Error("three more taps did not close the monitor")
	}
}

// The panel's text is built now and then, so the tiles of the frame it names
// must survive until the next text: counting them for the frames in between
// leaves the row reading zero.
func TestPerfHUDSaysTheTilesOfTheFrame(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.hud.on = true
	a.instant = true
	tt.Frame()
	tt.Frame()
	if a.hud.tiles == 0 {
		t.Fatal("the frame composed no tiles")
	}
	var row string
	for _, l := range a.hud.figures(nil) {
		if strings.Contains(l.text, "tiles ") {
			row = l.text
		}
	}
	if row == "" || strings.HasPrefix(row, "tiles 0 ") {
		t.Fatalf("the panel says %q of %d tiles composed", row, a.hud.tiles)
	}
}
