package toc

import (
	"context"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render"
)

// sampleBook opens the committed sample every layout test uses.
func sampleBook(t *testing.T) *book.Book {
	t.Helper()
	b, err := book.Open("../book/testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sampleLayout lays the book out with a real font, or skips when the machine
// has no CJK font to shape with.
func sampleLayout(t *testing.T, b *book.Book) *render.Layout {
	t.Helper()
	path, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no font available, skipping layout test: %v", err)
	}
	fonts, err := reader.OpenFont(path)
	if err != nil {
		t.Skipf("cannot open font %s: %v", path, err)
	}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 640, reader.Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

// fakeBook makes a book whose chapters need no archive, for the rules that do
// not involve pagination.
func fakeBook(titles ...string) *book.Book {
	b := &book.Book{Title: "测试书"}
	for _, title := range titles {
		body := "<html><body><p>" + title + "。</p></body></html>"
		b.Chapters = append(b.Chapters, book.Chapter{Title: title, XHTML: []byte(body)})
	}
	return b
}

func TestBuildWithoutLayout(t *testing.T) {
	b := fakeBook("第一卷 风起", "第一章 相遇", "第二章 离别", "卷三", "第三章 尾声", "第二部 归途")
	entries := Build(b, nil, 0)
	if len(entries) != len(b.Chapters) {
		t.Fatalf("entries = %d, want %d", len(entries), len(b.Chapters))
	}
	wantLevels := []int{0, 1, 1, 0, 1, 0}
	for i, e := range entries {
		if e.Chapter != i || e.Title != b.Chapters[i].Title {
			t.Errorf("entry %d = {%q chapter %d}, want {%q chapter %d}",
				i, e.Title, e.Chapter, b.Chapters[i].Title, i)
		}
		if e.Level != wantLevels[i] {
			t.Errorf("entry %d (%q) level = %d, want %d", i, e.Title, e.Level, wantLevels[i])
		}
		if e.Page != -1 || e.Pages != 0 {
			t.Errorf("entry %d without a layout has page %d/%d, want -1/0", i, e.Page, e.Pages)
		}
		if e.Current {
			t.Errorf("entry %d is Current without a layout", i)
		}
	}
}

func TestBuildEmptyBook(t *testing.T) {
	if got := Build(nil, nil, 0); got != nil {
		t.Errorf("Build(nil, nil, 0) = %v, want nil", got)
	}
	if got := Build(&book.Book{}, nil, 3); got != nil {
		t.Errorf("Build(empty, nil, 3) = %v, want nil", got)
	}
}

func TestPageOfWithoutLayout(t *testing.T) {
	if _, ok := PageOf(nil, Entry{Chapter: 0, Page: 4}); ok {
		t.Error("PageOf(nil, ...) reported a page")
	}
}

func TestBuildFromLayout(t *testing.T) {
	b := sampleBook(t)
	layout := sampleLayout(t, b)

	entries := Build(b, layout, -1)
	if len(entries) != len(b.Chapters) {
		t.Fatalf("entries = %d, want %d", len(entries), len(b.Chapters))
	}
	if len(layout.ChapterPages()) != len(b.Chapters) {
		t.Fatalf("layout covers %d chapters, book has %d", len(layout.ChapterPages()), len(b.Chapters))
	}

	total := 0
	for i, e := range entries {
		first, count, ok := layout.PageRange(e.Chapter)
		if !ok || count == 0 {
			t.Fatalf("no page range for chapter %d", i)
		}
		if e.Page != first || e.Pages != count {
			t.Errorf("entry %d pages = %d/%d, want %d/%d", i, e.Page, e.Pages, first, count)
		}
		if e.Page < 0 || e.Pages < 1 {
			t.Errorf("entry %d has no pages: %d/%d", i, e.Page, e.Pages)
		}
		if i > 0 && e.Page < entries[i-1].Page {
			t.Errorf("entry %d starts at page %d, before entry %d at %d", i, e.Page, i-1, entries[i-1].Page)
		}
		page, ok := PageOf(layout, e)
		if !ok {
			t.Fatalf("PageOf(chapter %d) found no page", i)
		}
		if page < first || page >= first+count {
			t.Errorf("PageOf(chapter %d) = %d, outside [%d, %d)", i, page, first, first+count)
		}
		if e.Current {
			t.Errorf("entry %d is Current with currentPage -1", i)
		}
		total += count
	}
	if total != layout.PageCount() {
		t.Errorf("chapter pages sum to %d, layout has %d", total, layout.PageCount())
	}
}

func TestBuildMarksCurrentChapter(t *testing.T) {
	b := sampleBook(t)
	layout := sampleLayout(t, b)

	// The first page of the second chapter belongs to the second chapter only.
	second := layout.ChapterPages()[1]
	entries := Build(b, layout, second)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Current {
		t.Error("chapter 0 is Current at the start of chapter 1")
	}
	if !entries[1].Current {
		t.Error("chapter 1 is not Current at its first page")
	}

	// A page past the end belongs to no chapter.
	entries = Build(b, layout, layout.PageCount()+10)
	for i, e := range entries {
		if e.Current {
			t.Errorf("entry %d is Current past the last page", i)
		}
	}
}

// A layout from another book must degrade to missing pages, not panic.
func TestBuildStaleLayout(t *testing.T) {
	layout := sampleLayout(t, sampleBook(t)) // two chapters

	longer := fakeBook("第一章 甲", "第二章 乙", "第三章 丙")
	entries := Build(longer, layout, 0)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for i := 0; i < 2; i++ {
		first, _, _ := layout.PageRange(i)
		if entries[i].Page != first {
			t.Errorf("entry %d page = %d, want %d", i, entries[i].Page, first)
		}
	}
	if entries[2].Page != -1 || entries[2].Pages != 0 {
		t.Errorf("chapter past the layout has pages %d/%d, want -1/0", entries[2].Page, entries[2].Pages)
	}

	shorter := fakeBook("第一章 甲")
	entries = Build(shorter, layout, 0)
	if len(entries) != 1 || entries[0].Page < 0 {
		t.Fatalf("a book shorter than its layout lost its page: %+v", entries)
	}
}

func TestPageOfPrefersLayout(t *testing.T) {
	b := sampleBook(t)
	layout := sampleLayout(t, b)

	// A stale page in the entry is replaced by the layout's current one.
	stale := Entry{Chapter: 1, Page: 0, Pages: 1}
	page, ok := PageOf(layout, stale)
	if !ok {
		t.Fatal("PageOf(stale) found no page")
	}
	first, _, _ := layout.PageRange(1)
	if page != first {
		t.Errorf("PageOf(stale) = %d, want %d", page, first)
	}

	// A chapter the layout does not know falls back to the stored page when
	// that page still exists.
	gone := Entry{Chapter: 99, Page: 1}
	if page, ok := PageOf(layout, gone); !ok || page != 1 {
		t.Errorf("PageOf(stale chapter) = %d, %v, want 1, true", page, ok)
	}
	// ... and reports nothing when even that page is out of the layout.
	if _, ok := PageOf(layout, Entry{Chapter: 99, Page: layout.PageCount()}); ok {
		t.Error("PageOf resolved a page outside the layout")
	}
	if _, ok := PageOf(layout, Entry{Chapter: 99, Page: -1}); ok {
		t.Error("PageOf resolved an entry with no page")
	}
}

// The levels rule is fixed and small, so pin it down.
func TestLevelOf(t *testing.T) {
	cases := map[string]int{
		"第一卷 风起":    0,
		"第一卷":       0,
		"第1卷":       0,
		"第一百零八部":    0,
		"卷三":        0,
		"卷":         0,
		"第一章 初见":    1,
		"第二章":       1,
		"序章":        1,
		"第一章卷宗":     1,
		"":          1,
		" 第三篇 归途":   0,
		"Chapter 3": 1,
		"上部":        1,
	}
	for title, want := range cases {
		if got := levelOf(title); got != want {
			t.Errorf("levelOf(%q) = %d, want %d", title, got, want)
		}
	}
}

func TestSelectCurrent(t *testing.T) {
	p := &Panel{Entries: []Entry{{Chapter: 0}, {Chapter: 1, Current: true}}, Selected: -1}
	if !p.SelectCurrent() {
		t.Fatal("SelectCurrent found nothing")
	}
	if p.Selected != 1 {
		t.Errorf("Selected = %d, want 1", p.Selected)
	}
	empty := &Panel{}
	if empty.SelectCurrent() {
		t.Error("SelectCurrent found a current chapter in an empty panel")
	}
}

func TestPageLabel(t *testing.T) {
	if got := pageLabel(Entry{Page: 4, Pages: 1}); got != "5" {
		t.Errorf("single page label = %q, want 5", got)
	}
	if got := pageLabel(Entry{Page: 4, Pages: 3}); got != "5–7" {
		t.Errorf("range label = %q, want 5–7", got)
	}
}

func TestEntryTitleIsTrimmedOfNothing(t *testing.T) {
	// Build must not rewrite titles: the panel shows what book gave it.
	b := fakeBook("  第三章  ")
	entries := Build(b, nil, 0)
	if len(entries) != 1 || entries[0].Title != "  第三章  " {
		t.Fatalf("title = %q, want it unchanged", entries[0].Title)
	}
	if entries[0].Level != 1 {
		t.Errorf("level = %d, want 1", entries[0].Level)
	}
	if !strings.Contains(entries[0].Title, "第三章") {
		t.Error("title lost its text")
	}
}
