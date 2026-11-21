package main

import (
	"sync"

	"github.com/laic7092/layx/render"
)

// A window's work off the frame thread, in two lanes: the one place a window
// computes and the one place it waits.
//
// The lanes belong to the *window*, not to a feature. Tiles, the panels' rows,
// a book's index and a cover's pixels are all computation and share the CPU
// lane; a store write, an import and a download are waits and share the I/O
// lane. A lane is one goroutine however many kinds of work pass through it,
// which is the difference between giving work a home and giving it a thread.
//
// Two rules keep a lane from being where frames are lost:
//
//   - A lane holds one pending task per priority instead of a queue: a newer
//     task of the same priority replaces the one waiting, so nothing computes a
//     place the reader has already left.
//   - The highest priority waiting runs first, so the tiles the next frames
//     will show never wait behind a whole book's index. Long work is handed
//     over in slices and asks wanted between them, so a slice is the longest a
//     frame can be kept waiting.
//
// Work runs off the frame thread and may not touch the window. It is handed the
// values it needs, and what it makes comes back through post, which runs on the
// UI thread.
type workroom struct {
	cpu lane
	io  lane
}

// The priorities a lane's slots hold, highest first.
type prio int

const (
	// prioFrame is work the frames about to be drawn are waiting for: the tiles
	// a scroll is heading into.
	prioFrame prio = iota
	// prioAsk is work the reader just asked for by name: a book opening, a jump.
	prioAsk
	// prioIdle is work nothing is waiting for: an index, covers, statistics.
	prioIdle
	prioCount
)

// lane is one thread's worth of a window's work: at most one goroutine, and
// whatever is waiting for it.
type lane struct {
	post func(func())

	mu      sync.Mutex
	wait    [prioCount]*work
	started bool
	wake    chan struct{}
	stop    chan struct{}
}

// work is one task in a lane.
type work struct {
	prio int
	// run does the work and returns what the UI thread should do with the
	// result, or nil when the frame will read it anyway. wanted is asked
	// between slices: false means the task has been superseded and may stop.
	run func(wanted func() bool) func()
}

// submit offers a task to the lane, dropping the one waiting at the same
// priority: the newest ask is the one that matters.
func (l *lane) submit(p prio, run func(wanted func() bool) func()) {
	if run == nil {
		return
	}
	l.mu.Lock()
	l.wait[p] = &work{prio: int(p), run: run}
	first := !l.started
	if first {
		l.started = true
		l.wake, l.stop = make(chan struct{}, 1), make(chan struct{})
	}
	l.mu.Unlock()
	if first {
		go l.loop()
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// loop takes the highest priority waiting until the lane is stopped.
func (l *lane) loop() {
	for {
		select {
		case <-l.stop:
			return
		case <-l.wake:
		}
		for {
			w := l.take()
			if w == nil {
				break
			}
			publish := w.run(l.wanted(w.prio))
			if publish != nil && l.post != nil {
				l.post(publish)
			}
		}
	}
}

// take removes the highest priority task waiting.
func (l *lane) take() *work {
	l.mu.Lock()
	defer l.mu.Unlock()
	for p := prio(0); p < prioCount; p++ {
		if w := l.wait[p]; w != nil {
			l.wait[p] = nil
			return w
		}
	}
	return nil
}

// wanted reports whether a task of priority p is still the newest of its kind:
// a higher priority waiting, or a newer task of the same one, supersedes it.
func (l *lane) wanted(p int) func() bool {
	return func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for q := prio(0); q < prio(p); q++ {
			if l.wait[q] != nil {
				return false
			}
		}
		return l.wait[p] == nil
	}
}

// close stops the lane's thread: a closed window's work is not worth finishing.
func (l *lane) close() {
	l.mu.Lock()
	started := l.started
	l.mu.Unlock()
	if started {
		select {
		case <-l.stop:
		default:
			close(l.stop)
		}
	}
}

// closeWork stops both lanes of a window that is going away.
func (a *app) closeWork() {
	a.work.cpu.close()
	a.work.io.close()
}

// warmTiles lays out the tiles a scroll is heading into, on the CPU lane: a tile
// is a pass over a screenful of text, and a frame that shapes one is a frame
// that misses the display.
//
// LayX was written for this: Tiles is the explicit prefetch API and spawns no
// goroutine of its own, and Tile guards its cache with a mutex, so the lane and
// the frame may ask for the same tile at once and neither sees a half-built one.
// The layout is handed over rather than read from the window, so nothing here
// touches state the UI thread owns.
func (a *app) warmTiles(lay *render.Layout, lo, hi int) {
	if lay == nil {
		return
	}
	if a.ahead.lay == lay && a.ahead.lo == lo && a.ahead.hi == hi {
		// The column has not travelled into another tile: the range the lane was
		// given is the range these frames need, and asking again would waken a
		// thread to find four tiles in the cache it already put there.
		return
	}
	a.ahead.lay, a.ahead.lo, a.ahead.hi = lay, lo, hi
	a.work.cpu.submit(prioFrame, func(func() bool) func() {
		lay.Tiles(lo, hi)
		return nil
	})
}
