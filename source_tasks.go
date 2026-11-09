package main

// Source-sheet background work. The sourceSheet owns the fields the view
// draws; this file owns the goroutines that fill them. Every search and
// download is a task in the window's scope, so closing the sheet, replacing a
// search, or closing the window cancels the work and drops its late callbacks.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/laix/quire/internal/acquire"
	"github.com/laix/quire/internal/source"
	"github.com/laix/quire/locales"
)

// startSearch cancels any running search and starts a new one. The search ID
// is kept as a second guard for the UI: it names the result the current query
// and filter expect even if a callback from an older task somehow reaches the
// sheet.
func (f *sourceSheet) startSearch(a *app) {
	q := strings.TrimSpace(f.query)
	if q == "" || a.sources == nil {
		return
	}
	if f.searchCancel != nil {
		f.searchCancel()
		f.searchCancel = nil
	}
	f.searchID++
	id := f.searchID
	f.searching = true
	f.results = nil
	f.statuses = make(map[source.ID]sourceStatus)
	f.mu.Lock()
	f.dlErr = nil
	f.dlSaved = acquire.Saved{}
	f.mu.Unlock()

	task := a.taskScope().newTask()
	// Publish the task's handles before starting it: its first post may be
	// immediate, and both the UI and closeSheet must see the same owner.
	f.searchCtx = task.Context()
	f.searchCancel = task.Cancel
	task.start(func(ctx context.Context, post func(func())) func() {
		outcomes := a.sources.Search(ctx, source.Query{Text: q, Page: 1})
		return func() {
			if id != f.searchID {
				return
			}
			f.applyOutcomes(a, outcomes)
			f.searching = false
			f.searchCtx = nil
			f.searchCancel = nil
		}
	})
}

// download starts downloading book from its source. Save owns the actual
// fetching and writing; this task owns cancellation, UI progress and the
// callback that puts the finished file on every shelf.
func (f *sourceSheet) download(a *app, book source.Book) {
	if a.acq == nil || a.sources == nil {
		return
	}
	src, ok := a.sources.Get(book.Source)
	if !ok {
		return
	}
	f.mu.Lock()
	if f.downloading {
		f.mu.Unlock()
		return
	}
	f.downloading = true
	f.dlBook = book
	f.dlProgress = source.Progress{}
	f.dlErr = nil
	f.dlSaved = acquire.Saved{}
	task := a.taskScope().newTask()
	f.dlCancel = task.Cancel
	f.mu.Unlock()

	progressMu := &sync.Mutex{}
	var lastUpdate time.Time
	throttle := 100 * time.Millisecond

	task.start(func(ctx context.Context, post func(func())) func() {
		saved, err := a.acq.Save(ctx, src, book, func(p source.Progress) {
			if ctx.Err() != nil {
				return
			}
			f.mu.Lock()
			f.dlProgress = p
			f.mu.Unlock()

			progressMu.Lock()
			now := time.Now()
			shouldUpdate := lastUpdate.IsZero() || now.Sub(lastUpdate) >= throttle
			progressMu.Unlock()
			if !shouldUpdate {
				return
			}
			post(func() {
				progressMu.Lock()
				lastUpdate = time.Now()
				progressMu.Unlock()
				// app.post asks for a frame after the callback returns.
			})
		})
		return func() { f.finishDownload(a, saved, err) }
	})
}

// finishDownload is the UI half of a completed download. It is only reached
// through the task's gated post, so a canceled or closed task never changes the
// sheet, notice, shelf or page.
func (f *sourceSheet) finishDownload(a *app, saved acquire.Saved, err error) {
	f.mu.Lock()
	f.downloading = false
	f.dlCancel = nil
	if err != nil {
		f.dlErr = err
	} else {
		f.dlSaved = saved
	}
	f.mu.Unlock()

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		a.notice = locales.SourceSheetDownloadFailed(err.Error())
		return
	}
	a.notice = locales.SourceSheetDownloaded(saved.Title)
	if saved.Path != "" {
		if a.pan != nil {
			a.pan.ImportDir(filepath.Dir(saved.Path))
		}
		if a.desk != nil {
			a.desk.refreshShelves(a)
		}
	}
}

// cancelDownload stops the download in flight. The task's posted progress and
// completion callbacks are dropped; the state is marked not busy at once so
// the sheet can accept another book.
func (f *sourceSheet) cancelDownload() {
	f.mu.Lock()
	cancel := f.dlCancel
	f.dlCancel = nil
	f.downloading = false
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// applyOutcomes turns registry outcomes into result rows and per-source
// status. It runs on the UI thread from the gated search callback.
func (f *sourceSheet) applyOutcomes(a *app, outcomes []source.Outcome) {
	f.results = nil
	f.statuses = make(map[source.ID]sourceStatus)
	for _, o := range outcomes {
		id := o.Source.ID()
		f.statuses[id] = sourceStatus{err: o.Err, elapsed: o.Elapsed}
		if o.Err != nil {
			continue
		}
		for _, b := range o.Result.Books {
			if strings.TrimSpace(b.Title) == "" {
				continue
			}
			f.results = append(f.results, sourceRow{book: b, elapsed: o.Elapsed})
		}
	}
}
