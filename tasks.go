package main

import (
	"context"
	"sync"
	"sync/atomic"
)

// taskScope owns the background work that belongs to one UI object. Every task
// gets a context derived from the scope's, is tracked so closing the scope can
// cancel it, and publishes through the scope's post callback. The callback is
// expected to schedule work on the UI thread and must never make a window.
//
// A task may post progress while it runs and return a final callback when it is
// done. Both are dropped if the task is canceled or the scope is closed before
// the UI callback starts. This is what keeps a result from a closed source
// sheet or a replaced search from being applied to the window that replaced it.
type taskScope struct {
	ctx    context.Context
	cancel context.CancelFunc
	post   func(func())

	closed atomic.Bool

	mu    sync.Mutex
	tasks map[*task]struct{}
}

// task is one unit of background work in a taskScope.
type task struct {
	scope  *taskScope
	ctx    context.Context
	cancel context.CancelFunc

	canceled atomic.Bool
	started  atomic.Bool
	done     chan struct{}
}

// newTaskScope makes a scope whose tasks publish through post. A nil parent
// means context.Background. post may be nil; callbacks are then discarded,
// which is useful for a reader built without a UI thread.
func newTaskScope(parent context.Context, post func(func())) *taskScope {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if post == nil {
		post = func(func()) {}
	}
	return &taskScope{
		ctx:    ctx,
		cancel: cancel,
		post:   post,
		tasks:  make(map[*task]struct{}),
	}
}

// newTask registers a task with the scope but does not start it. Keeping the
// task out of the goroutine until the caller has stored its Cancel and Context
// is what lets a UI owner replace old work without racing its first post.
func (s *taskScope) newTask() *task {
	ctx, cancel := context.WithCancel(s.ctx)
	t := &task{
		scope:  s,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.mu.Lock()
	if s.closed.Load() {
		t.canceled.Store(true)
	} else {
		s.tasks[t] = struct{}{}
	}
	s.mu.Unlock()
	return t
}

// start runs work in a goroutine. work receives the task context and a safe
// post function; its returned callback, if any, is posted when work finishes.
// A task can be started only once.
func (t *task) start(work func(context.Context, func(func())) func()) {
	if !t.started.CompareAndSwap(false, true) {
		return
	}
	if t.canceled.Load() || t.scope.closed.Load() {
		t.finish()
		return
	}
	go func() {
		defer t.finish()
		if t.canceled.Load() || t.scope.closed.Load() {
			return
		}
		result := work(t.ctx, t.post)
		if result != nil {
			t.post(result)
		}
	}()
}

// Context is the task's context. It is canceled by Cancel or by closing the
// task's scope.
func (t *task) Context() context.Context { return t.ctx }

// Cancel stops the task and discards callbacks that have not started yet.
func (t *task) Cancel() {
	if !t.canceled.CompareAndSwap(false, true) {
		return
	}
	t.cancel()
	t.scope.forget(t)
}

// post schedules fn on the UI thread if the task and its scope are still live.
// The check is repeated when the UI callback runs, so a result queued just
// before Cancel is still dropped.
func (t *task) post(fn func()) {
	if fn == nil {
		return
	}
	s := t.scope
	if s.closed.Load() || t.canceled.Load() || t.ctx.Err() != nil {
		return
	}
	s.post(func() {
		if s.closed.Load() || t.canceled.Load() || t.ctx.Err() != nil {
			return
		}
		fn()
	})
}

// Close cancels every task in the scope and refuses new work. It does not wait
// for work that ignores its context: callbacks are gated above either way, and
// a closing window must not block on a network call.
func (s *taskScope) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.cancel()
	s.mu.Lock()
	for t := range s.tasks {
		t.canceled.Store(true)
		t.cancel()
	}
	s.mu.Unlock()
}

// forget removes a task from the scope's cancellation set.
func (s *taskScope) forget(t *task) {
	s.mu.Lock()
	delete(s.tasks, t)
	s.mu.Unlock()
}

// finish records that a task's work has returned. The done channel is kept for
// tests and for callers that need to know a task stopped; callbacks already
// queued on the UI remain subject to the cancellation checks above.
func (t *task) finish() {
	t.scope.forget(t)
	close(t.done)
}

// wait blocks until the task's work has returned or the task was never started
// after having been canceled. It is used by focused tests and does not wait for
// UI callbacks already queued by post.
func (t *task) wait() { <-t.done }

// taskScope returns the app's task scope, making it on first use. newApp
// installs one before a window exists, but a reader built by hand (as the
// tests build one) still gets an owned scope instead of sharing process state.
func (a *app) taskScope() *taskScope {
	if s := a.tasks.Load(); s != nil {
		return s
	}
	s := newTaskScope(context.Background(), a.post)
	if a.tasks.CompareAndSwap(nil, s) {
		return s
	}
	s.Close() // another caller installed the app's one scope first
	return a.tasks.Load()
}

// post sends fn to the window's UI thread through the pointer background
// goroutines may read. It never looks at a.win and never makes a window: once
// the window has gone the callback is dropped. The callback also asks for a
// frame after it runs, the same way the source sheet's old hand-written
// Window.Update blocks did.
func (a *app) post(fn func()) {
	if fn == nil {
		return
	}
	win := a.winShared.Load()
	if win == nil || win.IsDestroyed() {
		return
	}
	win.Update(func() {
		if win.IsDestroyed() {
			return
		}
		defer a.refresh(win)
		fn()
	})
}

// closeTasks cancels every task owned by this window and makes the next use
// start with a fresh scope. It is called from the window's close handler.
func (a *app) closeTasks() {
	if s := a.tasks.Swap(nil); s != nil {
		s.Close()
	}
}
