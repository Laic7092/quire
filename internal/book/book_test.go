package book

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

// writeImageEPUB writes a book whose first spine item is a cover page made of
// one SVG-wrapped raster image, followed by one text chapter with an inline
// image.
func writeImageEPUB(t *testing.T, file string) {
	t.Helper()
	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 0xc0, G: 0x20, B: 0x20, A: 0xff})
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>有图的书</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="pic" href="pic.png" media-type="image/png" properties="cover-image"/></manifest><spine><itemref idref="cover"/><itemref idref="ch1"/></spine></package>`),
		"OEBPS/cover.xhtml":      []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><div><svg viewBox="0 0 2 3"><image width="2" height="3" xlink:href="pic.png"/></svg></div></body></html>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h1>正文</h1><p>Hello image.</p><img src="pic.png" width="2" height="3"/></body></html>`),
		"OEBPS/pic.png":          pic.Bytes(),
	}
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCoverAndInlineImages(t *testing.T) {
	file := filepath.Join(t.TempDir(), "images.epub")
	writeImageEPUB(t, file)

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasCover {
		t.Fatal("cover image not detected")
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1 (the cover page must leave the reading flow)", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "正文" {
		t.Errorf("chapter title %q", got)
	}

	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter 0 missing")
	}
	var found []render.Node
	for _, n := range ch.Nodes {
		if n.Kind == render.NodeImage {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("image nodes %d, want 1", len(found))
	}
	if found[0].Width != 2 || found[0].Height != 3 {
		t.Errorf("image size %gx%g, want 2x3", found[0].Width, found[0].Height)
	}
	if _, ok := images[found[0].Image]; !ok {
		t.Error("inline image bytes missing")
	}
	if _, ok := images[b.Cover]; !ok {
		t.Error("cover bytes missing")
	}
	if !strings.Contains(chapterText(ch), "Hello image.") {
		t.Errorf("chapter text %q", chapterText(ch))
	}
}

func TestEmptyChapterGetsAPlaceholder(t *testing.T) {
	ch, err := convertChapter([]byte(`<html><body><div></div></body></html>`), "x", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := chapterText(ch); !strings.Contains(got, "没有可显示的文本") {
		t.Errorf("placeholder missing, chapter text %q", got)
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
