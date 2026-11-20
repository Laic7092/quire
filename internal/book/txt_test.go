package book

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/laic7092/layx/render"
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

// A Windows editor can save UTF-16 with no BOM at all. Read as UTF-8 the file
// is a page of tofu, so the NUL pattern has to decide the byte order.
func TestTextUTF16WithoutBOM(t *testing.T) {
	// CRLF, the Windows export that produces such a file in the first place.
	src := "第一章 开始\r\n\r\n中文内容，短句。\r\n\r\n第二章 结束\r\n\r\n在这里。\r\n"
	for _, bigEndian := range []bool{false, true} {
		name := "utf16le-nobom.txt"
		if bigEndian {
			name = "utf16be-nobom.txt"
		}
		var buf bytes.Buffer
		for _, r := range src {
			if r > 0xFFFF {
				t.Fatalf("test text has a surrogate pair: %U", r)
			}
			if bigEndian {
				buf.WriteByte(byte(r >> 8))
				buf.WriteByte(byte(r))
			} else {
				buf.WriteByte(byte(r))
				buf.WriteByte(byte(r >> 8))
			}
		}
		if text, enc := decodeText(buf.Bytes()); text != src || !strings.HasPrefix(enc, "UTF-16") {
			t.Errorf("%s: decodeText chose %q and %d runes", name, enc, utf8.RuneCountInString(text))
		}
		b, err := Open(writeTmp(t, name, buf.Bytes()))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(b.Chapters) != 2 {
			t.Fatalf("%s: chapters %d, want 2", name, len(b.Chapters))
		}
		if got := b.Chapters[0].Title; got != "第一章 开始" {
			t.Errorf("%s: first title %q", name, got)
		}
		if got := chapterText(mustChapter(t, b, 1)); !strings.Contains(got, "在这里") {
			t.Errorf("%s: chapter text %q", name, got)
		}
		if b.Language != "zh" {
			t.Errorf("%s: language %q", name, b.Language)
		}
	}
}

// A file of nothing but whitespace must fail with a message that names the
// file, not open as a book with one empty page.
func TestTextEmptyFiles(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty.txt":      nil,
		"whitespace.txt": []byte(" \n\t\n\u3000\n\r\n"),
	} {
		file := writeTmp(t, name, data)
		b, err := Open(file)
		if err == nil {
			t.Fatalf("%s: opened as a book with %d chapters", name, len(b.Chapters))
		}
		if b != nil {
			t.Errorf("%s: book returned with an error", name)
		}
		if !strings.Contains(err.Error(), file) {
			t.Errorf("%s: %q does not name the file", name, err)
		}
	}
}

