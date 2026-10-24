package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/locales"
)

// coverKey identifies a decoded cover: a book re-downloaded or edited gets a new
// key, so the shelf does not show the old picture.
type coverKey struct {
	path string
	size int64
}

// coverBitmap returns the decoded cover of e, decoding it the first time and keeping
// the bitmap afterwards. The cache lives on the UI thread, like every other
// bitmap: MyGo keeps a bitmap on the GPU as long as it is used.
func (a *app) coverBitmap(e library.Entry) *ui.Bitmap {
	if len(e.Cover) == 0 {
		return nil
	}
	if a.covers == nil {
		a.covers = map[coverKey]*ui.Bitmap{}
	}
	key := coverKey{path: e.Path, size: e.Size}
	if b, ok := a.covers[key]; ok {
		return b
	}
	b, err := ui.DecodeBitmap(e.Cover)
	if err != nil {
		b = nil
	}
	a.covers[key] = b
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
