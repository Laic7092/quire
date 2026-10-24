package book

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/laix/layx/render"
)

func writeTmp(t *testing.T, name string, data []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// A real Gutenberg text: BOM, CRLF, full-width chapter headings, one
// indented paragraph per line and blank lines between blocks.
func TestTextBookFromSample(t *testing.T) {
	b, err := Open("testdata/samples/aq-zhuan.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "aq-zhuan" {
		t.Errorf("title %q", b.Title)
	}
	if len(b.Chapters) < 8 {
		t.Fatalf("chapters %d, want at least 8", len(b.Chapters))
	}
	var titles []string
	for _, ch := range b.Chapters {
		titles = append(titles, ch.Title)
	}
	if !containsTitle(titles, "第一章") {
		t.Errorf("no 第一章 heading in %v", titles)
	}
	if got := b.Language; got != "zh" {
		t.Errorf("language %q", got)
	}

	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChapterCount() != len(b.Chapters) {
		t.Errorf("document chapters %d, book chapters %d", doc.ChapterCount(), len(b.Chapters))
	}
	if len(images) != 0 {
		t.Errorf("a text book has no images, got %d", len(images))
	}
	if !strings.Contains(allText(t, doc), "阿") {
		t.Error("the decoded text lost its Chinese characters")
	}
}

// GB18030 is what most Chinese downloads are saved in. The file is not valid
// UTF-8, so the decoder has to pick it.
func TestTextEncodingGB18030(t *testing.T) {
	src := "第一章 开始\n\n第一段中文，用来检查 GBK 解码是否正常。\n\n第二章 结束\n\n完。\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encoded, []byte(src)) {
		t.Fatal("test data is still UTF-8")
	}
	b, err := Open(writeTmp(t, "gbk.txt", encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 2 {
		t.Fatalf("chapters %d, want 2", len(b.Chapters))
	}
	got := chapterText(mustChapter(t, b, 0))
	if !strings.Contains(got, "检查 GBK 解码") {
		t.Errorf("decoded text %q", got)
	}
	if b.Language != "zh" {
		t.Errorf("language %q", b.Language)
	}
}

func TestTextEncodingUTF16(t *testing.T) {
	src := "第一章 开始\n\n中文内容。\n"
	units := utf16.Encode([]rune(src))
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE}) // little-endian BOM
	for _, u := range units {
		buf.WriteByte(byte(u))
		buf.WriteByte(byte(u >> 8))
	}
	b, err := Open(writeTmp(t, "utf16.txt", buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1", len(b.Chapters))
	}
	if got := chapterText(mustChapter(t, b, 0)); !strings.Contains(got, "中文内容") {
		t.Errorf("decoded text %q", got)
	}
}

// Without headings the file is cut at a paragraph boundary once a chapter
// grows too large, so no page has to lay out a multi-megabyte block.
func TestTextWithoutHeadingsSplits(t *testing.T) {
	paragraph := strings.Repeat("这是一段没有章节标题的长文本。", 20) // ~280 runes
	var sb strings.Builder
	for i := 0; i < 120; i++ {
		sb.WriteString(paragraph)
		sb.WriteString("\n\n")
	}
	b, err := Open(writeTmp(t, "plain.txt", []byte(sb.String())))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) < 2 {
		t.Fatalf("chapters %d, want more than one", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "第 1 节" {
		t.Errorf("first title %q", got)
	}
}

// The usual web-novel shape: 第N章 headings, one indented line per paragraph,
// no blank lines. This is the structure 斗破苍穹 and its peers use.
func TestTextWebNovelShape(t *testing.T) {
	var sb strings.Builder
	const chapters = 60
	for i := 1; i <= chapters; i++ {
		fmt.Fprintf(&sb, "第%d章 风起于青萍之末%d\n", i, i)
		for j := 0; j < 5; j++ {
			fmt.Fprintf(&sb, "\u3000\u3000这是第%d章的第%d段，主角又一次踏上了修炼之路。\n", i, j+1)
		}
	}
	b, err := Open(writeTmp(t, "webnovel.txt", []byte(sb.String())))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != chapters {
		t.Fatalf("chapters %d, want %d", len(b.Chapters), chapters)
	}
	if got := b.Chapters[0].Title; got != "第1章 风起于青萍之末1" {
		t.Errorf("first title %q", got)
	}
	if got := b.Chapters[59].Title; got != "第60章 风起于青萍之末60" {
		t.Errorf("last title %q", got)
	}
	if got := chapterText(mustChapter(t, b, 0)); !strings.Contains(got, "第1段") {
		t.Errorf("first chapter text %q", got)
	}
}

// A downloaded novel in the shape most of them are saved in: a blank line
// before each chapter, one indented line per paragraph, and no blank line
// between the paragraphs. Every line has to stay its own <p>, or a whole
// chapter turns into one wall of text.
func TestTextOneParagraphPerIndentedLine(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&sb, "\n第%d章 风起%d\n\n", i, i)
		for j := 0; j < 4; j++ {
			fmt.Fprintf(&sb, "\u3000\u3000第%d章的第%d段正文。\n", i, j+1)
		}
	}
	b, err := Open(writeTmp(t, "indented.txt", []byte(sb.String())))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 3 {
		t.Fatalf("chapters %d, want 3", len(b.Chapters))
	}
	for i, ch := range b.Chapters {
		if got := paragraphCount(ch.XHTML); got != 4 {
			t.Errorf("chapter %d has %d paragraphs, want 4", i, got)
		}
	}
	if got := chapterText(mustChapter(t, b, 0)); !strings.Contains(got, "第1段正文") {
		t.Errorf("first chapter text %q", got)
	}
}

