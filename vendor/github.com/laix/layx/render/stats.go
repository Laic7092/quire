package render

import (
	"sync/atomic"
	"time"
)

// Stats is a snapshot of an Engine's cumulative phase timings.
//
// Only work performed with Options.Measure enabled is timed; Shapes is the
// exception and is always counted. Timings are wall-clock sums over every
// Layout and Relayout call since New or the last ResetStats.
//
// Total covers one whole Layout call. Keys, Break, Stack, Paginate and Assemble
// are its stages and together account for most of Total. Display is lazy
// Page/Tile display-list construction, which happens outside Layout, so it is
// reported separately and is not part of Total.
//
// Stats reads each counter independently: a snapshot taken while another
// goroutine is laying out may mix slightly different moments. Use it for
// benchmarks and diagnostics, not for control flow.
type Stats struct {
	// Layouts is the number of Layout and Relayout calls that passed option
	// validation and reached the keying stage.
	Layouts int64
	// Total is the wall time spent inside Engine.Layout and Layout.Relayout.
	Total time.Duration
	// Keys is the time spent deriving BreakKey, PageKey and PaintKey. It walks
	// the document but does no shaping.
	Keys time.Duration
	// Break is the time spent tokenizing, shaping, line breaking and scheduling
	// the parallel workers for chapters whose BreakKey missed.
	Break time.Duration
	// Stack is the time spent stacking cached line boxes (line height, paragraph
	// gap, margins) and resolving node and anchor placements.
	Stack time.Duration
	// Paginate is the time spent slicing the stacked flow into pages.
	Paginate time.Duration
	// Assemble is the time spent building chapter flows, merging anchors and
	// initializing scroll coordinates.
	Assemble time.Duration
	// Display is the time spent lazily building Page and Tile display lists.
	Display time.Duration
	// Shapes is the number of shaping calls, counted even when Measure is off.
	Shapes int64
	// Tasks is the number of work items handed to parallel phases (chapters,
	// paragraphs, page items). It is counted even when Measure is off; the
	// ratio to Layouts is the library's average parallelism per layout.
	Tasks int64
	// Busy is per-worker slot busy wall time within parallel phases, index 0
	// being the calling goroutine. Only measured work is counted, so it never
	// exceeds Total. It is nil when Measure is off.
	Busy []time.Duration
	// Imbalance is (busiest - idlest) / busiest over the used worker slots, in
	// [0,1]. Zero means every participant did the same amount of work; values
	// near 1 mean one worker carried the layout. Use it to check that the task
	// granularity is fine enough for the document at hand.
	Imbalance float64
	// Pages and Tiles are the number of display lists built so far. A build
	// raced by two goroutines counts twice.
	Pages int64
	Tiles int64
}

type statsCounters struct {
	layouts  atomic.Int64
	total    atomic.Int64
	keys     atomic.Int64
	brk      atomic.Int64
	stack    atomic.Int64
	paginate atomic.Int64
	assemble atomic.Int64
	display  atomic.Int64
	pages    atomic.Int64
	tiles    atomic.Int64
	tasks    atomic.Int64
	busy     []atomic.Int64
}

// recordBusy adds d to one worker slot's busy time. Out-of-range slots (a
// nested phase reporting under its parent's id) are ignored.
func (s *statsCounters) recordBusy(slot int, d time.Duration) {
	if slot < 0 || slot >= len(s.busy) {
		return
	}
	s.busy[slot].Add(int64(d))
}

func (s *statsCounters) reset() {
	s.layouts.Store(0)
	s.total.Store(0)
	s.keys.Store(0)
	s.brk.Store(0)
	s.stack.Store(0)
	s.paginate.Store(0)
	s.assemble.Store(0)
	s.display.Store(0)
	s.pages.Store(0)
	s.tiles.Store(0)
	s.tasks.Store(0)
	for i := range s.busy {
		s.busy[i].Store(0)
	}
}

// Stats returns a snapshot of the Engine's counters.
func (e *Engine) Stats() Stats {
	s := Stats{
		Layouts:  e.stats.layouts.Load(),
		Total:    time.Duration(e.stats.total.Load()),
		Keys:     time.Duration(e.stats.keys.Load()),
		Break:    time.Duration(e.stats.brk.Load()),
		Stack:    time.Duration(e.stats.stack.Load()),
		Paginate: time.Duration(e.stats.paginate.Load()),
		Assemble: time.Duration(e.stats.assemble.Load()),
		Display:  time.Duration(e.stats.display.Load()),
		Shapes:   e.shapes.Load(),
		Tasks:    e.stats.tasks.Load(),
		Pages:    e.stats.pages.Load(),
		Tiles:    e.stats.tiles.Load(),
	}
	if len(e.stats.busy) > 0 {
		s.Busy = make([]time.Duration, len(e.stats.busy))
		var busiest, idlest time.Duration
		used := 0
		for i := range s.Busy {
			d := time.Duration(e.stats.busy[i].Load())
			s.Busy[i] = d
			if d <= 0 {
				continue
			}
			if used == 0 || d < idlest {
				idlest = d
			}
			if d > busiest {
				busiest = d
			}
			used++
		}
		if busiest > 0 && used > 1 {
			s.Imbalance = float64(busiest-idlest) / float64(busiest)
		}
	}
	return s
}

