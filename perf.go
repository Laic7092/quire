package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/laix/quire/internal/reader"
)

// The performance log answers "what does reading cost on this machine?" in a
// form a person reads, not a profiler: a header naming the session, a block
// every couple of seconds with what the frames cost, a line for every frame
// that missed the display's budget, and a line for every event that made the
// reader work — opening a book, laying it out, changing a setting, finishing a
// turn.
//
// The numbers here are the ones the engine cannot know: the app's build pass,
// its draw callback, the tiles it painted, the glyphs it uploaded, and the jobs
// it ran off the UI thread. MyGo's own MYGO_FRAME_STATS covers the phases
// around them (the native layout and the present); its lines land in the same
// file when the reader asks for both.
//
// It is off unless LAYX_PERF says otherwise, and the whole logger hangs off
// one pointer: every method works on a nil log, so the code that measures
// nothing pays one call and one branch.

// perf is the session's log, nil while the reader did not ask for one.
var perf *perfLog

// The defaults the log runs with; LAYX_PERF_SLOW, LAYX_PERF_EVERY and
// LAYX_PERF_HZ override them.
const (
	// perfSlowFrame is the cost at which a frame earns a line of its own. It is
	// under one frame at 120 Hz, so a machine that misses its budget says so.
	perfSlowFrame = 8 * time.Millisecond
	// perfBlock is how often the frames are summed up, which trades the size of
	// the log against how sharply it shows a slow few seconds.
	perfBlock = 2 * time.Second
	// perfTileRoom is how many tile times a frame keeps for its slow line.
	perfTileRoom = 16
)

// perfHints are the rates a measured frame gap is matched against: the display
// rates a reader's machine plausibly runs at.
var perfHints = []float32{30, 50, 60, 75, 90, 100, 120, 144, 165, 240}

// The runtime's counters a frame is judged by, read once when it ends: the
// frame's share of them is what it allocated and what the collector did for it.
// The CPU time it cost comes from perfCPU instead: the runtime's own
// /cpu/classes counters are updated in lumps, which would put minutes of work
// on the frame that happened to trigger a collection.
const (
	perfAlloc = iota
	perfCycles
	perfAssist
	perfSamples
)

// perfCounterNames are the samples above, in the runtime's own spelling.
var perfCounterNames = [perfSamples]string{
	"/gc/heap/allocs:bytes",
	"/gc/cycles/total:gc-cycles",
	"/cpu/classes/gc/mark/assist:cpu-seconds",
}

// perfLog is one session's log. Its mutex guards the file and every counter:
// the frames come from the UI thread, the layout jobs from a goroutine of their
// own, and both write here.
type perfLog struct {
	mu   sync.Mutex
	f    *os.File
	w    io.Writer
	path string

	slow   time.Duration // the cost at which a frame gets its own line
	every  time.Duration // how often a block is written
	hz     float32       // the display's rate, 0 while unknown
	guess  bool          // whether hz was measured here rather than given
	budget time.Duration // one frame at that rate, 0 while unknown
	sess   time.Time     // when the log began

	// The frame under way: the build pass fills frameAt and build, the draw
	// callback paint, the tiles and the glyphs, and paintEnd adds them up.
	inFrame bool
	frameAt time.Time
	build   time.Duration
	paintAt time.Time
	paint   time.Duration
	painted bool
	scene   string
	records time.Duration // time spent writing the reading position down
	prev    time.Time     // when the previous frame began, for the gap
	samples [perfSamples]metrics.Sample
	last    [perfSamples]float64
	cpu     int64 // the process's CPU time as of the last frame
	started bool  // whether last holds a reading

	// What this frame's painter did, filled by the draw callback.
	tiles       [perfTileRoom]time.Duration
	tileIdx     [perfTileRoom]int
	tilesN      int
	glyphUp     int // atlas uploads the frame caused
	glyphRaster int // glyphs it rasterized
	glyphCover  int // glyphs a warmer had already rasterized

	// One block's worth of frames, summed when it is written.
	blockAt    time.Time
	n          int
	gaps       []time.Duration
	builds     []time.Duration
	paints     []time.Duration
	costs      []time.Duration
	tilesSum   int
	uploads    int
	rasters    int
	lookups    int
	hits       int
	covers     int
	recordsSum time.Duration
	cpuSum     time.Duration
	scenes     map[string]int
	scrolls    int
	precise    int
	scrollDy   float64
	glides     int
	allocs     int64
	cycles     float64
	assist     time.Duration

	// What the glyph counters said at the last frame, so the frame's share of
	// them is a subtraction.
	glyphs reader.GlyphStats

	// The session's totals, for the closing line.
	frames     int
	slowFrames int
	overBudget int
	costSum    time.Duration
	worstCost  time.Duration
	worstAt    time.Time
	worstScene string

	spoke   bool // whether the first frame has been described
	turnAt  time.Time
	turnN   int
	profile *os.File
}

