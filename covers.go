package main

import (
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/library"
	"github.com/laix/quire/locales"
)

// coverKey identifies a decoded cover: a book re-downloaded or edited gets a new
// key, so the shelf does not show the old picture. The modification time is part
// of it, so a file replaced by one of the same size is read again too.
type coverKey struct {
	path     string
	size     int64
	modified time.Time
}

// coverCacheLimit bounds the decoded cover cache. A shelf only paints a handful
// of covers at once; keeping every cover ever seen would make memory use grow
// with the library and with every replacement the reader opens. The value is
// deliberately small and fixed so memory use is predictable.
const coverCacheLimit = 128

// coverRecency records the order covers were used: least recently used first.
// a.covers is the storage (existing callers create it themselves), while the
// order lives here because the cache is UI-thread-only and the app struct is
// shared by the rest of the UI.
type coverRecency struct {
	keys []coverKey
}

var (
	coverCacheMu      sync.Mutex
	coverCacheRecency = map[*app]*coverRecency{}
)

// recencyFor returns the recency list for a's cover map, rebuilding it if the
// map was replaced or initialized outside coverBitmap.
func recencyFor(a *app) *coverRecency {
	r := coverCacheRecency[a]
	if r == nil || len(r.keys) != len(a.covers) {
		r = &coverRecency{keys: make([]coverKey, 0, len(a.covers))}
		for key := range a.covers {
			r.keys = append(r.keys, key)
		}
		coverCacheRecency[a] = r
	}
	return r
}

// touch marks key as the most recently used.
func (r *coverRecency) touch(key coverKey) {
	for i := range r.keys {
		if r.keys[i] == key {
			copy(r.keys[i:], r.keys[i+1:])
			r.keys[len(r.keys)-1] = key
			return
		}
	}
	r.keys = append(r.keys, key)
}

// takeOldest removes and returns the least recently used key.
func (r *coverRecency) takeOldest() (coverKey, bool) {
	if len(r.keys) == 0 {
		return coverKey{}, false
	}
	key := r.keys[0]
	copy(r.keys, r.keys[1:])
	r.keys = r.keys[:len(r.keys)-1]
	return key, true
}

// coverBitmap returns the decoded cover of e, decoding it the first time and keeping
// the bitmap afterwards. The cache lives on the UI thread, like every other
// bitmap: MyGo keeps a bitmap on the GPU as long as it is used. It is bounded to
// coverCacheLimit entries, evicting the least recently used cover first.
func (a *app) coverBitmap(e library.Entry) *ui.Bitmap {
	if len(e.Cover) == 0 {
		return nil
	}

	coverCacheMu.Lock()
	defer coverCacheMu.Unlock()

	if a.covers == nil {
		a.covers = map[coverKey]*ui.Bitmap{}
	}
	r := recencyFor(a)
	key := coverKey{path: e.Path, size: e.Size, modified: e.Modified}
	if b, ok := a.covers[key]; ok {
		r.touch(key)
		return b
	}

	for len(a.covers) >= coverCacheLimit {
		oldest, ok := r.takeOldest()
		if !ok {
			// The recency list and map got out of sync (for example, a
			// caller replaced covers directly); drop one arbitrary entry
			// rather than allowing the cache to exceed its bound.
			for oldest = range a.covers {
				break
			}
			delete(a.covers, oldest)
			break
		}
		delete(a.covers, oldest)
	}

	b, err := ui.DecodeBitmap(e.Cover)
	if err != nil {
		// A broken cover is cached too: retrying it on every shelf frame
		// would decode-fail just as often, and the key changes when the
		// file does.
		b = nil
	}
	a.covers[key] = b
	r.touch(key)
	return b
}

// shortAgo is "just now", "3 minutes ago", "2 hours ago", "yesterday" or a
// date, as a shelf shows when a book was last opened.
func shortAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return locales.TimeJustNow()
	case d < time.Hour:
		return locales.TimeMinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour:
		return locales.TimeHoursAgo(int(d.Hours()))
	case d < 48*time.Hour:
		return locales.TimeYesterday()
	case d < 7*24*time.Hour:
		return locales.TimeDaysAgo(int(d.Hours() / 24))
	default:
		// A date older than a week is written the way the reader's language and
		// regional formats write it, not as a fixed ISO string.
		formats := mygo.App.Region().FormatLocale(mygo.App.Locale())
		return i18n.FormatDate(formats, t, i18n.DateOptions{Skeleton: "yMMMd"})
	}
}
