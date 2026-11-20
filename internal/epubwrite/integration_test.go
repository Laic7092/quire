package epubwrite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laic7092/layx/epub"
)

// TestOpenWithLayx is the acceptance test: the archive this package writes must
// be readable by the parser Quire itself uses.
func TestOpenWithLayx(t *testing.T) {
	cover := pngCover(t)
	data, err := Bytes(Book{
		Title:       "山海之间",
		Author:      "某作者",
		Language:    "zh",
		Description: "一本测试用书",
		Cover:       cover,
	}, []Chapter{
		{Title: "第一章 出发", HTML: `<p>他推开门，<em>风</em>扑面而来。</p><p>路在脚下&nbsp;延伸。</p>`},
		{Title: "第二章 归来", HTML: `<p>回家的路更短。</p><script>bad()</script>`},
		{Title: "", HTML: `<p>没有标题的一章。</p>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	book, err := epub.Open(path)
	if err != nil {
		t.Fatalf("epub.Open: %v", err)
	}
	if book.Title != "山海之间" {
		t.Errorf("title = %q", book.Title)
	}
	if book.Language != "zh" {
		t.Errorf("language = %q", book.Language)
	}
	if len(book.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(book.Chapters))
	}
	wantTitles := []string{"第一章 出发", "第二章 归来", "Chapter 3"}
	for i, want := range wantTitles {
		if book.Chapters[i].Title != want {
			t.Errorf("chapter %d title = %q, want %q", i, book.Chapters[i].Title, want)
		}
	}
	first := string(book.Chapters[0].XHTML)
	if !strings.Contains(first, "他推开门") || !strings.Contains(first, "风") || !strings.Contains(first, "路在脚下") {
		t.Errorf("first chapter lost text:\n%s", first)
	}
	second := string(book.Chapters[1].XHTML)
	if !strings.Contains(second, "回家的路更短") {
		t.Errorf("second chapter lost text:\n%s", second)
	}
	if strings.Contains(second, "bad()") {
		t.Error("script survived into a chapter")
	}
	if !strings.Contains(string(book.Chapters[2].XHTML), "没有标题的一章") {
		t.Error("untitled chapter lost its text")
	}
	if !book.HasCover {
		t.Error("cover was not recognized")
	}

	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatalf("epub.ReadMetadata: %v", err)
	}
	if meta.Title != "山海之间" || meta.Creator != "某作者" || meta.Language != "zh" {
		t.Errorf("metadata = %+v", meta)
	}
	if len(meta.Cover) == 0 || meta.CoverType != "image/png" {
		t.Errorf("cover metadata = type %q, %d bytes", meta.CoverType, len(meta.Cover))
	}
}

func TestOpenWithNoMetadata(t *testing.T) {
	data, err := Bytes(Book{}, []Chapter{{HTML: ""}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plain.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	book, err := epub.Open(path)
	if err != nil {
		t.Fatalf("epub.Open: %v", err)
	}
	if book.Title != "Untitled" {
		t.Errorf("title = %q, want Untitled", book.Title)
	}
	if len(book.Chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Chapter 1" {
		t.Errorf("chapter title = %q", book.Chapters[0].Title)
	}
}
