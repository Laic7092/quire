package render

import "context"

// Prefetch builds the display lists for a window of pages (paged mode) or tiles
// (scroll mode) before the host paints them. It is the answer to the one-off
// spike a page turn or a scroll causes: instead of building one display list on
// the drawing path, the window is built on the engine's whole parallel budget.
//
// The window is clamped to the layout and to the display-list cache: asking for
// more than Options.PageCache (or TileCache) pages would just evict what was
// built, so only the first cache-sized part of the window is built. Call it
// again after the cache has moved on.
//
// Prefetch is synchronous; it uses the caller's goroutine plus the engine's
// helpers, and returns nil without doing anything in the wrong mode. Hosts that
// must not block (a UI thread) call StartPrefetch, which runs the same work on
// an explicit goroutine the host owns.
func (l *Layout) Prefetch(ctx context.Context, lo, hi int) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lo, hi = l.clampPrefetch(lo, hi)
	if hi <= lo {
		return ctx.Err()
	}
	scroll := l.opts.Mode == ModeScroll
	// Page and Tile never cache a partial display list (a recovered internal
	// error marks the page incomplete), so a failed prefetch leaves the cache
	// consistent; the error still tells the host that the window is cold.
	if err := l.engine.parallelFor(ctx, hi-lo, func(k int) {
		if ctx.Err() != nil {
			return
		}
		if scroll {
			l.Tile(lo + k)
			return
		}
		l.Page(lo + k)
	}); err != nil {
		return err
	}
	return ctx.Err()
}

// clampPrefetch bounds a prefetch window to the layout and to the display-list
// cache that will hold the result.
func (l *Layout) clampPrefetch(lo, hi int) (int, int) {
	total, cache := l.PageCount(), l.engine.pageCache
	if l.opts.Mode == ModeScroll {
		total, cache = l.tileCount, l.engine.tileCache
	}
	if lo < 0 {
		lo = 0
	}
	if hi > total {
		hi = total
	}
	if cache > 0 && hi-lo > cache {
		hi = lo + cache
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// Prefetch is the handle of a background prefetch started by StartPrefetch.
// The goroutine is explicit — the host owns it through this value — and it ends
// when the work finishes or the context is cancelled. Wait joins it.
type Prefetch struct {
	done chan struct{}
	err  error
}

// StartPrefetch runs Prefetch on its own goroutine and returns the handle.
//
// It exists so a UI thread can warm a page or tile window without blocking the
// frame it is drawing. The work still goes through the engine's budget, so a
// background prefetch cannot starve a foreground layout: the two share the same
// Workers goroutines.
func (l *Layout) StartPrefetch(ctx context.Context, lo, hi int) *Prefetch {
	p := &Prefetch{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		// This goroutine belongs to the library, so a panic here must not
		// escape: it would take down the host with no way to recover.
		defer recoverInto(&p.err)
		p.err = l.Prefetch(ctx, lo, hi)
	}()
	return p
}

// Wait blocks until the prefetch finishes and reports its result: nil on
// success, the context's error if it was cancelled. Wait may be called more
// than once.
func (p *Prefetch) Wait() error {
	if p == nil {
		return nil
	}
	<-p.done
	return p.err
}

// Done is closed when the prefetch finishes. Select on it together with the
// host's own cancellation signal instead of blocking in Wait.
func (p *Prefetch) Done() <-chan struct{} {
	if p == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.done
}