// perfOptions is what the reader tells the log before the window opens.
type perfOptions struct {
	flag      bool   // the -perf flag
	env       string // LAYX_PERF
	file      string // LAYX_PERF_FILE
	statePath string // the state file, whose directory holds the log by default
	reading   string // the reading settings, as the header prints them
	font      string // the face the reader opened
}

// perfStart opens the log the reader asked for and writes its header. It does
// nothing, and costs nothing after that, when the reader did not ask.
func perfStart(o perfOptions) {
	on, path := perfSetting(o)
	if !on {
		return
	}
	p := &perfLog{
		slow:   perfSlowFrame,
		every:  perfBlock,
		sess:   time.Now(),
		scenes: map[string]int{},
	}
	if v, ok := perfFloat("LAYX_PERF_SLOW"); ok && v >= 0 {
		p.slow = time.Duration(v * float64(time.Millisecond))
	}
	if v, ok := perfFloat("LAYX_PERF_EVERY"); ok && v > 0 {
		p.every = time.Duration(v * float64(time.Second))
	}
	if v, ok := perfFloat("LAYX_PERF_HZ"); ok && v > 0 {
		p.hz = float32(v)
		p.budget = time.Duration(float64(time.Second) / v)
	}
	for i, name := range perfCounterNames {
		p.samples[i].Name = name
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if f, err := os.Create(path); err != nil {
		// A log that cannot be written is worth saying out loud, and worth
		// keeping the session measurable: the console gets it instead.
		log.Printf("quire: performance log %s: %v (writing it to the console instead)", path, err)
		p.w = os.Stderr
	} else {
		p.f, p.w, p.path = f, f, path
		// MyGo logs its GPU fallback and its slow frames to the standard
		// logger: they belong in the same file, on the same clock.
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	perf = p
	// The counters are what tell whether a frame uploaded a layer of the atlas,
	// which is exactly what a real machine notices and a benchmark does not.
	reader.CountGlyphs(true)
	p.header(o)

	if name := strings.TrimSpace(os.Getenv("LAYX_CPU_PROFILE")); name != "" {
		if f, err := os.Create(name); err != nil {
			p.event("no CPU profile: %v", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			p.event("no CPU profile: %v", err)
			_ = f.Close()
		} else {
			p.profile = f
			p.event("a CPU profile goes to %s: go tool pprof reads it after the session", name)
		}
	}
}

// perfSetting says whether the log was asked for and where it goes: the -perf
// flag, LAYX_PERF with 1/on/yes for the default place and anything else for a
// file of its own, and LAYX_PERF_FILE names one either way.
func perfSetting(o perfOptions) (bool, string) {
	on, path := o.flag, ""
	if on {
		path = strings.TrimSpace(o.env)
	} else {
		switch v := strings.ToLower(strings.TrimSpace(o.env)); v {
		case "", "0", "false", "off", "no":
		case "1", "true", "on", "yes":
			on = true
		default:
			on, path = true, strings.TrimSpace(o.env)
		}
	}
	if f := strings.TrimSpace(o.file); f != "" {
		on, path = true, f
	}
	if !on {
		return false, ""
	}
	switch strings.ToLower(path) {
	case "", "1", "true", "on", "yes":
		path = filepath.Join(perfDir(o.statePath), "perf-"+time.Now().Format("20060102-150405")+".log")
	}
	return true, path
}

// perfDir is where the log goes by default: beside the state file, and the
// temporary directory when the reader has no state file to sit next to.
func perfDir(statePath string) string {
	if statePath != "" {
		return filepath.Dir(statePath)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "quire")
	}
	return os.TempDir()
}

// perfFloat reads a number the environment holds, if it holds one.
func perfFloat(name string) (float64, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Printf("quire: %s=%q is not a number: ignored", name, v)
		return 0, false
	}
	return n, true
}

// header writes what the session is: the machine, the reader's settings, and
// how to read the lines that follow.
func (p *perfLog) header(o perfOptions) {
	rule := strings.Repeat("=", 78)
	p.line(rule)
	p.line(" quire performance log")
	p.line(strings.Repeat("-", 78))
	p.line(" session   %s", p.sess.Format("2006-01-02 15:04:05"))
	p.line(" platform  %s/%s, %s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU())
	p.line(" reading   %s", o.reading)
	if o.font != "" {
		p.line(" font      %s", o.font)
	}
	p.line(" watch     a frame over %s ms gets a line of its own, a block every %s",
		perfMS(p.slow), perfSpan(p.every))
	if p.budget > 0 {
		p.line(" budget    %s ms a frame, at the %g Hz LAYX_PERF_HZ names", perfMS(p.budget), p.hz)
	} else {
		p.line(" budget    measured from the frames themselves (a display's refresh caps them)")
	}
	p.line("")
	p.line(" A block sums the frames between two of these lines. \"cost\" is the app's own")
	p.line(" work: build is the view it makes each frame, paint is its draw callback. The")
	p.line(" gap is one frame to the next, which the display's refresh rate caps, so a gap")
	p.line(" of two budgets is a frame the reader never got. \"budget\" counts the frames")
	p.line(" whose own cost did not fit, which is the app's fault; the gaps say how it read.")
	p.line(" The frames measured are the reading view's, which paints the window itself;")
	p.line(" opening a book, laying it out and changing a setting show up as events.")
	if p.path != "" {
		p.line("")
		p.line(" log file  %s", p.path)
	}
	p.line(rule)
}

// event writes a line about something the reader did, rather than about a
// frame: a book opened, a layout finished, a setting changed.
func (p *perfLog) event(format string, args ...any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.line("[%s] %s", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// line writes one line, with the lock held by its caller.
func (p *perfLog) line(format string, args ...any) {
	fmt.Fprintf(p.w, format+"\n", args...)
}

// frameBegin starts the clock on a frame's build pass.
func (p *perfLog) frameBegin() {
	if p == nil {
		return
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFrame, p.painted = true, false
	p.frameAt = now
	p.paintAt = time.Time{}
	p.build, p.paint, p.records = 0, 0, 0
	p.tilesN, p.glyphUp, p.glyphRaster = 0, 0, 0
	p.scene = ""
	for i := range p.tiles {
		p.tiles[i] = 0
	}
	if p.blockAt.IsZero() {
		p.blockAt = now
	}
}

// buildDone ends the build pass, so what the frame spends after it is MyGo's
// own layout and commit rather than the reader's.
func (p *perfLog) buildDone() {
	if p == nil {
		return
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inFrame {
		p.build = now.Sub(p.frameAt)
	}
}

// frameScene names what the frame was doing, which is what makes a slow line
// readable six months later.
func (p *perfLog) frameScene(name string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scene = name
}

// paintBegin starts the clock on the draw callback.
func (p *perfLog) paintBegin() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paintAt = time.Now()
}

// paintEnd ends the draw callback and records the frame. gs is the painter's
// counters as of now, which the caller reads after flushing the glyphs, so the
// uploads it sees are the frame's.
func (p *perfLog) paintEnd(gs reader.GlyphStats) {
	if p == nil {
		return
	}
	now := time.Now()
	counters := p.read()
	p.mu.Lock()
	defer p.mu.Unlock()
	// The draw callback may run more than once for one build pass; the frame is
	// the build pass, so only its first paint counts.
	if !p.inFrame || p.painted {
		return
	}
	p.painted = true
	if !p.paintAt.IsZero() {
		p.paint = now.Sub(p.paintAt)
	}

	var alloc int64
	var cycles float64
	var assist time.Duration
	cpu := perfCPU()
	if p.started {
		p.cpuSum += time.Duration(cpu - p.cpu)
		alloc = int64(counters[perfAlloc] - p.last[perfAlloc])
		cycles = counters[perfCycles] - p.last[perfCycles]
		assist = time.Duration((counters[perfAssist] - p.last[perfAssist]) * float64(time.Second))
		p.glyphUp = gs.Uploads - p.glyphs.Uploads
		p.glyphRaster = gs.Rasters - p.glyphs.Rasters
		p.glyphCover = gs.CoverHits - p.glyphs.CoverHits
		p.uploads += p.glyphUp
		p.rasters += p.glyphRaster
		p.covers += p.glyphCover
		p.lookups += gs.Lookups - p.glyphs.Lookups
		p.hits += gs.Hits - p.glyphs.Hits
	}
	p.last, p.cpu, p.started, p.glyphs = counters, cpu, true, gs

	gap := time.Duration(0)
	if !p.prev.IsZero() {
		gap = p.frameAt.Sub(p.prev)
	}
	p.prev = p.frameAt
	cost := p.build + p.paint

	p.frames++
	p.costSum += cost
	if cost > p.worstCost {
		p.worstCost, p.worstAt, p.worstScene = cost, now, p.scene
	}
	p.n++
	p.gaps = append(p.gaps, gap)
	p.builds = append(p.builds, p.build)
	p.paints = append(p.paints, p.paint)
	p.costs = append(p.costs, cost)
	p.tilesSum += p.tilesN
	p.recordsSum += p.records
	p.allocs += alloc
	p.cycles += cycles
	p.assist += assist
	p.scenes[p.scene]++

	if p.budget > 0 && cost > p.budget {
		p.overBudget++
	}
	if p.slow > 0 && cost >= p.slow {
		p.slowFrames++
		p.slowLine(now, gap, cost, alloc, cycles, assist)
	}
	if time.Since(p.blockAt) >= p.every {
		p.summary(now)
	}
}

// tileAt runs the painting of tile i and notes what it took. The slow-frame
// line keeps these: a tile built on the frame it is first shown is the shape a
// scroll stutter has, and the index says which one it was.
func (p *perfLog) tileBegin(i int) func() {
	if p == nil {
		return nil
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.tilesN < len(p.tiles) {
			p.tiles[p.tilesN], p.tileIdx[p.tilesN] = d, i
		}
		p.tilesN++
	}
}

// record notes the time the frame spent writing the reading position down,
// which is the reader's own cost inside the build pass.
func (p *perfLog) record(d time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = d
}

// scrolled notes one wheel or touchpad event, and how far it asked the column
// to go. A trackpad sends pixel-sized events; a wheel sends notches.
func (p *perfLog) scrolled(dy float32, precise bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scrolls++
	p.scrollDy += float64(dy)
	if precise {
		p.precise++
	}
}

// glide notes a column that was sent somewhere while it was already moving,
// which is the case that makes a touchpad read as lagging.
func (p *perfLog) glide() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.glides++
}

// turnStart notes a page turn beginning.
func (p *perfLog) turnStart(from, to int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnAt, p.turnN = time.Now(), 0
	p.line("[%s] page turn %d -> %d", time.Now().Format("15:04:05.000"), from+1, to+1)
}

// turnFrame counts one frame drawn while a turn is in flight.
func (p *perfLog) turnFrame() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnN++
}

// turnDone notes a turn landing, with how long it took and over how many
// frames: a reading turn that should take 240 ms and takes 300 is the frame
// budget being missed, not the animation being wrong. A run of turns from a
// held key lands every 90 ms, which is the short slide, not a mistimed one.
func (p *perfLog) turnDone() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.turnAt.IsZero() {
		return
	}
	p.line("[%s] page turn done in %s ms over %d frames",
		time.Now().Format("15:04:05.000"), perfMS(time.Since(p.turnAt)), p.turnN)
	p.turnAt = time.Time{}
}

// layoutStart notes a layout job beginning, with the sheet it is laying out on.
func (p *perfLog) layoutStart(sw, sh float32, scroll bool) {
	if p == nil {
		return
	}
	p.event("laying out: sheet %.0fx%.0f, %s", sw, sh, perfMode(scroll))
}

// laidOut notes a layout job that landed, and where its time went.
func (p *perfLog) laidOut(total, doc, index, layout time.Duration, count int, scroll bool) {
	if p == nil {
		return
	}
	unit := "pages"
	if scroll {
		unit = "tiles"
	}
	p.event("laid out in %s: %d %s (document %s ms, search %s ms, layout %s ms)",
		perfSpan(total), count, unit, perfMS(doc), perfMS(index), perfMS(layout))
}

// firstPaint notes how the frame is being drawn, once: on a real machine this
// is the line that says whether the GPU was there at all.
func (p *perfLog) firstPaint(scale float32, accelerated bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.spoke {
		return
	}
	p.spoke = true
	path := "the software rasterizer"
	if accelerated {
		path = "the GPU"
	}
	p.line("[%s] first frame: device scale %.2f, drawing on %s",
		time.Now().Format("15:04:05.000"), scale, path)
}

// slowLine writes a frame that cost more than the reader asked to hear about.
func (p *perfLog) slowLine(now time.Time, gap, cost time.Duration, alloc int64, cycles float64, assist time.Duration) {
	if gap > 0 {
		p.line("[%s] slow frame %s ms (build %s + paint %s) while %s, gap %s ms",
			now.Format("15:04:05.000"), perfMS(cost), perfMS(p.build), perfMS(p.paint), perfOr(p.scene), perfMS(gap))
	} else {
		p.line("[%s] slow frame %s ms (build %s + paint %s) while %s",
			now.Format("15:04:05.000"), perfMS(cost), perfMS(p.build), perfMS(p.paint), perfOr(p.scene))
	}
	if p.tilesN > 0 {
		shown := min(p.tilesN, len(p.tiles))
		tiles := make([]string, 0, shown)
		for i := 0; i < shown; i++ {
			tiles = append(tiles, fmt.Sprintf("#%d %s", p.tileIdx[i], perfMS(p.tiles[i])))
		}
		more := ""
		if p.tilesN > shown {
			more = fmt.Sprintf(", and %d more", p.tilesN-shown)
		}
		p.line("    tiles   %s ms%s", strings.Join(tiles, " / "), more)
	}
	if p.glyphRaster > 0 || p.glyphUp > 0 || p.glyphCover > 0 {
		p.line("    glyphs  %d rasterized, %d from a warmer, %d uploads this frame",
			p.glyphRaster, p.glyphCover, p.glyphUp)
	}
	if alloc > 0 || assist > 0 {
		p.line("    memory  %s allocated, %s, %s ms of GC assist",
			perfBytes(alloc), perfCount(int(cycles), "GC cycle", "GC cycles"), perfMS(assist))
	}
}

// summary writes the block of frames since the last one: what the frames cost,
// what they missed, and what the app did besides paint them.
func (p *perfLog) summary(now time.Time) {
	if p.n == 0 {
		p.blockAt = now
		return
	}
	span := now.Sub(p.blockAt)
	rate := float64(p.n) / span.Seconds()
	p.guessRate()

	p.line("")
	p.line("[%s] %d frames in %s (%.0f fps, %s)",
		now.Format("15:04:05"), p.n, perfSpan(span), rate, perfMost(p.scenes))
	p.line("    cost    build %s   paint %s   (p50/p95/max)", perfSpread(p.builds), perfSpread(p.paints))
	if p.budget > 0 {
		over := 0
		for _, d := range p.costs {
			if d > p.budget {
				over++
			}
		}
		how := "LAYX_PERF_HZ"
		if p.guess {
			how = "measured"
		}
		p.line("    budget  %s ms a frame (%s): %d frames (%d%%) took longer",
			perfMS(p.budget), how, over, 100*over/p.n)
		p.line("    gaps    %s   (p50/p95/max; a gap of two budgets is a lost frame)", perfSpread(p.gaps))
	} else {
		p.line("    gaps    %s   (p50/p95/max)", perfSpread(p.gaps))
	}
	if p.lookups > 0 {
		p.line("    glyphs  %.1f uploads a frame, %d rasterized, %d from a warmer, %.1f%% came from the atlas",
			float64(p.uploads)/float64(p.n), p.rasters, p.covers, 100*float64(p.hits)/float64(p.lookups))
	}
	p.line("    tiles   %.1f a frame", float64(p.tilesSum)/float64(p.n))
	if p.cpuSum > 0 {
		p.line("    cpu     %s ms a frame on every thread (including MyGo's rasterizing)",
			perfMS(p.cpuSum/time.Duration(p.n)))
	}
	p.line("    memory  %s allocated a frame, %s, %s ms of GC assist",
		perfBytes(p.allocs/int64(p.n)), perfCount(int(p.cycles), "GC cycle", "GC cycles"), perfMS(p.assist))
	if p.scrolls > 0 || p.glides > 0 {
		trackpad := ""
		if p.precise > 0 {
			trackpad = fmt.Sprintf(", %d of them smooth", p.precise)
		}
		p.line("    input   %d scroll events (%s px%s), %d glide restarts",
			p.scrolls, perfNum(p.scrollDy), trackpad, p.glides)
	}
	if p.recordsSum > 0 {
		p.line("    store   %s ms a frame writing the reading position",
			perfMS(p.recordsSum/time.Duration(p.n)))
	}
	p.restart(now)
}

// guessRate works out the display's rate from the fastest tenth of the block's
// gaps: only a frame the reader drew as fast as the display allows shows it.
// A window that never animates never reveals one, so the budget stays unknown
// until it does.
func (p *perfLog) guessRate() {
	if p.hz > 0 || len(p.gaps) < 8 {
		return
	}
	s := append([]time.Duration(nil), p.gaps...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	gap := s[(len(s)-1)/10]
	if gap <= 0 {
		return
	}
	rate := float32(float64(time.Second) / float64(gap))
	best, off := float32(0), float32(0.08)
	for _, r := range perfHints {
		if d := perfAbsf(rate-r) / r; d < off {
			best, off = r, d
		}
	}
	if best == 0 {
		return
	}
	p.hz, p.guess = best, true
	p.budget = time.Duration(float64(time.Second) / float64(best))
}

// restart clears the block's sums.
func (p *perfLog) restart(now time.Time) {
	p.blockAt = now
	p.n = 0
	p.gaps, p.builds, p.paints, p.costs = p.gaps[:0], p.builds[:0], p.paints[:0], p.costs[:0]
	p.tilesSum, p.uploads, p.rasters, p.lookups, p.hits, p.covers = 0, 0, 0, 0, 0, 0
	p.recordsSum, p.allocs, p.cycles, p.assist, p.cpuSum = 0, 0, 0, 0, 0
	p.scrolls, p.precise, p.scrollDy, p.glides = 0, 0, 0, 0
	clear(p.scenes)
}

// close writes the session's totals, stops the profile and lets the console
// have its log back.
func (p *perfLog) close() {
	if p == nil {
		return
	}
	now := time.Now()
	p.mu.Lock()
	p.summary(now)
	rule := strings.Repeat("=", 78)
	p.line(rule)
	p.line(" session ended %s, after %s", now.Format("2006-01-02 15:04:05"), perfSpan(now.Sub(p.sess)))
	if p.frames > 0 {
		mean := p.costSum / time.Duration(p.frames)
		p.line("   %s, %s ms a frame on average", perfCount(p.frames, "frame", "frames"), perfMS(mean))
		if p.worstCost > 0 {
			p.line("   the worst cost %s ms, while %s at %s",
				perfMS(p.worstCost), perfOr(p.worstScene), p.worstAt.Format("15:04:05"))
		}
		if p.budget > 0 {
			p.line("   %d frames (%d%%) did not fit the %s ms budget at about %g Hz",
				p.overBudget, 100*p.overBudget/p.frames, perfMS(p.budget), p.hz)
		}
	}
	if p.slowFrames > 0 {
		verb := "were"
		if p.slowFrames == 1 {
			verb = "was"
		}
		p.line("   %s %s slow enough to be written down (over %s ms)",
			perfCount(p.slowFrames, "frame", "frames"), verb, perfMS(p.slow))
	}
	p.line(rule)
	p.mu.Unlock()

	if p.profile != nil {
		pprof.StopCPUProfile()
		_ = p.profile.Close()
	}
	log.SetOutput(os.Stderr)
	if p.f != nil {
		_ = p.f.Sync()
		_ = p.f.Close()
	}
	perf = nil
}

// read returns the runtime's counters.
func (p *perfLog) read() [perfSamples]float64 {
	var v [perfSamples]float64
	metrics.Read(p.samples[:])
	for i, s := range p.samples {
		switch s.Value.Kind() {
		case metrics.KindUint64:
			v[i] = float64(s.Value.Uint64())
		case metrics.KindFloat64:
			v[i] = s.Value.Float64()
		}
	}
	return v
}

// perfReading is the header's line about the reading settings. It normalizes
// them first, so a reader that has never saved its preferences still names the
// line height and margins it is actually laying out with.
func perfReading(o reader.Options, theme reader.Theme) string {
	o = o.Normalized()
	paper := string(theme)
	if paper == "" {
		paper = "the system's"
	}
	return fmt.Sprintf("%s, font %.1f, line %.2f, margin %.0f, paper %s",
		perfMode(o.Scroll), o.FontSize, o.LineHeight, o.Margin, paper)
}

// perfCount is "1 frame" and "2 frames": the log is read by a person, so the
// plural is worth getting right.
func perfCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// perfMode names the reading mode.
func perfMode(scroll bool) string {
	if scroll {
		return "scrolling"
	}
	return "paged"
}

// perfOr is s, or a stand-in when the frame never said what it was doing.
func perfOr(s string) string {
	if s == "" {
		return "standing still"
	}
	return s
}

// perfMost is the name that happened most in a block, which is what the block
// mostly was.
func perfMost(m map[string]int) string {
	best, n := "", 0
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return perfOr(best)
}

// perfSpread formats a block's durations as p50/p95/max milliseconds: the
// typical frame, the bad one, and the worst.
func perfSpread(d []time.Duration) string {
	if len(d) == 0 {
		return "     -/     -/     - ms"
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(p int) string { return perfMS(s[(len(s)-1)*p/100]) }
	return fmt.Sprintf("%6s/%6s/%6s ms", at(50), at(95), perfMS(s[len(s)-1]))
}

// perfMS formats a duration in milliseconds with one decimal: the resolution a
// frame is judged at.
func perfMS(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds()*1000, 'f', 1, 64)
}

// perfSpan formats a whole span of time the way a person says it.
func perfSpan(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	default:
		return fmt.Sprintf("%d m %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// perfBytes formats a byte count in the unit a person would use.
func perfBytes(n int64) string {
	switch {
	case n <= 0:
		return "0 B"
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}

// perfNum formats a pixel count without a fractional tail.
func perfNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// perfAbsf is the distance of v from zero.
func perfAbsf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
