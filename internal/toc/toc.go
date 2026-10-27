// Package toc turns a book's chapters into a clickable table of contents.
//
// The data layer is Build: one Entry per row, with the global page the row
// points at, all taken from render.Layout. Nothing is parsed here: the EPUB
// navigation document is already reduced to chapters and to Book.Toc by
// github.com/laix/layx/epub, so this package needs neither internal/reader nor
// a second pass over the archive.
//
// The panel shows the book's own table of contents when the book has one:
// Book.Toc keeps the nav/NCX order, nesting and #fragment targets, and every
// target carries a render.AnchorID that Layout.Locate resolves, so a row can
// land inside a chapter. A navigation document is used only when it reads like
// a table of contents (see rowsFromTOC); otherwise the rows are the chapters
// themselves, and a plain text book — which has no navigation document at all
// — is always in that second shape.
//
// The chapter rows guess their levels from the title text, with one hard-coded
// rule:
//
//   - A heading of the form 第<number>卷 / 部 / 篇 / 册, or one that starts
//     with 卷, is a volume heading: Level 0.
//   - Any other heading is Level 1 and is indented one step in the panel.
//
// Those rows are a fallback for books without a usable navigation document; a
// listed book keeps its real hierarchy instead.
package toc

import (
	"strings"
	"unicode/utf8"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
)

// Entry is one line of the table of contents.
type Entry struct {
	Title   string // row title, from the navigation document or the chapter
	Chapter int    // index into book.Chapters
	// Anchor is the row's target inside that chapter: the chapter root for a
	// row that only names the chapter, a fragment's anchor for a row from the
	// navigation document. Loc is what Layout.Locate made of it.
	Anchor render.AnchorID
	Page   int // global page the row points at; -1 in scroll mode or without a layout
	Pages  int // pages in a chapter row's range; 1 for a row inside a chapter; 0 without pages
	Level  int // indentation: 0 for a top-level row, one step per nested level
	// Group reports that the rows after this one are nested under it, so it
	// names a part of the book rather than a place to read. The panel draws
	// groups muted.
	Group   bool
	Current bool // reading position falls inside this row
	// Loc is where the row jumps to: the row's anchor, or the first line of
	// its chapter's content when the anchor is missing from the layout.
	// HasLoc is false when the layout cannot resolve the row, which is also
	// when the row is disabled.
	Loc    render.Location
	HasLoc bool
}

// row is one candidate line, before pages, locations and the reading position
// are known.
type row struct {
	title   string
	chapter int
	anchor  render.AnchorID
	level   int
	// nav marks a row taken from the navigation document, whose anchor may
	// name a fragment inside the chapter instead of its start.
	nav bool
}

// maxLabelRunes is the longest navigation label the panel can show as a title.
// It is generous — real chapter titles stay well below it — and exists to drop
// the labels some books put in their NCX: whole paragraphs of body text.
const maxLabelRunes = 60

// Build derives the table of contents from a book and a layout.
//
// layout may be nil, which is the state before anything is laid out: every
// Entry then has Page -1, Pages 0 and Current false, and only the titles and
// levels are filled in. currentPage is a global page index; it marks the row
// the reading position falls in, and no row when it falls outside every page
// range (including a page past the end of the layout).
//
// A scroll layout has no pages: Page stays -1 and Current is nobody's. BuildAt
// takes the reading location instead, so both modes mark the row the reader is
// in.
//
// A layout whose chapter count does not match the book is treated as stale
// rather than as an error: chapters it still covers keep their pages, the
// rest fall back to Page -1. Build never panics on a nil or empty book; it
// returns nil.
func Build(b *book.Book, layout *render.Layout, currentPage int) []Entry {
	return build(b, layout, currentPage, -1, render.Location{})
}

// BuildAt is Build for a reading position instead of a page. current is the
// location at the top of what the reader sees, which is also how a scroll
// layout names its place: the row current falls in is the one marked Current,
// and the entries carry the location their row jumps to.
func BuildAt(b *book.Book, layout *render.Layout, current render.Location) []Entry {
	page := -1
	if layout != nil && layout.PageCount() > 0 {
		if n, ok := layout.PageOf(current); ok {
			page = n
		}
	}
	return build(b, layout, page, current.Chapter, current)
}

