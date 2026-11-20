package book

import (
	"strings"
	"testing"
)

// The EPUB path is not this package's code any more: it opens the file through
// github.com/laic7092/layx/epub. This test only checks the wiring — that Open
// returns the module's book with its navigation titles, cover and document —
// because the parsing itself is covered by that module's own tests.
func TestOpenEPUBFromModule(t *testing.T) {
	b, err := Open("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "最小的书" || b.Language != "zh-CN" {
		t.Errorf("title %q language %q", b.Title, b.Language)
	}
	if len(b.Chapters) != 2 {
		t.Fatalf("chapters %d, want 2", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "第一章 初见" {
		t.Errorf("chapter 1 title %q (nav titles should win)", got)
	}
	if !strings.Contains(string(b.Chapters[0].XHTML), "Hello") {
		t.Error("chapter 1 lost its XHTML")
	}

	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChapterCount() != len(b.Chapters) {
		t.Errorf("document chapters %d, book chapters %d", doc.ChapterCount(), len(b.Chapters))
	}
	if !strings.Contains(allText(t, doc), "Hello reader, this is chapter one.") {
		t.Error("chapter text lost in the conversion")
	}
	if b.HasCover {
		if data, ok := images[b.Cover]; !ok || len(data) == 0 {
			t.Error("cover bytes missing")
		}
	}
}