// A text saved as one enormous line (a scraped page, or an export with the
// newlines lost) must not become one <p> the layouter has to lay out in a
// single block.
func TestTextSingleLongLineSplits(t *testing.T) {
	sentence := "这是一段没有换行的文本，用来检查超长行。"
	src := strings.Repeat(sentence, 4000) // 84000 runes, no newline at all
	b, err := Open(writeTmp(t, "one-line.txt", []byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) < 2 {
		t.Fatalf("chapters %d, want more than one", len(b.Chapters))
	}
	var got strings.Builder
	for i, ch := range b.Chapters {
		for _, p := range paragraphTexts(ch.XHTML) {
			if n := utf8.RuneCountInString(p); n > maxChapterRunes {
				t.Errorf("chapter %d has a %d rune paragraph, limit is %d", i, n, maxChapterRunes)
			}
			got.WriteString(p)
		}
	}
	if got.String() != src {
		t.Errorf("the split lost text: %d runes in, %d runes out",
			utf8.RuneCountInString(src), utf8.RuneCountInString(got.String()))
	}
}

// When a chapter is cut because it grew too large, the heading that follows
// must not leave an empty second fragment behind.
func TestTextNoEmptyChapterAfterOverflow(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("第一章\n\n")
	sb.WriteString(strings.Repeat("字", maxChapterRunes))
	sb.WriteString("\n\n第二章\n\n第二章的正文。\n")
	b, err := Open(writeTmp(t, "overflow.txt", []byte(sb.String())))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 2 {
		var titles []string
		for _, ch := range b.Chapters {
			titles = append(titles, fmt.Sprintf("%q(%d)", ch.Title, paragraphCount(ch.XHTML)))
		}
		t.Fatalf("chapters %v, want the two real ones", titles)
	}
	for i, ch := range b.Chapters {
		if paragraphCount(ch.XHTML) == 0 {
			t.Errorf("chapter %d (%q) has no text", i, ch.Title)
		}
	}
}

// A file saved one paragraph per line has no blank lines, and its final
// newline must not be read as one: a poem would otherwise be joined into a
// single paragraph, since its lines do not end with sentence punctuation.
func TestTextTrailingNewlineIsNotABlankLine(t *testing.T) {
	poem := "床前明月光\n疑是地上霜\n举头望明月\n低头思故乡\n"
	b, err := Open(writeTmp(t, "poem.txt", []byte(poem)))
	if err != nil {
		t.Fatal(err)
	}
	if got := paragraphCount(b.Chapters[0].XHTML); got != 4 {
		t.Fatalf("paragraphs %d, want 4 (one per line)", got)
	}
}

// A heading's padded separator leaves a run of spaces behind, and a tab
// becomes one. The table of contents should show a single gap.
func TestTextHeadingSpacesCollapsed(t *testing.T) {
	src := "第一章    开始\n\n正文一。\n\n第二章\t\t结束\n\n正文二。\n"
	b, err := Open(writeTmp(t, "headings.txt", []byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"第一章 开始", "第二章 结束"}
	if len(b.Chapters) != len(want) {
		t.Fatalf("chapters %d, want %d", len(b.Chapters), len(want))
	}
	for i, title := range want {
		if got := b.Chapters[i].Title; got != title {
			t.Errorf("title %d is %q, want %q", i, got, title)
		}
	}
}

// Control characters have no glyph and are invalid in XML: a file carrying
// them must lose them, not hand them to the layouter.
func TestTextControlCharactersDropped(t *testing.T) {
	src := "第一段正文\x00\x01\x0b\x7f。\n\n第二段正文。\n"
	b, err := Open(writeTmp(t, "controls.txt", []byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	xhtml := string(b.Chapters[0].XHTML)
	for _, bad := range []string{"\x00", "\x01", "\x0b", "\x7f"} {
		if strings.Contains(xhtml, bad) {
			t.Errorf("XHTML still carries %q: %q", bad, xhtml)
		}
	}
	if !strings.Contains(xhtml, "第一段正文。") {
		t.Errorf("the text around the controls was not kept: %q", xhtml)
	}
}

// CR-only line endings (old Mac exports) must split lines like CRLF does.
func TestTextCROnlyLineEndings(t *testing.T) {
	src := "第一章\r\r第一段。\r\r第二段。\r"
	b, err := Open(writeTmp(t, "cr.txt", []byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	if got := paragraphCount(b.Chapters[0].XHTML); got != 2 {
		t.Fatalf("paragraphs %d, want 2", got)
	}
}

// Chapter numbering comes in several shapes, including one with spaces around
// the number. Body text that merely starts with 第… must not match: a line with
// sentence punctuation or one that is too long stays a paragraph.
func TestLooksLikeHeading(t *testing.T) {
	headings := []string{
		"第一章 风起",
		"第 1 章 风起",
		"第１２章",
		"第十二回 风波",
		"第一卷",
		"卷二 闲情",
		"序章",
		"楔子",
		"尾声",
		"Chapter IV",
		"chapter 12",
	}
	for _, line := range headings {
		if !looksLikeHeading(line) {
			t.Errorf("%q was not recognized as a heading", line)
		}
	}
	body := []string{
		"第一次见面时，他还小。",                        // 。 makes it body text
		"第一章里，作者写道。",                         // ， makes it body text
		"第二天早上，他醒了。",                         // no 章 after the number
		"第三章的内容很长" + strings.Repeat("很", 40), // too long
	}
	for _, line := range body {
		if looksLikeHeading(line) {
			t.Errorf("%q was taken for a heading", line)
		}
	}
}

// splitParagraph must not lose a rune, and should cut at a sentence end when
// one is near the limit.
func TestSplitParagraph(t *testing.T) {
	plain := strings.Repeat("字", maxChapterRunes+5000)
	pieces := splitParagraph(plain, maxChapterRunes)
	if len(pieces) != 2 {
		t.Fatalf("pieces %d, want 2", len(pieces))
	}
	if got := utf8.RuneCountInString(pieces[0]); got != maxChapterRunes {
		t.Errorf("first piece %d runes, want %d", got, maxChapterRunes)
	}
	if strings.Join(pieces, "") != plain {
		t.Error("the pieces do not add up to the paragraph")
	}

	// A sentence end inside the last quarter of the piece moves the cut there.
	sentence := strings.Repeat("甲", maxChapterRunes-10) + "。" + strings.Repeat("乙", 100)
	pieces = splitParagraph(sentence, maxChapterRunes)
	if len(pieces) != 2 {
		t.Fatalf("pieces %d, want 2", len(pieces))
	}
	if !strings.HasSuffix(pieces[0], "。") {
		t.Errorf("first piece ends %q, want a sentence end", lastRunes(pieces[0], 8))
	}
	if strings.Join(pieces, "") != sentence {
		t.Error("the sentence split lost text")
	}

	if pieces := splitParagraph("短段。", maxChapterRunes); len(pieces) != 1 {
		t.Errorf("a short paragraph was split into %d pieces", len(pieces))
	}

	// Wherever the sentence ends fall, every piece stays inside the limit and
	// no rune is lost.
	mixed := strings.Repeat("甲。乙，丙丁戊。", 9000)
	pieces = splitParagraph(mixed, 1000)
	if strings.Join(pieces, "") != mixed {
		t.Error("the sentence split lost text")
	}
	for i, piece := range pieces {
		if n := utf8.RuneCountInString(piece); n == 0 || n > 1000 {
			t.Fatalf("piece %d has %d runes, limit is 1000", i, n)
		}
	}
}

func TestCleanText(t *testing.T) {
	in := "\uFEFF甲\r\n乙\r丙\td\x00\x0b\x7f\xffe\uFFFE"
	want := "甲\n乙\n丙 de"
	if got := cleanText(in); got != want {
		t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
	}
	// A real replacement character in the source is text, not noise.
	if got := cleanText("甲\uFFFD乙"); got != "甲\uFFFD乙" {
		t.Errorf("cleanText dropped a real U+FFFD: %q", got)
	}
}

func TestHeadingTitle(t *testing.T) {
	for in, want := range map[string]string{
		"第一章\u3000序":    "第一章\u3000序", // an ideographic space is the author's
		"  第一章    开始  ": "第一章 开始",
		"CHAPTER   I":   "CHAPTER I",
		"第一卷 閨房記樂":      "第一卷 閨房記樂",
	} {
		if got := headingTitle(in); got != want {
			t.Errorf("headingTitle(%q) = %q, want %q", in, got, want)
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

// paragraphTexts is the text of each <p> of a text chapter, in order. The
// XHTML is the one this package writes, so no parser is needed to read it.
func paragraphTexts(xhtml []byte) []string {
	s := string(xhtml)
	var out []string
	for {
		open := strings.Index(s, "<p>")
		if open < 0 {
			return out
		}
		s = s[open+len("<p>"):]
		close := strings.Index(s, "</p>")
		if close < 0 {
			return out
		}
		out = append(out, s[:close])
		s = s[close+len("</p>"):]
	}
}

// lastRunes is the last n runes of s, for error messages.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