// build turns the book's rows into entries: pages and locations from the
// layout, Current from the reading position.
func build(b *book.Book, layout *render.Layout, currentPage, currentChapter int, current render.Location) []Entry {
	if b == nil || len(b.Chapters) == 0 {
		return nil
	}
	rows := rowsFromTOC(b)
	if rows == nil {
		rows = rowsFromChapters(b)
	}
	entries := make([]Entry, len(rows))
	for i, r := range rows {
		e := Entry{
			Title:   r.title,
			Chapter: r.chapter,
			Anchor:  r.anchor,
			Level:   r.level,
			Page:    -1,
			Group:   i+1 < len(rows) && rows[i+1].level > r.level,
		}
		if layout != nil {
			e.Loc, e.HasLoc = locate(layout, r)
			if e.HasLoc {
				if page, ok := layout.PageOf(e.Loc); ok {
					e.Page, e.Pages = page, 1
				}
			}
			// A chapter row spans pages; its range is the more useful label
			// than the single page it starts on, and it also covers a layout
			// whose anchors do not resolve.
			if !r.nav {
				if first, count, ok := layout.PageRange(r.chapter); ok && count > 0 {
					e.Page, e.Pages = first, count
				}
			}
		}
		entries[i] = e
	}
	markCurrent(entries, layout, currentPage, currentChapter, current)
	return entries
}

