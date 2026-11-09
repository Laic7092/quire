package main

import (
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

// syncCoverOrder rebuilds the recency list after covers was initialized or
// replaced directly. Normal cache operations keep the two in step themselves.
func (a *app) syncCoverOrder() {
	if len(a.coversOrder) == len(a.covers) {
		return
	}
	a.coversOrder = make([]coverKey, 0, len(a.covers))
	for key := range a.covers {
		a.coversOrder = append(a.coversOrder, key)
	}
}

// touchCover marks key as the most recently used.
func (a *app) touchCover(key coverKey) {
	for i := range a.coversOrder {
		if a.coversOrder[i] == key {
			copy(a.coversOrder[i:], a.coversOrder[i+1:])
			a.coversOrder[len(a.coversOrder)-1] = key
			return
		}
	}
	a.coversOrder = append(a.coversOrder, key)
}

// evictOldestCover removes the least recently used entry.
func (a *app) evictOldestCover() {
	if len(a.coversOrder) == 0 {
		// syncCoverOrder normally prevents this. If the recency list was
		// emptied anyway, still drop an entry so the cache never exceeds its
		// bound; the next cache operation rebuilds the list from the map.
		for key := range a.covers {
			delete(a.covers, key)
			return
		}
		return
	}
	oldest := a.coversOrder[0]
	copy(a.coversOrder, a.coversOrder[1:])
	a.coversOrder = a.coversOrder[:len(a.coversOrder)-1]
	delete(a.covers, oldest)
}

// coverBitmap returns the decoded cover of e, decoding it the first time and keeping
// the bitmap afterwards. The cache lives on the UI thread, like every other
// bitmap: MyGo keeps a bitmap on the GPU as long as it is used. It is bounded to
// coverCacheLimit entries, evicting the least recently used cover first.
func (a *app) coverBitmap(e library.Entry) *ui.Bitmap {
	if len(e.Cover) == 0 {
		return nil
	}
	if a.covers == nil {
		a.covers = map[coverKey]*ui.Bitmap{}
	}
	a.syncCoverOrder()
	key := coverKey{path: e.Path, size: e.Size, modified: e.Modified}
	if b, ok := a.covers[key]; ok {
		a.touchCover(key)
		return b
	}

	for len(a.covers) >= coverCacheLimit {
		a.evictOldestCover()
	}

	b, err := ui.DecodeBitmap(e.Cover)
	if err != nil {
		// A broken cover is cached too: retrying it on every shelf frame
		// would decode-fail just as often, and the key changes when the
		// file does.
		b = nil
	}
	a.covers[key] = b
	a.touchCover(key)
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
