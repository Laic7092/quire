package render

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-text/typesetting/language"
)

// budget is the process-wide concurrency budget an Engine hands out to
// parallel phases. Every Engine call site that wants to run work in parallel
// must hold a token for each goroutine it starts, so N concurrent Layout calls
// (or a Layout while the host prefetches tiles) share one machine instead of
// each starting Workers goroutines and thrashing the scheduler and the caches.
//
// Tokens are taken without blocking. A phase that cannot take a token runs its
// work on the calling goroutine instead, which keeps the scheme free of
// deadlock: no goroutine ever waits for a token while holding one.
type budget struct {
	free atomic.Int64
}

func newBudget(n int) *budget {
	b := &budget{}
	if n < 1 {
		n = 1
	}
	b.free.Store(int64(n))
	return b
}

// take reserves one token, reporting whether one was available.
func (b *budget) take() bool {
	for {
		cur := b.free.Load()
		if cur <= 0 {
			return false
		}
		if b.free.CompareAndSwap(cur, cur-1) {
			return true
		}
	}
}

func (b *budget) give() { b.free.Add(1) }

// free reports how many tokens are currently unclaimed. It is a diagnostic.
func (b *budget) avail() int { return int(b.free.Load()) }

// parallelFor runs fn(0) ... fn(n-1). The calling goroutine always takes part,
// so the phase finishes with the same work done whether or not a helper was
// available; helpers only make it finish earlier. There is no blocking anywhere:
// a phase that cannot get a token simply runs on its caller.
//
// Load balance has two layers. Within a phase, one claim counter hands out the
// next index to whoever is free, so a slow task delays only itself. Between
// phases, the budget is split evenly by phaseLimit and helpers retire as soon as
// more phases appear, so a layout that started first cannot keep the whole
// machine while a second one waits — the first one's extra helpers finish their
// current task (tens of microseconds) and hand their tokens over.
//
// Deadlock safety comes from the same rule: no goroutine ever waits for a token.
// Helpers take one without blocking, work, and give it back; the caller works
// regardless. Nested phases (a page built inside a prefetch) therefore degrade
// to running on their caller instead of waiting for their parent.
//
// parallelFor cannot panic on behalf of fn: runTask converts a panic in fn into
// an error wrapping ErrInternal, both on the calling goroutine and on helpers.
// The first panic is returned and no further task is handed out; tasks already
// running are allowed to finish, so when parallelFor returns every helper has
// joined and every budget token has been returned. A cancelled context wins
// over the recovered error.
func (e *Engine) parallelFor(ctx context.Context, n int, fn func(i int)) error {
	if n <= 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n == 1 || e.workers <= 1 {
		for i := 0; i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := e.runTask(0, fn, i); err != nil {
				return err
			}
		}
		return nil
	}

	e.active.Add(1)
	defer e.active.Add(-1)
	e.stats.tasks.Add(int64(n))

	var (
		claim  atomic.Int64
		live   atomic.Int32
		failed atomic.Bool
		errMu  sync.Mutex
		first  error
		wg     sync.WaitGroup
	)
	// fail records the first panic. Once it is set, workers stop claiming new
	// tasks; helpers still in flight finish theirs and join.
	fail := func(err error) {
		if err == nil {
			return
		}
		failed.Store(true)
		errMu.Lock()
		if first == nil {
			first = err
		}
		errMu.Unlock()
	}
	// spawn starts one helper. Its ordinal is its position among the phase's
	// helpers; a helper whose ordinal no longer fits the (shrunk) share retires
	// at its next task boundary and releases its token.
	spawn := func(ordinal int) bool {
		// Never start more helpers than there is work: a three-tile phase on a
		// sixteen-core machine would otherwise create thirteen goroutines that
		// exit on their first claim.
		if ordinal >= n-1 || ordinal >= e.phaseLimit()-1 {
			return false
		}
		if !e.budget.take() {
			return false
		}
		slot := e.nextSlot()
		live.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer e.budget.give()
			defer live.Add(-1)
			// No panic may escape a helper goroutine: a panic recovered here is
			// a bug in the scheduling code itself (runTask already covers fn),
			// and the host can still receive it as an error.
			defer func() {
				if r := recover(); r != nil {
					fail(internalErr(r))
				}
			}()
			// The share check is a shared-cache-line read; checking it on every
			// task of a paragraph-sized phase costs more than it saves. Tasks
			// are short, so a delay of a few tasks before a helper retires is
			// still tens of microseconds.
			for taken := 0; ; taken++ {
				if failed.Load() {
					return
				}
				if taken&7 == 0 && ordinal >= e.phaseLimit()-1 {
					return
				}
				i := int(claim.Add(1)) - 1
				if i >= n || ctx.Err() != nil {
					return
				}
				if err := e.runTask(slot, fn, i); err != nil {
					fail(err)
					return
				}
			}
		}()
		return true
	}

	// The caller works and, between tasks, grows the helper set whenever the
	// budget allows it. Growing from the loop (instead of once up front) is what
	// lets a phase that started while the machine was busy pick up tokens as
	// other phases retire.
	for taken := 0; ; taken++ {
		if failed.Load() {
			break
		}
		if taken&7 == 0 {
			for spawn(int(live.Load())) {
			}
		}
		i := int(claim.Add(1)) - 1
		if i >= n || ctx.Err() != nil {
			break
		}
		if err := e.runTask(0, fn, i); err != nil {
			fail(err)
			break
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	errMu.Lock()
	err := first
	errMu.Unlock()
	return err
}

// phaseLimit is the number of goroutines a single phase may use right now: its
// own caller plus helpers, dividing the budget by the phases in flight. It is
// at least one, so a caller is never idle when it could work.
func (e *Engine) phaseLimit() int {
	if e.workers <= 1 {
		return 1
	}
	active := int(e.active.Load())
	if active < 1 {
		active = 1
	}
	limit := e.workers / active
	if limit < 1 {
		limit = 1
	}
	return limit
}

// runTask executes one claim, timing it when Measure is on so Stats can report
// how evenly the work was spread. A panic in fn — the library's own bug, since
// fn only processes the caller's already-validated document — is converted into
// an error wrapping ErrInternal instead of tearing down the host process.
func (e *Engine) runTask(slot int, fn func(int), i int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = internalErr(r)
		}
	}()
	if !e.measure {
		fn(i)
		return nil
	}
	start := time.Now()
	fn(i)
	e.stats.recordBusy(slot, time.Since(start))
	return nil
}

