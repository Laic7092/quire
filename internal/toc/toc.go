// Package toc turns a book's chapters into a clickable table of contents.
//
// The data layer is Build: one Entry per book.Chapter, with the global page
// the chapter starts at and how many pages it spans, taken from
// render.Layout.PageRange. Nothing else is parsed: the EPUB navigation
// document is already reduced to a title per spine item by package book, so
// this package needs neither internal/reader nor a second pass over the
// archive.
//
// Levels are guessed from the title text, with one hard-coded rule:
//
//   - A heading of the form 第<number>卷 / 部 / 篇 / 册, or one that starts
//     with 卷, is a volume heading: Level 0.
//   - Any other heading is Level 1 and is indented one step in the panel.
//
// That is the whole hierarchy model: no nesting, no parent links, no
// order beyond book.Chapters. It is a deliberate approximation, not a
// general parser of Chinese book structure.
package toc

import (
	"strings"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
)

// Entry is one line of the table of contents.
type Entry struct {
	Title   string // chapter title
	Chapter int    // index into book.Chapters
	Page    int    // global page the chapter starts at; -1 in scroll mode or without a layout
	Pages   int    // pages in the chapter; 0 in scroll mode or without a layout
	Level   int    // 0 for a volume heading, 1 for everything else
	Current bool   // reading position falls inside this chapter
	// Loc is where the row jumps to: the chapter's root node, or the first
	// line of its content in a scroll layout. HasLoc is false when the layout
	// cannot resolve the chapter, which is also when the row is disabled.
	Loc    render.Location
	HasLoc bool
}

// Build derives the table of contents from a book and a layout.
//
// layout may be nil, which is the state before anything is laid out: every
// Entry then has Page -1, Pages 0 and Current false, and only the titles and
// levels are filled in. currentPage is a global page index; it marks the
// chapter whose PageRange contains it as Current, and no chapter when it
// falls outside every range (including a page past the end of the layout).
//
// A scroll layout has no pages: Page stays -1 and Current is nobody's. BuildAt
// takes the reading location instead, so both modes mark the chapter the reader
// is in.
//
// A layout whose chapter count does not match the book is treated as stale
// rather than as an error: chapters it still covers keep their pages, the
// rest fall back to Page -1. Build never panics on a nil or empty book; it
// returns nil.
func Build(b *book.Book, layout *render.Layout, currentPage int) []Entry {
	return build(b, layout, currentPage, -1)
}

// BuildAt is Build for a reading position instead of a page. current is the
// location at the top of what the reader sees, which is also how a scroll
// layout names its place: the chapter current falls in is the one marked
// Current, and the entries carry the location their row jumps to.
func BuildAt(b *book.Book, layout *render.Layout, current render.Location) []Entry {
	page := -1
	if layout != nil && layout.PageCount() > 0 {
		if n, ok := layout.PageOf(current); ok {
			page = n
		}
	}
	return build(b, layout, page, current.Chapter)
}

// build fills the entries: pages for a paged layout, a location for either.
func build(b *book.Book, layout *render.Layout, currentPage, currentChapter int) []Entry {
	if b == nil || len(b.Chapters) == 0 {
		return nil
	}
	entries := make([]Entry, len(b.Chapters))
	for i, ch := range b.Chapters {
		e := Entry{
			Title:   ch.Title,
			Chapter: i,
			Page:    -1,
			Level:   levelOf(ch.Title),
		}
		if layout != nil {
			if first, count, ok := layout.PageRange(i); ok && count > 0 {
				e.Page, e.Pages = first, count
				e.Current = currentPage >= first && currentPage < first+count
			} else {
				// A scroll layout: no pages, so the chapter is current when the
				// reader is anywhere in it.
				e.Current = currentChapter == i
			}
			if loc, ok := layout.Locate(render.AnchorID(i + 1)); ok {
				e.Loc, e.HasLoc = loc, true
			} else if top, ok := layout.ChapterContentTop(i); ok {
				if loc, ok := layout.LocationAt(top); ok {
					e.Loc, e.HasLoc = loc, true
				}
			}
		}
		entries[i] = e
	}
	return entries
}

// PageOf returns the page clicking e should jump to.
//
// The layout wins over the page stored in e, so entries built from an older
// layout still land on the right page after a relayout, as long as the
// chapter still exists. When the layout does not cover e.Chapter, the stored
// page is used if it is still inside the layout. Without a layout, ok is
// false. A scroll layout has no pages: use e.Loc and Layout.OffsetOf instead.
func PageOf(layout *render.Layout, e Entry) (page int, ok bool) {
	if layout == nil {
		return 0, false
	}
	if first, count, ok := layout.PageRange(e.Chapter); ok && count > 0 {
		return first, true
	}
	if e.Page >= 0 && e.Page < layout.PageCount() {
		return e.Page, true
	}
	return 0, false
}

// levelOf guesses a heading's level from its text alone. See the package
// comment for the rule.
func levelOf(title string) int {
	t := strings.TrimSpace(title)
	r := []rune(t)
	if len(r) == 0 {
		return 1
	}
	if r[0] == '第' {
		i := 1
		for i < len(r) && isNumberRune(r[i]) {
			i++
		}
		if i > 1 && i < len(r) && strings.ContainsRune("卷部篇册", r[i]) {
			return 0
		}
		return 1
	}
	if r[0] == '卷' {
		return 0
	}
	return 1
}

// isNumberRune reports whether r can be the number in 第<number>卷: an ASCII
// digit or one of the Chinese numerals a volume number uses. It is a fixed
// set, not a general numeral parser.
func isNumberRune(r rune) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	return strings.ContainsRune("零〇一二三四五六七八九十百千万两", r)
}
