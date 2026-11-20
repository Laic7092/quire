package render

import (
	"context"

	"github.com/laix/layx/render/paint"
)

// linePaint is one line's contribution to a display list. Building it does not
// touch the list, so every line of a page or a tile can be built at once.
type linePaint struct {
	ops   []drawOp
	links []Link
}

// lineJob is one line to paint, with the page- or tile-local origin of its line
// box.
type lineJob struct {
	it      flowItem
	chapter int
	x, y    float32
}

// linePainter paints a batch of collected lines into a display list.
//
// A page or a tile is a few dozen lines and a line is a few microseconds, so
// such a batch is painted inline, on one leased worker, straight into the
// destination slice: that is the sequential cost and nothing else. Spreading it
// over the machine would cost a goroutine, a wakeup and a second copy per line,
// which is more than the few microseconds it saves — the parallelism that
// matters for a page or a tile comes from the page or tile above it (Tiles,
// Prefetch). Only a batch large enough to pay for the helpers is built in
// parallel; then results are appended by index and the caller keeps its own
// paint order.
type linePainter struct {
	l    *Layout
	jobs []lineJob
	// w is the leased worker of the inline path; out is the parallel result.
	w   *textWorker
	out []linePaint
	// err is the first panic recovered while building the parallel batch (it
	// wraps ErrInternal). The display list of that batch is incomplete: the
	// caller must not cache the page or tile it belongs to.
	err error
}

// newPainter starts painting jobs. The inline path leases a worker until close;
// the parallel path finishes before it returns, so its results can be appended
// in any order.
func (l *Layout) newPainter(jobs []lineJob) linePainter {
	p := linePainter{l: l, jobs: jobs}
	if len(jobs) == 0 {
		return p
	}
	if len(jobs) < l.engine.paintBatch() {
		p.w = l.leaseWorker()
		return p
	}
	p.out = make([]linePaint, len(jobs))
	p.err = l.engine.parallelFor(context.Background(), len(jobs), func(i int) {
		w := l.leaseWorker()
		// The worker must come back to the pool even if painting panics:
		// runTask recovers the panic after this defer has run.
		defer l.releaseWorker(w)
		l.paintLineInto(w, &jobs[i], &p.out[i].ops, &p.out[i].links, true)
	})
	return p
}

// paint appends job i to the display list. The inline path must be called in job
// order; the parallel path does not care.
func (p *linePainter) paint(i int, ops *[]drawOp, links *[]Link) {
	if p.w != nil {
		p.l.paintLineInto(p.w, &p.jobs[i], ops, links, false)
		return
	}
	*ops = append(*ops, p.out[i].ops...)
	*links = append(*links, p.out[i].links...)
}

// close releases the leased worker.
func (p *linePainter) close() {
	if p.w != nil {
		p.l.releaseWorker(p.w)
		p.w = nil
	}
}

// paintBatch is the line count at which a batch is worth spreading over the
// machine; below it, one worker is faster. See linePainter.
func (e *Engine) paintBatch() int {
	if n := 4 * e.workers; n > 32 {
		return n
	}
	return 32
}

// paintLineInto appends one line's draw ops and link rectangles in display-list
// order: decorations first (they sit behind the ink), then one op per shaped
// run, then the links the line carries. exact asks for the line's ops to be
// allocated once at their final size, which is what the parallel path wants.
func (l *Layout) paintLineInto(w *textWorker, job *lineJob, ops *[]drawOp, links *[]Link, exact bool) {
	it := &job.it
	runs, pos := l.buildLine(w, it, job.chapter)
	if len(runs) == 0 && it.para == nil {
		return
	}
	for j := range runs {
		runs[j].X += job.x
		// buildLine returns baselines relative to the line box, so the line's
		// own origin has to be added here.
		runs[j].Y += job.y
	}
	decos := l.lineDecorations(w, it, job.chapter, runs)
	if exact {
		*ops = make([]drawOp, 0, len(decos)+len(runs))
	}
	// Decorations come before the glyphs, so they sit behind the ink at the same
	// baseline.
	for _, deco := range decos {
		*ops = append(*ops, deco)
	}
	for j := range runs {
		*ops = append(*ops, drawOp{
			kind: opGlyphs,
			rect: glyphOpRect(job.x+it.x, job.y, it.width, it.height),
			run:  runs[j],
		})
	}
	if it.para == nil {
		return
	}
	for si := range it.para.segs {
		seg := &it.para.segs[si]
		if seg.link == "" {
			continue
		}
		if int(seg.end) <= int(it.start) || int(seg.start) >= int(it.end) {
			continue
		}
		// link rects reuse the positions buildLine already computed instead of
		// shaping the line a second time.
		x0, x1 := runeXFromPos(pos, int(seg.start), int(seg.end))
		*links = append(*links, Link{
			Rect: paint.Rect{X: job.x + it.x + x0, Y: job.y, Width: x1 - x0, Height: it.height},
			URL:  seg.link,
		})
	}
}
