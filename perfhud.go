package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"
)

// The reader's own performance monitor: a panel at the right edge of the
// window, half way down it, that says frame by frame what the frames cost and
// how much of a frame is the reader's.
//
// It answers the question perf.go writes to a file where the reader can see it:
// on a phone no environment variable can be set and no log can be taken off the
// device, so this is the only instrument there. It measures three things,
// because the difference between them is the diagnosis:
//
//   - build: our own view pass, what a frame that builds the view costs us;
//   - paint: our draw callback, the tiles composited and the marks over them;
//   - rest: what a frame took beyond those two — the engine's layout, commit,
//     draw submission, present and wait for the display.
//
// A frame of 33 ms with a build of 1 ms and a paint of 0.4 ms says the frames
// are not lost in the reader, and sends whoever reads it past the reader for the
// answer. A frame of 33 ms with the build at 30 ms says the opposite. No other
// number in the app can be read that quickly off a phone.
//
// The monitor must not be part of what it measures. Shaping six lines of figures
// into the scene every frame allocated about 9 KB a frame, sixty times a second:
// a heap that grew under the reader and a collection in the middle of a scroll —
// the exact hitch the panel exists to find, made by the panel. So the figures are
// painted into a canvas now and then (hudEvery) and composited every frame after
// that: a frame of the panel is one image, and the text is shaped six times a
// second rather than sixty.

// perfHUDOn, set by QUIRE_PERF_HUD (or LAYX_PERF_HUD), opens the monitor at
// launch. The shortcut toggles it on a desk, three taps in the middle of a
// phone's page on a phone (see handlePageInput).
var perfHUDOn = os.Getenv("QUIRE_PERF_HUD") != "" || os.Getenv("LAYX_PERF_HUD") != ""

// The panel's size, in DIPs, and how often its figures are painted: six lines of
// figures and a strip of the frames before them.
const (
	hudPanelWidth  float32 = 188
	hudPanelHeight float32 = 126
	hudEvery               = 150 * time.Millisecond
	// hudStrip is how many of the frames before this one the panel keeps: about
	// a second of them at a phone's rate.
	hudStrip = 72
)

// hud is the monitor's own state: the frame being measured and the second it
// falls in. The sheet leaves the build, the paint and the tiles of the frame it
// drew, and the panel reads them, draws them and resets them.
type hud struct {
	on bool

	at    time.Time     // when the last frame began, from the painter's clock
	gap   time.Duration // from that frame to this one: the whole frame
	paint time.Duration // our draw callback's cost, this frame
	tiles int           // tile canvases the frame composed
	// tileWay says where they were rastered: "cpu" by the window's worker, "gpu"
	// by the renderer (see tiles.go).
	tileWay string
	built   bool // this frame built the view

	buildAt  time.Time     // when the build pass under way began
	buildSum time.Duration // what this frame's build passes cost together

	// The second the frames are counted in, and the figures the second before
	// it left. A mean taken over a second can be read while it moves; a figure
	// of one frame flickers too much to be.
	winAt     time.Time
	frames    int
	builds    int
	missed    int
	worst     time.Duration
	shortest  time.Duration
	fps       float32
	buildRate float32
	worstGap  time.Duration
	missRate  int
	interval  time.Duration // one refresh of the display, as the frames tell it
	buildMs   float32
	paintMs   float32
	restMs    float32

	// The strip: the last frames' gaps, in milliseconds, as a ring.
	strip   [hudStrip]float32
	stripAt int

	// The heap and the collections, read now and then: ReadMemStats stops the
	// world, so it is not something every frame does.
	heap     float32
	gcs      uint32
	sampleAt time.Time

	winW, winH float32
	scale      float32

	// The panel's pixels, painted when the figures move (hudEvery) and
	// composited every frame after that: cvW and cvH are its size in pixels.
	cv       *ui.Canvas
	cvAt     time.Time
	cvW, cvH int
	lines    []hudLine
	// stripMs is the strip the canvas holds, and stripN how many frames it was
	// taken after: they move with the canvas, not with the frames it is
	// composited in.
	stripMs [hudStrip]float32
	stripN  int
}

