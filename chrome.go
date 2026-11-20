package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// chromeFade is how long the reading bar and the folio take to go away and to
// come back: short enough to read as the reader's own motion, long enough that
// a single notch of a wheel does not make them blink. It is run as a pose, not
// as a rebuilt value: the bars fade in the frames that only paint, so a glide
// down the column builds nothing to put them away.
const chromeFade = 160 * time.Millisecond

// chromePose is where the bars show: out when they are up, faded away when they
// are not.
func chromePose(shown float32) ui.Pose { return ui.Pose{Fade: 1 - shown} }

// chromeTarget is where the bars are going: away while the reader moves down
// the column, back for anything they are needed for.
func (a *app) chromeTarget() float32 {
	if a.chromeShown() {
		return 1
	}
	return 0
}

// chromeShown reports whether the bars stand out. A scrolling column puts them
// away as the reader moves down it, and a tap in the middle of a phone's page
// puts them away in either mode; anything the reader may need them for brings
// them back — a panel, the display settings, a selection to act on, the cover,
// the note the folio is carrying, or the pointer resting where the bars are.
func (a *app) chromeShown() bool {
	if !a.chromeAway {
		return true
	}
	if a.pan != nil && a.pan.Showing() {
		return true
	}
	if a.aaOpen || a.showCover || len(a.selQuads) > 0 {
		return true
	}
	return a.selectionHint() != "" || a.transientStatus() != ""
}

// chromeFollow tells the bars which way the reader moved down the column: a
// step down puts them away, a step back brings them out again, as a browser's
// toolbar behaves. Only the asks that travel a distance call it — a wheel, a
// key, a screenful — so a jump to a place, as the table of contents makes, comes
// with the bars still up.
func (a *app) chromeFollow(dy float32) {
	switch {
	case dy > 0:
		a.chromeAway = true
	case dy < 0:
		a.chromeAway = false
	}
}
