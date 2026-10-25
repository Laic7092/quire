package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestPerfLogWritesWhatItPromises drives frames through a tester with the log
// on, and reads the file back: a header, a block of frames with the numbers
// this reader is judged by, and the closing totals. It is also the place the
// format is eyeballed.
func TestPerfLogWritesWhatItPromises(t *testing.T) {
	a := benchScrollApp(t)
	path := filepath.Join(t.TempDir(), "perf.log")
	perfStart(perfOptions{
		flag:    true,
		file:    path,
		reading: perfReading(a.opts, a.theme),
	})
	if perf == nil {
		t.Fatal("LAYX_PERF did not start the log")
	}
	// A short glide, as the reader drives it: the column moves, the view is
	// built anew and the window painted.
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
	for i := 0; i < 30; i++ {
		tt.Advance(16 * time.Millisecond)
	}
	perf.event("a test event")
	// The lines a session has besides the frames: a book laid out, and a turn.
	perf.layoutStart(787, 469, true)
	perf.laidOut(412*time.Millisecond, 260*time.Millisecond, 88*time.Millisecond,
		64*time.Millisecond, 186, true)
	perf.turnStart(11, 12)
	for i := 0; i < 9; i++ {
		perf.turnFrame()
	}
	time.Sleep(20 * time.Millisecond)
	perf.turnDone()
	perf.close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	log := string(data)
	t.Logf("the log the reader gets:\n%s", log)
	for _, want := range []string{
		"layx-reader performance log", // the header
		"log file  " + path,
		"first frame: device scale",     // how the frame is drawn
		"frames in",                     // a block
		"cost    build ",                // the numbers a frame is judged by
		"gaps    ",                      // the frame pacing
		"a test event",                  // an event of the reader's own
		"laid out in 412 ms: 186 tiles", // a layout job, split by what it cost
		"page turn 12 -> 13",            // a turn, and how it landed
		"page turn done in",
		"session ended", // the closing totals
		"frames, ",      // how many it saw
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not mention %q:\n%s", want, log)
		}
	}
}

// The frames that only paint are frames of the session too: a glide that builds
// once and paints the rest must show up in the log as the frames they are, with
// nothing built, and named for what they were doing. A log that counted only the
// build passes would report a gesture as a single frame.
func TestPerfLogCountsTheFramesThatOnlyPaint(t *testing.T) {
	a := benchScrollApp(t)
	path := filepath.Join(t.TempDir(), "perf.log")
	perfStart(perfOptions{
		flag:    true,
		file:    path,
		reading: perfReading(a.opts, a.theme),
	})
	if perf == nil {
		t.Fatal("LAYX_PERF did not start the log")
	}
	a.instant = false
	var tt *ui.Tester
	tt = ui.NewTester(func(c *ui.Context) { a.view(c) }, 1180, 800)
	a.now = tt.Now // the glide runs in the tester's frames
	tt.Frame()
	a.scrollBy(300)
	tt.Frame() // the frame the ask arrived in builds
	painted := 0
	for i := 0; i < 120 && a.gliding(); i++ {
		tt.Advance(17 * time.Millisecond)
		painted++
	}
	if a.gliding() {
		t.Fatalf("the glide never landed in %d frames", painted)
	}
	if painted < 2 {
		t.Fatalf("the glide painted %d frames, too few to tell anything", painted)
	}
	perf.close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	log := string(data)
	// The frames of the glide are named for what they were doing, painted ones
	// included: a slow line from one of them reads "while scrolling".
	if !strings.Contains(log, "while scrolling") && !strings.Contains(log, "(406 fps, scrolling)") &&
		!strings.Contains(log, ", scrolling)") {
		t.Errorf("the frames of the glide are not named in the log:\n%s", log)
	}
	// The frames counted are every frame of the session, painted ones included:
	// the frames of the glide alone are more than the ask's and the landing
	// build, so a log that skipped them would count a gesture as two frames.
	m := perfFrames.FindStringSubmatch(log)
	if m == nil {
		t.Fatalf("the log does not total the frames it saw:\n%s", log)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n < painted+2 {
		t.Errorf("the log counted %v frames, fewer than the %d the glide painted:\n%s", m[1], painted, log)
	}
}

// perfFrames finds the closing line's frame count.
var perfFrames = regexp.MustCompile(`(\d+) frames, `)