// internalErr wraps a recovered panic. The panic value is kept in the message:
// it is the only clue a host or a bug report has about what went wrong.
func internalErr(r any) error {
	return fmt.Errorf("%w: %v", ErrInternal, r)
}

// recoverInto converts a panic on the calling goroutine into an error wrapping
// ErrInternal. Call it directly from a defer statement (recover only works when
// called by the deferred function itself):
//
//	defer recoverInto(&err)
//
// It complements parallelFor: phases recover panics inside their tasks, but a
// code path that runs on the caller without going through parallelFor (a plan
// pass, say) still must fail with an error instead of crashing the host.
func recoverInto(errp *error) {
	if r := recover(); r != nil {
		if errp != nil {
			*errp = internalErr(r)
		}
	}
}

// nextSlot returns the slot id a helper reports its busy time under. Slot 0 is
// the calling goroutine.
func (e *Engine) nextSlot() int {
	if e.workers <= 1 {
		return 0
	}
	return 1 + int(e.slot.Add(1)-1)%(e.workers-1)
}

// workerPool returns the reusable textWorker pool for a font set. Pools are
// shared by every Layout the Engine builds, so a font.Face and a Harfbuzz
// shaper are created once per worker instead of once per Layout call, and the
// scratch buffers stay warm across relayouts.
func (e *Engine) workerPool(fs *FontSet) *sync.Pool {
	e.poolsMu.Lock()
	defer e.poolsMu.Unlock()
	if e.pools == nil {
		e.pools = make(map[*FontSet]*sync.Pool)
	}
	p := e.pools[fs]
	if p == nil {
		fonts, shapes := fs, &e.shapes
		p = &sync.Pool{New: func() any {
			w := newTextWorker(fonts, Typography{}, nil)
			w.shapes = shapes
			return w
		}}
		e.pools[fs] = p
	}
	return p
}

// leaseWorker borrows a worker from a pool and points it at one layout
// variant. Workers are reused across layouts; only Typography, the hyphenator
// and the default language change, never the font set.
func leaseWorker(pool *sync.Pool, typo Typography, hyphenator Hyphenator, lang language.Language) *textWorker {
	w := pool.Get().(*textWorker)
	w.prepare(typo, hyphenator, lang)
	return w
}

// releaseWorker returns a leased worker. Long-lived state that belongs to the
// host rather than to the Engine is dropped so a pooled worker never keeps a
// hyphenator dictionary or a theme alive.
func releaseWorker(pool *sync.Pool, w *textWorker) {
	w.hyphenator = nil
	w.typo = Typography{}
	pool.Put(w)
}

// Close releases the Engine's cached workers (font face caches, shapers and
// scratch buffers). It is optional: cached workers are also reclaimed by the
// garbage collector. The Engine stays usable afterwards and rebuilds the cache
// on the next layout; existing Layouts are unaffected because they only borrow
// workers for the duration of a call.
//
// There is nothing else to shut down: the Engine starts no background
// goroutines, and parallel phases join their helpers before returning. The one
// exception is an explicit StartPrefetch, whose goroutine ends on Wait or when
// its context is cancelled.
func (e *Engine) Close() {
	e.poolsMu.Lock()
	e.pools = nil
	e.poolsMu.Unlock()
}
