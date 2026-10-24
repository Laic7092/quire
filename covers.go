package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/library"
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

// shortAgo is "刚刚", "3 分钟前", "2 小时前", "昨天" or a date, as a shelf shows
// when a book was last opened.
func shortAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 48*time.Hour:
		return "昨天"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