// rowsFromTOC returns one row per navigation target, in reading order, or nil
// when the book has no navigation document worth showing.
//
// The panel is a way around the book, so a navigation document is only
// trusted with it when it reads like a table of contents:
//
//   - Most of its labels must be short. Some books — Gutenberg conversions
//     among them — put whole paragraphs in the NCX; those rows say nothing a
//     reader can scan.
//   - It must name at least half of the chapters. A single NCX entry for a
//     thirty-chapter book is a stub, and the chapter list is a better table of
//     contents than the stub.
//
// Labels that fail the first rule are dropped row by row. Chapters no
// surviving row names keep a row of their own, so the panel still lists the
// whole book.
func rowsFromTOC(b *book.Book) []row {
	if len(b.Toc) == 0 || len(b.Chapters) == 0 {
		return nil
	}
	var kept []book.TOCEntry
	for _, e := range b.Toc {
		if e.Chapter < 0 || e.Chapter >= len(b.Chapters) || strings.TrimSpace(e.Title) == "" {
			continue // outside the reading order: the cover, a note page
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return nil
	}
	named := make(map[int]bool, len(kept))
	short, minDepth := 0, kept[0].Depth
	for _, e := range kept {
		if utf8.RuneCountInString(e.Title) > maxLabelRunes {
			continue
		}
		short++
		named[e.Chapter] = true
		if e.Depth < minDepth {
			minDepth = e.Depth
		}
	}
	if short*3 < len(kept)*2 || len(named)*2 < len(b.Chapters) {
		return nil
	}

	perChapter := make(map[int][]book.TOCEntry, len(named))
	for _, e := range kept {
		if utf8.RuneCountInString(e.Title) <= maxLabelRunes {
			perChapter[e.Chapter] = append(perChapter[e.Chapter], e)
		}
	}
	rows := make([]row, 0, len(kept))
	for ci := range b.Chapters {
		nav := perChapter[ci]
		if len(nav) == 0 {
			rows = append(rows, chapterRow(b, ci))
			continue
		}
		for _, e := range nav {
			level := e.Depth - minDepth
			if level < 0 {
				level = 0
			}
			rows = append(rows, row{title: e.Title, chapter: ci, anchor: e.Anchor, level: level, nav: true})
		}
	}
	return rows
}

// rowsFromChapters is the fallback table of contents: one row per chapter,
// with the level guessed from its title. It is what a plain text book always
// gets.
func rowsFromChapters(b *book.Book) []row {
	rows := make([]row, len(b.Chapters))
	for i := range b.Chapters {
		rows[i] = chapterRow(b, i)
	}
	return rows
}

// chapterRow is the row of a chapter that starts at its first line.
func chapterRow(b *book.Book, chapter int) row {
	return row{
		title:   b.Chapters[chapter].Title,
		chapter: chapter,
		anchor:  render.AnchorID(chapter + 1),
		level:   levelOf(b.Chapters[chapter].Title),
	}
}

// locate resolves a row to a reading position. A row whose anchor the layout
// does not know still has the start of its chapter's content: navigation
// documents name fragments that a conversion can drop, or that an older
// archive spelled differently, and a row landing at the top of its chapter is
// better than a row that cannot be opened at all.
func locate(layout *render.Layout, r row) (render.Location, bool) {
	if layout == nil {
		return render.Location{}, false
	}
	if loc, ok := layout.Locate(r.anchor); ok {
		return loc, true
	}
	if top, ok := layout.ChapterContentTop(r.chapter); ok {
		return layout.LocationAt(top)
	}
	return render.Location{}, false
}

// markCurrent marks the row the reading position falls in. A chapter can own
// several rows — a navigation document may name sections — so the current row
// is the last one at or before the position, and a chapter whose rows all start
// later marks its first row.
func markCurrent(entries []Entry, layout *render.Layout, currentPage, currentChapter int, current render.Location) {
	if layout == nil || len(entries) == 0 {
		return
	}
	chapter := currentChapter
	if chapter < 0 && currentPage >= 0 {
		chapter = chapterOfPage(layout, currentPage)
	}
	if chapter < 0 {
		return
	}
	// The reading position as one number. A location orders by content
	// offset, which works in both modes and is finer than a page; a bare page
	// index orders by page. The two are never mixed: every row of one pass is
	// compared in the same unit.
	key, ref, haveRef := offsetKey, 0.0, false
	if currentChapter < 0 {
		key = pageKey
		ref, haveRef = float64(currentPage), true
	} else {
		ref, haveRef = offsetKey(layout, current)
	}
	pick := -1
	for i := range entries {
		if entries[i].Chapter != chapter {
			continue
		}
		if pick < 0 {
			pick = i
		}
		if !haveRef || !entries[i].HasLoc {
			continue
		}
		if at, ok := key(layout, entries[i].Loc); ok && at <= ref {
			pick = i
		}
	}
	if pick >= 0 {
		entries[pick].Current = true
	}
}

// offsetKey orders a location by its content offset, which both layout modes
// define and which agrees with Layout.OffsetOf.
func offsetKey(layout *render.Layout, loc render.Location) (float64, bool) {
	if layout == nil {
		return 0, false
	}
	if y, ok := layout.OffsetOf(loc); ok {
		return float64(y), true
	}
	return 0, false
}

// pageKey orders a location by page.
func pageKey(layout *render.Layout, loc render.Location) (float64, bool) {
	if layout == nil {
		return 0, false
	}
	if page, ok := layout.PageOf(loc); ok {
		return float64(page), true
	}
	return 0, false
}

// chapterOfPage returns the chapter whose page range contains page, or -1.
func chapterOfPage(layout *render.Layout, page int) int {
	for i := 0; ; i++ {
		first, count, ok := layout.PageRange(i)
		if !ok {
			return -1
		}
		if count > 0 && page >= first && page < first+count {
			return i
		}
	}
}

// PageOf returns the page clicking e should jump to.
//
// The layout wins over the page stored in e, so entries built from an older
// layout still land on the right page after a relayout, as long as the chapter
// still exists. When the layout does not cover e.Chapter, the stored page is
// used if it is still inside the layout. Without a layout, ok is false. A scroll
// layout has no pages: use e.Loc and Layout.OffsetOf instead.
func PageOf(layout *render.Layout, e Entry) (page int, ok bool) {
	if layout == nil {
		return 0, false
	}
	if e.HasLoc {
		if page, ok := layout.PageOf(e.Loc); ok {
			return page, true
		}
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
