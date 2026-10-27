package toc

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
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

// writeNavEPUB writes a small EPUB 3 book whose navigation document nests: one
// section under chapter one, then chapter two. Both sections live in the same
// XHTML file, so the rows have to be told apart by their #fragment anchors.
func writeNavEPUB(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>目录测试</dc:title><dc:language>zh</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`)
	para := strings.Repeat("这一章有足够的文字，让排版能把它分成好几行。", 12)
	add("OEBPS/c1.xhtml", `<html><body><h1>第一章</h1>
<p>`+para+`</p><h2 id="one">第一节</h2><p>`+para+`</p><h2 id="two">第二节</h2><p>`+para+`</p></body></html>`)
	add("OEBPS/c2.xhtml", `<html><body><h1>第二章</h1><p>`+para+`</p></body></html>`)
	add("OEBPS/nav.xhtml", `<html><body><nav epub:type="toc"><ol>
<li><a href="c1.xhtml#one">第一节</a><ol><li><a href="c1.xhtml#two">第二节</a></li></ol></li>
<li><a href="c2.xhtml">第二章</a></li>
</ol></nav></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "nav.epub")
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// A book with a real navigation document gets its own rows, nesting and
// fragment targets, instead of one guessed row per chapter.
func TestBuildFromNavigationDocument(t *testing.T) {
	b, err := book.Open(writeNavEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	entries := Build(b, nil, -1)
	if len(entries) != 3 {
		t.Fatalf("entries = %d (%+v), want 3", len(entries), entries)
	}
	wantTitles := []string{"第一节", "第二节", "第二章"}
	wantLevels := []int{0, 1, 0}
	wantGroups := []bool{true, false, false}
	for i, e := range entries {
		if e.Title != wantTitles[i] {
			t.Errorf("entry %d title = %q, want %q", i, e.Title, wantTitles[i])
		}
		if e.Level != wantLevels[i] {
			t.Errorf("entry %d level = %d, want %d", i, e.Level, wantLevels[i])
		}
		if e.Group != wantGroups[i] {
			t.Errorf("entry %d group = %v, want %v", i, e.Group, wantGroups[i])
		}
		if e.Chapter != 0 && e.Chapter != 1 {
			t.Errorf("entry %d chapter = %d", i, e.Chapter)
		}
	}
	if entries[0].Chapter != 0 || entries[1].Chapter != 0 || entries[2].Chapter != 1 {
		t.Errorf("chapters = %d/%d/%d, want 0/0/1", entries[0].Chapter, entries[1].Chapter, entries[2].Chapter)
	}
	// The first two rows name sections inside chapter one, each with its own
	// fragment anchor; the third names chapter two itself.
	if entries[2].Anchor != render.AnchorID(2) {
		t.Errorf("chapter two anchor = %d, want 2", entries[2].Anchor)
	}
	root := render.AnchorID(len(b.Chapters))
	if entries[0].Anchor <= root || entries[1].Anchor <= root {
		t.Errorf("section anchors = %d/%d, want fragments beyond %d",
			entries[0].Anchor, entries[1].Anchor, root)
	}
	if entries[0].Anchor == entries[1].Anchor {
		t.Errorf("both sections share anchor %d", entries[0].Anchor)
	}
	if entries[0].Page != -1 || entries[0].HasLoc {
		t.Errorf("a row without a layout has page %d, loc %v", entries[0].Page, entries[0].HasLoc)
	}
}

// The rows of a navigation document land where their anchors are, and the row
// being read is the last one at or before the reading position.
func TestNavigationRowsLocateAndMarkCurrent(t *testing.T) {
	b, err := book.Open(writeNavEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := sampleLayout(t, b)
	entries := Build(b, layout, -1)
	for i, e := range entries {
		if !e.HasLoc || e.Page < 0 {
			t.Fatalf("entry %d (%q) did not resolve to a page", i, e.Title)
		}
	}

	loc, ok := layout.Locate(entries[1].Anchor)
	if !ok {
		t.Fatal("the second section has no location")
	}
	entries = BuildAt(b, layout, loc)
	if !entries[1].Current {
		t.Errorf("reading the second section marks %+v", currentOf(entries))
	}
	if entries[0].Current || entries[2].Current {
		t.Errorf("more than the second section is current: %+v", currentOf(entries))
	}

	first, ok := layout.Locate(entries[0].Anchor)
	if !ok {
		t.Fatal("the first section has no location")
	}
	entries = BuildAt(b, layout, first)
	if !entries[0].Current || entries[1].Current {
		t.Errorf("reading the first section marks %+v", currentOf(entries))
	}
}

// currentOf lists the titles marked Current, for failure messages.
func currentOf(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		if e.Current {
			out = append(out, e.Title)
		}
	}
	return out
}

// A navigation document with a single entry for a thirty-chapter book is a
// stub, not a table of contents: the chapters themselves are better rows.
func TestNavigationStubFallsBackToChapters(t *testing.T) {
	b := fakeBook("第一章 甲", "第二章 乙", "第三章 丙")
	b.Toc = []book.TOCEntry{{Title: "第一章 甲", Chapter: 0, Anchor: 1}}
	entries := Build(b, nil, -1)
	if len(entries) != len(b.Chapters) {
		t.Fatalf("entries = %d, want the %d chapters", len(entries), len(b.Chapters))
	}
	for i, e := range entries {
		if e.Chapter != i || e.Title != b.Chapters[i].Title {
			t.Errorf("entry %d = {%q chapter %d}, want a chapter row", i, e.Title, e.Chapter)
		}
	}
}

// Some books put whole paragraphs in their NCX. Those rows are dropped, and a
// document that is mostly such rows is not trusted at all.
func TestNavigationParagraphLabelsAreNotRows(t *testing.T) {
	b := fakeBook("第一章 甲", "第二章 乙")
	long := strings.Repeat("这是一整段正文，不是标题。", 10)
	b.Toc = []book.TOCEntry{
		{Title: "第一章 甲", Chapter: 0, Anchor: 1},
		{Title: long, Chapter: 0, Anchor: 3},
		{Title: long, Chapter: 1, Anchor: 4},
		{Title: long, Chapter: 1, Anchor: 5},
	}
	entries := Build(b, nil, -1)
	if len(entries) != len(b.Chapters) {
		t.Fatalf("entries = %d (%+v), want the %d chapters", len(entries), entries, len(b.Chapters))
	}
	if entries[0].Title != "第一章 甲" || entries[1].Title != "第二章 乙" {
		t.Errorf("titles = %q/%q, want the chapter titles", entries[0].Title, entries[1].Title)
	}

	// A single paragraph label among real ones is dropped row by row.
	b.Toc = []book.TOCEntry{
		{Title: "第一章 甲", Chapter: 0, Anchor: 1},
		{Title: long, Chapter: 1, Anchor: 4},
	}
	entries = Build(b, nil, -1)
	if len(entries) != 2 {
		t.Fatalf("entries = %d (%+v), want 2", len(entries), entries)
	}
	if entries[0].Anchor != 1 || entries[1].Chapter != 1 {
		t.Errorf("rows = %+v, want the nav row then chapter two's own row", entries)
	}
}

// A navigation document that nests under a root nobody lists starts at the
// shallowest level it has, and a chapter no row names still gets a row.
func TestNavigationDepthNormalized(t *testing.T) {
	b := fakeBook("第一章 甲", "第二章 乙", "第三章 丙")
	b.Toc = []book.TOCEntry{
		{Title: "第一章 甲", Chapter: 0, Anchor: 1, Depth: 1},
		{Title: "第一幕", Chapter: 0, Anchor: 4, Depth: 2},
		{Title: "第二章 乙", Chapter: 1, Anchor: 2, Depth: 1},
	}
	entries := Build(b, nil, -1)
	if len(entries) != 4 {
		t.Fatalf("entries = %d (%+v), want 4", len(entries), entries)
	}
	if entries[0].Level != 0 || entries[1].Level != 1 || entries[2].Level != 0 {
		t.Errorf("levels = %d/%d/%d, want 0/1/0", entries[0].Level, entries[1].Level, entries[2].Level)
	}
	if !entries[0].Group {
		t.Error("the row with a nested row under it is not a group")
	}
	// Chapter three has no row of its own, and still appears, after the two
	// chapters the navigation document named.
	if entries[3].Chapter != 2 || entries[3].Title != "第三章 丙" {
		t.Errorf("unnamed chapter row = %+v", entries[3])
	}
}

// A navigation row whose fragment the layout does not know still opens: it
// falls back to the top of its chapter instead of being a dead row with no
// page.
func TestNavigationRowWithoutTargetFallsBackToChapter(t *testing.T) {
	b, err := book.Open(writeNavEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := sampleLayout(t, b)
	// A stale fragment: the anchor is beyond anything the layout registered.
	b.Toc = []book.TOCEntry{
		{Title: "失效的锚点", Chapter: 0, Anchor: render.AnchorID(9999)},
		{Title: "第二章", Chapter: 1, Anchor: 2},
	}
	entries := Build(b, layout, -1)
	if len(entries) != 2 {
		t.Fatalf("entries = %d (%+v), want 2", len(entries), entries)
	}
	e := entries[0]
	if !e.HasLoc {
		t.Fatal("a row with an unknown anchor has no location")
	}
	if e.Chapter != 0 || e.Loc.Chapter != 0 {
		t.Errorf("row loc = %+v, want chapter 0", e.Loc)
	}
	first, count, ok := layout.PageRange(0)
	if !ok || count == 0 {
		t.Fatal("chapter 0 has no page range")
	}
	if e.Page < first || (e.Page >= first+count) {
		t.Errorf("row page = %d, want it inside chapter 0's range [%d, %d)", e.Page, first, first+count)
	}
	// The row jumps, so the panel does not draw it disabled.
	p := &Panel{Entries: entries, Selected: -1}
	var jumped []Entry
	p.OnJump = func(e Entry) { jumped = append(jumped, e) }
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 400)
	tt.Frame()
	if err := tt.Click("失效的锚点"); err != nil {
		t.Fatal(err)
	}
	if len(jumped) != 1 {
		t.Errorf("the fallback row did not jump: %v", jumped)
	}
}

// A plain text book has no navigation document, so its table of contents is one
// row per chapter heading, with the volume headings guessed as the top level
// and the pages of a real layout.
func TestBuildTextBook(t *testing.T) {
	b, err := book.Open("../book/testdata/samples/fusheng-liuji.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Toc) != 0 {
		t.Fatalf("the text sample has %d navigation rows, want none", len(b.Toc))
	}
	layout := sampleLayout(t, b)
	if len(layout.ChapterPages()) != len(b.Chapters) {
		t.Fatalf("layout covers %d chapters, book has %d", len(layout.ChapterPages()), len(b.Chapters))
	}

	entries := BuildAt(b, layout, render.Location{Chapter: 0, Node: 0})
	if len(entries) != len(b.Chapters) {
		t.Fatalf("entries = %d, want %d", len(entries), len(b.Chapters))
	}
	total := 0
	for i, e := range entries {
		if e.Title != b.Chapters[i].Title {
			t.Errorf("entry %d title = %q, want %q", i, e.Title, b.Chapters[i].Title)
		}
		if want := levelOf(b.Chapters[i].Title); e.Level != want {
			t.Errorf("entry %d (%q) level = %d, want %d", i, e.Title, e.Level, want)
		}
		first, count, ok := layout.PageRange(i)
		if !ok || count == 0 {
			t.Fatalf("chapter %d has no page range", i)
		}
		if e.Page != first || e.Pages != count || !e.HasLoc {
			t.Errorf("entry %d = page %d/%d hasLoc %v, want %d/%d and located",
				i, e.Page, e.Pages, e.HasLoc, first, count)
		}
		total += count
	}
	if total != layout.PageCount() {
		t.Errorf("chapter pages sum to %d, layout has %d", total, layout.PageCount())
	}
	if got := currentOf(entries); len(got) != 1 || got[0] != b.Chapters[0].Title {
		t.Errorf("reading the first chapter marks %v", got)
	}

	// The volume heading is marked while reading inside that chapter.
	entries = BuildAt(b, layout, render.Location{Chapter: 1, Node: 0})
	if got := currentOf(entries); len(got) != 1 || got[0] != b.Chapters[1].Title {
		t.Errorf("reading chapter 1 marks %v", got)
	}
}

// scrollLayout lays the same book out as one continuous column, the reader's
// other mode.
func scrollLayout(t *testing.T, b *book.Book) *render.Layout {
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
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 640,
		reader.Options{FontSize: 18, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

// A scrolling layout has no pages: its rows still locate, the panel draws no
// page for them, and BuildAt marks the section the reader is in.
func TestBuildAtScrollLayout(t *testing.T) {
	b, err := book.Open(writeNavEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := scrollLayout(t, b)
	if layout.PageCount() != 0 {
		t.Fatalf("scroll layout reports %d pages", layout.PageCount())
	}

	entries := Build(b, layout, -1)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for i, e := range entries {
		if e.Page != -1 || e.Pages != 0 {
			t.Errorf("entry %d has pages %d/%d in a scroll layout, want -1/0", i, e.Page, e.Pages)
		}
		if !e.HasLoc {
			t.Errorf("entry %d (%q) has no location", i, e.Title)
		}
		if _, ok := PageOf(layout, e); ok {
			t.Errorf("PageOf(entry %d) found a page in a scroll layout", i)
		}
	}

	loc, ok := layout.Locate(entries[1].Anchor)
	if !ok {
		t.Fatal("the second section has no location")
	}
	entries = BuildAt(b, layout, loc)
	if got := currentOf(entries); len(got) != 1 || got[0] != "第二节" {
		t.Errorf("reading the second section marks %v", got)
	}
}