// A Gutenberg text: paragraphs are hard-wrapped below the block width and no
// blank line separates the wrapped paragraphs. The lines of one paragraph are
// joined back together, and two paragraphs must not merge into one.
func TestTextHardWrappedParagraphs(t *testing.T) {
	paragraphs := []string{
		strings.Repeat("甲", 45) + "。",
		strings.Repeat("乙", 38) + "。",
	}
	var sb strings.Builder
	sb.WriteString("浮生六記\n\n") // a Gutenberg header, so the file has blank lines
	for _, p := range paragraphs {
		for line := range chunks(p, 20) {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	b, err := Open(writeTmp(t, "wrapped.txt", []byte(sb.String())))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1", len(b.Chapters))
	}
	if got := paragraphCount(b.Chapters[0].XHTML); got != len(paragraphs)+1 {
		t.Fatalf("paragraphs %d, want %d plus the header line", got, len(paragraphs))
	}
	// The blocks are the header line, the chapter heading, then the paragraphs.
	blocks := blockTexts(mustChapter(t, b, 0))[2:]
	if len(blocks) != len(paragraphs) {
		t.Fatalf("blocks %d, want %d", len(blocks), len(paragraphs))
	}
	for i, want := range paragraphs {
		if blocks[i] != want {
			t.Errorf("paragraph %d is %q, want %q", i, blocks[i], want)
		}
	}
}

// A real Gutenberg EPUB whose cover page is a bare <img>, the common shape
// for an EPUB2 book.
func TestEPUBWithImageCover(t *testing.T) {
	b, err := Open("testdata/samples/aq-zhuan.epub")
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasCover {
		t.Fatal("cover not detected")
	}
	if len(b.Chapters) == 0 {
		t.Fatal("no chapters")
	}
	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChapterCount() != len(b.Chapters) {
		t.Errorf("document chapters %d, book chapters %d", doc.ChapterCount(), len(b.Chapters))
	}
	cover, ok := images[b.Cover]
	if !ok {
		t.Fatal("cover bytes missing")
	}
	if len(cover) == 0 {
		t.Error("cover bytes empty")
	}
	if !strings.Contains(allText(t, doc), "阿") {
		t.Error("chapter text lost")
	}
}

func containsTitle(titles []string, prefix string) bool {
	for _, title := range titles {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

// chunks splits a string into lines of at most n runes, the way a hard-wrapped
// text file is saved.
func chunks(s string, n int) func(func(string) bool) {
	return func(yield func(string) bool) {
		runes := []rune(s)
		for len(runes) > 0 {
			take := min(n, len(runes))
			if !yield(string(runes[:take])) {
				return
			}
			runes = runes[take:]
		}
	}
}

// paragraphCount is how many <p> one chapter of a text book carries.
func paragraphCount(xhtml []byte) int {
	return strings.Count(string(xhtml), "<p>")
}

// blockTexts is the text of each block of a chapter, in document order.
func blockTexts(ch render.Chapter) []string {
	var collect func(id render.NodeID) string
	collect = func(id render.NodeID) string {
		if id < 0 || int(id) >= len(ch.Nodes) {
			return ""
		}
		n := ch.Nodes[id]
		if n.Kind == render.NodeText {
			return n.Text
		}
		var b strings.Builder
		for _, child := range n.Children {
			b.WriteString(collect(child))
		}
		return b.String()
	}
	// The chapter body may sit inside wrapper blocks; unwrap them.
	root := ch.Root
	for root >= 0 && int(root) < len(ch.Nodes) {
		children := ch.Nodes[root].Children
		if len(children) != 1 {
			break
		}
		child := children[0]
		if child < 0 || int(child) >= len(ch.Nodes) || ch.Nodes[child].Kind != render.NodeBlock {
			break
		}
		root = child
	}
	var blocks []string
	if root < 0 || int(root) >= len(ch.Nodes) {
		return nil
	}
	for _, id := range ch.Nodes[root].Children {
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		if ch.Nodes[id].Kind != render.NodeBlock {
			continue
		}
		blocks = append(blocks, strings.TrimSpace(collect(id)))
	}
	return blocks
}

// chapterText is the text of one chapter in document order, the same shape a
// reader sees it in.
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

func mustChapter(t *testing.T, b *Book, i int) render.Chapter {
	t.Helper()
	doc, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(i)
	if !ok {
		t.Fatalf("chapter %d missing", i)
	}
	return ch
}

func allText(t *testing.T, doc *render.Document) string {
	t.Helper()
	var sb strings.Builder
	for i := 0; i < doc.ChapterCount(); i++ {
		ch, ok := doc.Chapter(i)
		if !ok {
			continue
		}
		sb.WriteString(chapterText(ch))
	}
	return sb.String()
}