// hudLine is one run of the panel's text: a row of the canvas, how far in from
// the panel's left edge it starts, and its ink.
type hudLine struct {
	row   int
	dx    float32 // DIPs from the panel's left edge
	text  string
	color ui.Color
}

// beginBuild marks the start of a frame's build pass.
func (h *hud) beginBuild() {
	if h.on {
		h.buildAt = time.Now()
	}
}

// endBuild adds the pass that just ran to the frame's build.
func (h *hud) endBuild() {
	if h.on && !h.buildAt.IsZero() {
		h.buildSum += time.Since(h.buildAt)
		h.buildAt = time.Time{}
	}
}

// frameDone is what the sheet tells the panel: whether the frame it just drew
// built the view or only painted it, what its build cost, and how many tiles it
// put on the screen.
func (h *hud) frameDone(built bool, paint time.Duration, tiles int) {
	if !h.on {
		return
	}
	h.built, h.paint, h.tiles = built, paint, tiles
}

// reset forgets what was measured, as the panel opens, and with it the canvas
// that was painted for it: the figures of a window that is no longer there must
// not come back.
func (h *hud) reset() { *h = hud{on: true} }

// observe takes this frame's gap into the second's figures and the strip.
func (h *hud) observe(now time.Time) {
	gap := now.Sub(h.at)
	h.gap = gap
	h.frames++
	h.worst = max(h.worst, gap)
	if h.shortest == 0 || gap < h.shortest {
		h.shortest = gap
	}
	if h.interval > 0 && gap > h.interval*3/2 {
		// A frame that took half a refresh longer than one did missed its turn:
		// the display showed the frame before it again.
		h.missed++
	}
	h.strip[h.stripAt%len(h.strip)] = msf(gap)
	h.stripAt++
	d := now.Sub(h.winAt)
	if d < time.Second || h.frames < 2 {
		return
	}
	secs := float32(d.Seconds())
	h.fps = float32(h.frames) / secs
	h.worstGap = h.worst
	h.buildRate = float32(h.builds) / secs
	h.missRate = h.missed
	if iv, ok := hintInterval(h.shortest); ok {
		// The shortest frame of the second is the display's own interval, unless
		// the window never kept up with it: what a frame that is not waiting for
		// the display costs is then what it took.
		h.interval = iv
	}
	h.winAt, h.frames, h.builds, h.missed, h.worst, h.shortest = now, 0, 0, 0, 0, 0
}

// draw paints the panel: the canvas of figures, composited, and repainted now
// and then from what the frames did.
func (a *app) drawHUD(p *ui.Painter, r ui.Rect) {
	h := &a.hud
	if !h.on || p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	now := p.Now()
	if !h.at.IsZero() {
		if gap := now.Sub(h.at); gap > 0 {
			h.observe(now)
		}
	} else {
		h.winAt = now
	}
	h.at = now
	h.scale = p.Scale()
	// What the sheet left is the frame this panel is painted for: the sheet
	// painted before the panel did.
	build, paint, built := h.buildSum, h.paint, h.built
	// tiles is not cleared here: the panel's text is built from it now and then,
	// not every frame, and the next frame reports it again.
	h.buildSum, h.paint, h.built = 0, 0, false
	if built {
		h.builds++
		h.buildMs = blend(h.buildMs, msf(build))
	}
	h.paintMs = blend(h.paintMs, msf(paint))
	h.restMs = blend(h.restMs, msf(max(h.gap-build-paint, 0)))
	if now.Sub(h.sampleAt) > 500*time.Millisecond {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		h.heap = float32(m.HeapAlloc) / (1 << 20)
		h.gcs = m.NumGC
		h.sampleAt = now
	}
	// The figures are painted into the panel's canvas now and then, and the
	// canvas is put on the screen every frame: a frame of the panel is one image,
	// where shaping the text again every frame was 9 KB of garbage a frame.
	scale := p.Scale()
	w, hgt := int(r.W*scale+0.5), int(r.H*scale+0.5)
	if h.cv == nil || h.cvW != w || h.cvH != hgt || now.Sub(h.cvAt) >= hudEvery {
		if h.cv == nil || h.cvW != w || h.cvH != hgt {
			h.cv, h.cvW, h.cvH = ui.NewCanvas(w, hgt), w, hgt
		}
		h.lines = h.figures(h.lines[:0])
		h.cvAt = now
		h.cv.Draw(p, func(cp *ui.Painter) {
			h.paintPanel(cp, float32(w), float32(hgt), scale)
		})
	}
	if h.cv != nil {
		p.Image(h.cv.Bitmap(), r, ui.FillBox)
	}
	// A monitor nobody paints again shows the frame it opened on: the panel asks
	// for the next frame itself, as a drawing that moves does, and the frames it
	// keeps coming are the frames it measures.
	p.AnimationFrame()
}

