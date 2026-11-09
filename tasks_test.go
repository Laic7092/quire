package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskScopeCancelDropsQueuedCallbacks(t *testing.T) {
	posted := make(chan func(), 2)
	scope := newTaskScope(nil, func(fn func()) { posted <- fn })
	task := scope.newTask()

	var ran atomic.Int32
	task.start(func(_ context.Context, post func(func())) func() {
		post(func() { ran.Add(1) })
		return func() { ran.Add(1) }
	})
	task.wait()

	task.Cancel()
	select {
	case <-task.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("task context was not canceled")
	}

	for i := 0; i < 2; i++ {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(time.Second):
			t.Fatal("task did not queue its progress and result callbacks")
		}
	}
	if got := ran.Load(); got != 0 {
		t.Fatalf("canceled task ran %d callbacks, want 0", got)
	}
}

func TestTaskScopeCloseDropsQueuedCallbacks(t *testing.T) {
	posted := make(chan func(), 2)
	scope := newTaskScope(nil, func(fn func()) { posted <- fn })
	task := scope.newTask()
	var ran atomic.Int32
	task.start(func(_ context.Context, post func(func())) func() {
		post(func() { ran.Add(1) })
		return func() { ran.Add(1) }
	})
	task.wait()
	scope.Close()

	for i := 0; i < 2; i++ {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(time.Second):
			t.Fatal("task did not queue its progress and result callbacks")
		}
	}
	if got := ran.Load(); got != 0 {
		t.Fatalf("closed scope ran %d callbacks, want 0", got)
	}
}

func TestTaskScopeConcurrentPostsAreSafe(t *testing.T) {
	const posts = 64
	queue := make(chan func(), posts)
	scope := newTaskScope(nil, func(fn func()) { queue <- fn })
	task := scope.newTask()

	var callers sync.WaitGroup
	var ran atomic.Int32
	task.start(func(_ context.Context, post func(func())) func() {
		for i := 0; i < posts; i++ {
			callers.Add(1)
			go func() {
				defer callers.Done()
				post(func() { ran.Add(1) })
			}()
		}
		callers.Wait()
		return nil
	})
	task.wait()
	task.Cancel()

	for i := 0; i < posts; i++ {
		(<-queue)()
	}
	if got := ran.Load(); got != 0 {
		t.Fatalf("canceled task ran %d concurrent callbacks, want 0", got)
	}
}

func TestAppPostWithoutWindowDropsCallback(t *testing.T) {
	var a app
	called := false
	a.post(func() { called = true })
	if called {
		t.Fatal("post ran a callback without a window")
	}
	if a.win != nil || a.winShared.Load() != nil {
		t.Fatal("post made or retained a window")
	}
}

func TestAppTaskScopeIsLazyAndOwned(t *testing.T) {
	var a app
	first := a.taskScope()
	if first == nil {
		t.Fatal("taskScope returned nil")
	}
	if second := a.taskScope(); second != first {
		t.Fatal("taskScope made more than one scope for one app")
	}
	a.closeTasks()
	if a.tasks.Load() != nil {
		t.Fatal("closeTasks kept the old scope")
	}
	if first.closed.Load() == false {
		t.Fatal("closeTasks did not close the old scope")
	}
	if next := a.taskScope(); next == first {
		t.Fatal("taskScope reused a closed scope")
	}
}
