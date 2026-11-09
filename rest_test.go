package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/locales"
)

// rest is how long a resting window is watched: much longer than any animation
// this app runs, so a tick that never stops shows up many times over.
const rest = 10 * time.Second

// watchRest settles tt, then moves its clock on in small steps and reports what
// the window built meanwhile. A window that asks for nothing builds nothing:
// the frames of a window that rests are none at all, which is the floor of what
// it can cost the machine.
func watchRest(t *testing.T, name string, tt *ui.Tester, builds *int) {
	t.Helper()
	tt.Frame()
	*builds = 0
	start := tt.Now()
	for tt.Now().Sub(start) < rest {
		tt.Advance(100 * time.Millisecond)
	}
	if *builds != 0 || tt.FramePending() {
		t.Errorf("%s: a window at rest built %d frames and %v asking for another; resting must cost no frame",
			name, *builds, map[bool]string{true: "is", false: "is not"}[tt.FramePending()])
	}
}

// A window resting on a page draws nothing: no frame, no build. This is the
// power floor, and it is what a tick added anywhere would break first. Every
// state that can rest is watched, since a single timer left armed in one of
// them is a core waking up a hundred times a second for a page nobody is
// turning.
func TestRestingWindowDrawsNoFrames(t *testing.T) {
	paged := []struct {
		name string
		set  func(a *app)
	}{
		{"the page", func(*app) {}},
		{"the cover", func(a *app) { a.showCover = true }},
		{"the contents", func(a *app) { a.pan.Show(appstate.SidebarToc) }},
		{"the display settings", func(a *app) { a.aaOpen = true }},
		{"the bar put away", func(a *app) { a.chromeAway = true }},
		{"a message in the folio", func(a *app) { a.say(locales.BookmarkAdded()) }},
	}
	for _, tc := range paged {
		a := newWindow(t)
		builds := 0
		tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1180, 800)
		tt.Frame()
		tc.set(a)
		t.Run(tc.name, func(t *testing.T) { watchRest(t, tc.name, tt, &builds) })
	}

	t.Run("the home screen", func(t *testing.T) {
		a := newWindow(t)
		a.book, a.bookWindow = nil, false
		builds := 0
		tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1180, 800)
		tt.Frame()
		watchRest(t, "the home screen", tt, &builds)
	})

	t.Run("the scrolling column", func(t *testing.T) {
		builds := 0
		_, tt := newScrollWindowView(t, func(c *ui.Context, view func(*ui.Context)) { builds++; view(c) })
		tt.Frame()
		watchRest(t, "the scrolling column", tt, &builds)
	})

	// The column with the bars put away: a pose fading them out is motion, and
	// motion ends. What is measured here is the window after it has landed.
	t.Run("the scrolling column, bar put away", func(t *testing.T) {
		var a *app
		builds := 0
		a, tt := newScrollWindowView(t, func(c *ui.Context, view func(*ui.Context)) { builds++; view(c) })
		a.chromeAway = true
		tt.Frame()
		tt.Advance(500 * time.Millisecond) // the fade, and the frames that run it
		watchRest(t, "the scrolling column, bar put away", tt, &builds)
	})
}