// figures is the panel's text as the frames have left it, row by row.
func (h *hud) figures(out []hudLine) []hudLine {
	muted := ui.RGBA(146, 155, 166, 1)
	text := ui.RGBA(228, 232, 238, 1)
	okC := ui.RGBA(118, 202, 140, 1)
	warnC := ui.RGBA(232, 190, 92, 1)
	badC := ui.RGBA(236, 108, 98, 1)
	gapC := okC
	// A frame is late against the display's own interval, which the frames before
	// it told: 16.7 ms of a 60 Hz screen is a frame that waited, not one that
	// worked.
	budget := h.interval
	if budget <= 0 {
		budget = 16 * time.Millisecond
	}
	switch {
	case h.gap > budget*3/2:
		gapC = badC
	case h.gap > budget*11/10:
		gapC = warnC
	}
	restC := muted
	if h.gap > budget*3/2 {
		restC = badC
	}
	row := func(r int, s string, c ui.Color, dx ...float32) []hudLine {
		x := float32(0)
		if len(dx) > 0 {
			x = dx[0]
		}
		return append(out, hudLine{row: r, dx: x, text: s, color: c})
	}
	out = row(0, fmt.Sprintf("%.0f fps", h.fps), gapC)
	out = row(0, fmt.Sprintf("%.1f ms", msf(h.gap)), gapC, 46)
	out = row(0, fmt.Sprintf("max %.0f", msf(h.worstGap)), muted, 110)
	out = row(1, fmt.Sprintf("build %.2f  paint %.2f", h.buildMs, h.paintMs), text)
	out = row(2, fmt.Sprintf("rest %.1f  (%s)", h.restMs, h.rateName()), restC)
	missC := muted
	if h.missRate > 0 {
		missC = warnC
	}
	out = row(3, fmt.Sprintf("built %.0f/s  miss %d/s", h.buildRate, h.missRate), missC)
	out = row(4, fmt.Sprintf("tiles %d %s  dpr %.2f  %.0fx%.0f", h.tiles, h.tileWay, h.scale, h.winW, h.winH), text)
	out = row(5, fmt.Sprintf("heap %.0f MB  gc %d", h.heap, h.gcs), muted)
	// The strip the canvas is about to hold, taken now: it moves with the canvas,
	// not with the frames it is composited in.
	copy(h.stripMs[:], h.strip[:])
	h.stripN = h.stripAt
	return out
}

// rateName is the display's rate as the frames before this one told it: the
// shortest gap of a second is one refresh of the screen, and what the frames
// after it took is measured against it.
func (h *hud) rateName() string {
	if h.interval <= 0 {
		return "rate unknown"
	}
	return fmt.Sprintf("%3.0f Hz", float64(time.Second)/float64(h.interval))
}