// ResetStats zeroes every counter, including Shapes. Call it between layouts
// rather than concurrently with Engine.Layout if you want a clean measurement
// window.
func (e *Engine) ResetStats() {
	e.stats.reset()
	e.shapes.Store(0)
}

// LayoutStats reports where one Layout or Relayout call spent its time.
//
// Unlike Engine.Stats, which accumulates over every call since New, LayoutStats
// is attached to the layout it produced and attributes work to that call only,
// even when the Engine runs several layouts concurrently:
//
//	l, err := eng.Layout(ctx, doc, opts)
//	if err == nil {
//		log.Printf("layout took %v, %d shapes", l.Stats().Total, l.Stats().Shapes)
//	}
//
// Durations are only measured when the Engine was created with
// Options.Measure. Without it every duration is zero and the layout path
// performs no clock reads; Shapes, Tasks, Pages and Tiles are always counted.
//
// Stack and Paginate are summed over the parallel chapter workers, so on a
// multi-core machine they can exceed Total: they are worker time, not wall time.
// Keys, Break, Assemble and Total are wall time; Break includes the parallel
// paragraph phase the caller waits for.
type LayoutStats struct {
	// Total is the wall time of the call: from after option validation to just
	// before the OnIssue replay. It is zero when Measure is off.
	Total time.Duration
	// Keys, Break, Stack, Paginate and Assemble attribute the call's stages.
	// Stack and Paginate are summed across the parallel chapter workers.
	Keys     time.Duration
	Break    time.Duration
	Stack    time.Duration
	Paginate time.Duration
	Assemble time.Duration
	// Shapes is the number of shaping calls this layout performed. It is
	// counted even when Measure is off.
	Shapes int64
	// Tasks is the number of work items this call handed to parallel phases
	// (chapters, paragraphs, page maps). It is counted even when Measure is
	// off.
	Tasks int64
	// Pages is the number of page maps the call produced, i.e. the page count
	// of a paged layout. It is 0 in scroll mode, where pages are not part of
	// the output.
	Pages int
	// Tiles is the scroll tile count of the call, and 0 in paged mode.
	Tiles int
}

// Stats returns the phase attribution of the Layout call that produced this
// Layout. The value is a snapshot: later calls on the same Engine do not change
// it.
func (l *Layout) Stats() LayoutStats { return l.stats }

// layoutRun accumulates the per-call counters and timings of one layoutDoc
// call. Scalar fields are written by the calling goroutine; atomics are what
// parallel chapter workers add.
type layoutRun struct {
	shapes     atomic.Int64
	stackNs    atomic.Int64
	paginateNs atomic.Int64
	tasks      int64
	keys       time.Duration
	brk        time.Duration
	assemble   time.Duration
}

// addTasks records that a parallel phase was handed n work items.
func (run *layoutRun) addTasks(n int) {
	if n > 0 {
		run.tasks += int64(n)
	}
}

// elapsed returns the time since start, or zero when start is the zero time a
// disabled timer returns. It is the per-call counterpart of Engine.stop.
func (e *Engine) elapsed(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}

// stats snapshots the run for a finished layout. total is measured separately
// because it spans the whole call.
func (run *layoutRun) stats(l *Layout, total time.Duration) LayoutStats {
	return LayoutStats{
		Total:    total,
		Keys:     run.keys,
		Break:    run.brk,
		Stack:    time.Duration(run.stackNs.Load()),
		Paginate: time.Duration(run.paginateNs.Load()),
		Assemble: run.assemble,
		Shapes:   run.shapes.Load(),
		Tasks:    run.tasks,
		Pages:    l.PageCount(),
		Tiles:    l.TileCount(),
	}
}

// mark starts a phase timer. It returns the zero time when timing is off, so
// stop becomes a no-op and the hot path performs no clock read.
func (e *Engine) mark() time.Time {
	if !e.measure {
		return time.Time{}
	}
	return time.Now()
}

// stop records the elapsed time since start into c. A zero start (timing off)
// is ignored.
func (e *Engine) stop(start time.Time, c *atomic.Int64) {
	if start.IsZero() {
		return
	}
	c.Add(int64(time.Since(start)))
}

// notePage records one lazily built page display list. It is a defer target so
// the call site stays a single line.
func (e *Engine) notePage(start time.Time) {
	e.stats.display.Add(int64(time.Since(start)))
	e.stats.pages.Add(1)
}

// noteTile is notePage for scroll-mode tiles.
func (e *Engine) noteTile(start time.Time) {
	e.stats.display.Add(int64(time.Since(start)))
	e.stats.tiles.Add(1)
}
