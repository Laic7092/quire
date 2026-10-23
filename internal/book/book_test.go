package book

import (
	"strings"
	"testing"

	"github.com/laix/layx/render"
)

func TestOpenAndDocument(t *testing.T) {
	b, err := Open("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "最小的书" {
		t.Errorf("title %q", b.Title)
	}
	if b.Language != "zh-CN" {
		t.Errorf("language %q", b.Language)
	}
	if len(b.Chapters) != 2 {
		t.Fatalf("chapters %d, want 2", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "第一章 初见" {
		t.Errorf("chapter 1 title %q (nav titles should win)", got)
	}
	if got := b.Chapters[1].Title; got != "第二章 再见" {
		t.Errorf("chapter 2 title %q", got)
	}
	if !strings.Contains(string(b.Chapters[0].XHTML), "Hello") {
		t.Errorf("chapter 1 lost its XHTML")
	}

	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChapterCount() != 2 {
		t.Fatalf("document chapters %d, want 2", doc.ChapterCount())
	}
	if doc.Metadata().Title != b.Title || !doc.Metadata().Reflowable {
		t.Errorf("metadata %+v", doc.Metadata())
	}

	first, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter 0 missing")
	}
	text := chapterText(first)
	for _, want := range []string{
		"第一章 初见",
		"Hello reader, this is chapter one.",
		"第二段，包含中文文本。",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chapter 1 text %q lacks %q", text, want)
		}
	}

	second, ok := doc.Chapter(1)
	if !ok {
		t.Fatal("chapter 1 missing")
	}
	text = chapterText(second)
	if !strings.Contains(text, "The second chapter has") || !strings.Contains(text, "bold words") {
		t.Errorf("chapter 2 text %q", text)
	}
}

func TestDocumentSizedScalesHeadings(t *testing.T) {
	b, err := Open("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	small, err := b.DocumentSized(16)
	if err != nil {
		t.Fatal(err)
	}
	large, err := b.DocumentSized(24)
	if err != nil {
		t.Fatal(err)
	}
	h1Small := headingSize(t, small)
	h1Large := headingSize(t, large)
	if h1Large <= h1Small {
		t.Errorf("h1 size did not scale: %g then %g", h1Small, h1Large)
	}
	if want := float32(16 * 1.6); h1Small != want {
		t.Errorf("h1 small %g, want %g", h1Small, want)
	}
}

func chapterText(ch render.Chapter) string {
	var b strings.Builder
	var walk func(render.NodeID)
	walk = func(id render.NodeID) {
		if id < 0 || int(id) >= len(ch.Nodes) {
			return
		}
		n := ch.Nodes[id]
		if n.Kind == render.NodeText {
			b.WriteString(n.Text)
		}
		if n.Kind == render.NodeBreak {
			b.WriteByte('\n')
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(ch.Root)
	return b.String()
}

func headingSize(t *testing.T, doc *render.Document) float32 {
	t.Helper()
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("no chapter")
	}
	styles := doc.Styles()
	for _, n := range ch.Nodes {
		if n.Kind == render.NodeBlock && int(n.Style) < len(styles) && styles[n.Style].FontWeight == 700 {
			return styles[n.Style].FontSize
		}
	}
	t.Fatal("no heading block")
	return 0
}