// paintPanel paints the panel's canvas: its background, the figures, and the
// strip of the frames before them. Everything is in the canvas's own pixels, so
// the figures are shaped at the screen's ratio and the panel is as sharp as the
// text under it.
func (h *hud) paintPanel(p *ui.Painter, w, hgt, scale float32) {
	okC := ui.RGBA(118, 202, 140, 1)
	warnC := ui.RGBA(232, 190, 92, 1)
	badC := ui.RGBA(236, 108, 98, 1)
	p.Fill(ui.Rect{W: w, H: hgt}, ui.RGBA(14, 16, 18, 0.86), 8*scale)
	const size, lead float32 = 10.5, 13
	for _, ln := range h.lines {
		y := (7 + float32(ln.row)*lead) * scale
		p.Text((9+ln.dx)*scale, y, ln.text, size*scale, ln.color)
	}
	// The frames before this one as bars, as tall as their gaps: a hitch shows as
	// a spike, where a mean would hide it.
	x := 9 * scale
	wid := w - 18*scale
	top := (7 + 6*lead + 1) * scale
	bars := hgt - top - 7*scale
	if bars < 4*scale {
		return
	}
	base := top + bars
	n := len(h.stripMs)
	pitch := wid / float32(n)
	hi := float32(34)
	for _, v := range h.stripMs {
		hi = max(hi, v)
	}
	p.Fill(ui.Rect{X: x, Y: base, W: wid, H: max(scale, 1)}, ui.RGBA(255, 255, 255, 0.10), 0)
	for i := 0; i < n; i++ {
		v := h.stripMs[(h.stripN+i)%n]
		if v <= 0 {
			continue
		}
		c := okC
		switch {
		case v >= 34:
			c = badC
		case v >= 20:
			c = warnC
		}
		bh := min(v/hi, 1) * bars
		p.Fill(ui.Rect{X: x + float32(i)*pitch, Y: base - bh, W: max(pitch-1, 1), H: bh}, c, 0)
	}
}

// blend is v moved a third of the way to x: a mean that follows the frames
// without flickering with each of them, and takes x while it has nothing yet.
func blend(v, x float32) float32 {
	if v == 0 {
		return x
	}
	return v + (x-v)/3
}

// msf is d in milliseconds.
func msf(d time.Duration) float32 { return float32(d.Microseconds()) / 1000 }

// perfHUD builds the monitor: the panel at the right edge of the window, half
// way down it, over whatever the window shows and taking no pointer, so a tap
// where it lies still reaches the page.
func (a *app) perfHUD(c *ui.Context) {
	if !a.hud.on {
		return
	}
	a.hud.winW, a.hud.winH = c.Size()
	ui.Column(c).Absolute().Right(6).Top(0).Bottom(0).Width(hudPanelWidth).
		Center().PassThrough().Children(func() {
		ui.Box(c).Width(hudPanelWidth).Height(hudPanelHeight).PassThrough().Draw(a.drawHUD)
	})
}

// togglePerfHUD shows or hides the monitor.
func (a *app) togglePerfHUD() {
	if a.hud.on {
		a.hud.on = false
	} else {
		a.hud.reset()
	}
	a.services.Invalidate()
}

// hintInterval snaps a frame gap to the refresh rate it was drawn at, as perf.go
// snaps the gaps it reports: a display's interval is a fixed number of
// milliseconds, and the shortest frame of a second is that number however the
// frames after it fell. A gap no rate explains is the interval itself, for a
// display none of them name.
func hintInterval(gap time.Duration) (time.Duration, bool) {
	if gap <= 0 {
		return 0, false
	}
	for _, hz := range perfHints {
		d := time.Duration(float64(time.Second) / float64(hz))
		if gap >= d*88/100 && gap <= d*112/100 {
			return d, true
		}
	}
	return gap, true
}
