package book

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

const (
	// maxTextBytes keeps one accidental huge file from exhausting memory.
	maxTextBytes = 64 << 20
	// maxChapterRunes bounds a single chapter when a text has no headings or
	// one enormous block. A typical web novel chapter is a few thousand runes.
	maxChapterRunes = 20000
	// maxHeadingRunes keeps a normal sentence that merely starts with 第…
	// from being taken for a chapter title.
	maxHeadingRunes = 40
)

// paragraphMode says how a text file marks paragraph boundaries.
type paragraphMode uint8

const (
	// modeBlock uses blank lines to end a block, the usual shape of Gutenberg
	// texts and many exported novels. Inside one block, indented lines and
	// lines that read as finished paragraphs start a new paragraph; the rest
	// are the hard-wrapped remains of the one before.
	modeBlock paragraphMode = iota
	// modeLine uses one paragraph per line with no blank lines anywhere, the
	// shape most Chinese web novels are saved in.
	modeLine
)

var (
	cnChapter = regexp.MustCompile(`^第[0-9０-９零〇一二三四五六七八九十百千万两]+[章节節回卷篇部集幕]`)
	cnVolume  = regexp.MustCompile(`^[卷部篇][0-9０-９零〇一二三四五六七八九十百千万两]+`)
	enHeading = regexp.MustCompile(`(?i)^(chapter|volume|part|book)\s+([0-9]+|[ivxlcdm]+)\b`)

	specialHeadings = []string{
		"序", "序章", "序言", "楔子", "引子", "前言", "后记", "後記",
		"尾声", "尾聲", "终章", "終章", "番外", "完本感言", "作者的话", "作者的話",
	}
)

// openText reads a plain text book: it decodes the file, splits it into
// chapters by headings and paragraphs, and reuses the EPUB module's
// Chapter → render.Document pipeline.
func openText(file string) (*Book, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("book: open %s: %w", file, err)
	}
	if info.Size() > maxTextBytes {
		return nil, fmt.Errorf("book: %s is %d bytes, larger than the %d byte limit", file, info.Size(), maxTextBytes)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("book: read %s: %w", file, err)
	}

	text, encodingName := decodeText(data)
	chapters := splitText(text)
	// A text book is an EPUB book with no archive: only the exported fields
	// are set, so Build has no images to resolve and no anchors to install.
	b := &Book{
		Title:    textTitle(file),
		Language: textLanguage(text),
		Chapters: chapters,
	}
	if strings.HasPrefix(encodingName, "GB") || encodingName == "Big5" {
		b.Language = "zh"
	}
	if len(b.Chapters) == 0 {
		return nil, fmt.Errorf("book: %s has no readable text", file)
	}
	return b, nil
}

// textTitle is the file name without its extension, the only title a plain
// text file carries.
func textTitle(file string) string {
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	name = strings.TrimSpace(name)
	if name == "" {
		return "未命名书籍"
	}
	return name
}

// textLanguage guesses a BCP 47 tag from the script. A Chinese sample is
// enough to pick zh; anything else stays unset and the engine uses its
// default.
func textLanguage(text string) string {
	han, total := 0, 0
	for _, r := range text {
		if total++; total > 4000 {
			break
		}
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	if total > 0 && han*4 >= total {
		return "zh"
	}
	return ""
}

// decodeText turns the file bytes into UTF-8. BOMs win; valid UTF-8 needs no
// guessing; otherwise GB18030 and Big5 are both tried and the one that reads
// as more common Chinese wins. GB18030 covers GBK and GB2312.
func decodeText(data []byte) (string, string) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return string(data[3:]), "UTF-8"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeUTF16(data[2:], false), "UTF-16LE"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16(data[2:], true), "UTF-16BE"
	}
	if utf8.Valid(data) {
		return string(data), "UTF-8"
	}

	gb, gbErr := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	big5, big5Err := traditionalchinese.Big5.NewDecoder().Bytes(data)

	best, bestName, bestScore := "", "UTF-8", scoreText(string(data))
	for _, candidate := range []struct {
		name string
		data []byte
		err  error
	}{
		{"GB18030", gb, gbErr},
		{"Big5", big5, big5Err},
	} {
		if candidate.err != nil && len(candidate.data) == 0 {
			continue
		}
		if score := scoreText(string(candidate.data)); score > bestScore {
			best, bestName, bestScore = string(candidate.data), candidate.name, score
		}
	}
	if best == "" {
		return string(data), "UTF-8"
	}
	return best, bestName
}

func decodeUTF16(data []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if bigEndian {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
		} else {
			units = append(units, uint16(data[i+1])<<8|uint16(data[i]))
		}
	}
	return string(utf16.Decode(units))
}

// scoreText rates a decoding by how much common Chinese it contains and how
// much replacement or control noise it produced.
func scoreText(s string) int {
	const common = "的一是不了在人有我他为这之大来以个中上们到说国和地也子时道出而要于就下得可你年生"
	score, seen := 0, 0
	for _, r := range s {
		if seen++; seen > 4000 {
			break
		}
		switch {
		case r == utf8.RuneError:
			score -= 20
		case strings.ContainsRune(common, r):
			score += 4
		case unicode.Is(unicode.Han, r):
			score++
		case r < 0x20 && r != '\n' && r != '\r' && r != '\t':
			score -= 20
		}
	}
	return score
}

// splitText turns a whole text file into chapters of XHTML, so the rest of
// the pipeline does not know the book was not an EPUB.
func splitText(text string) []Chapter {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\uFEFF", "")
	text = strings.ReplaceAll(text, "\t", " ")

	lines := strings.Split(text, "\n")
	mode := detectParagraphMode(lines)

	var (
		chapters []Chapter
		paras    []string
		block    []string // raw lines between two blank lines
		base     string   // the heading of the logical chapter
		part     int      // fragments of one logical chapter
		runes    int
	)

	emit := func() {
		if len(paras) == 0 && base == "" {
			return
		}
		title := base
		switch {
		case title == "":
			title = fmt.Sprintf("第 %d 节", len(chapters)+1)
		case part > 0:
			title = fmt.Sprintf("%s（%d）", base, part+1)
		}
		chapters = append(chapters, Chapter{Title: title, XHTML: textChapter(title, paras)})
		paras, runes = nil, 0
		if base != "" {
			part++
		}
	}
	// flushBlock turns the lines collected since the last blank line into
	// paragraphs. A block is only joined as one paragraph when its lines are
	// hard-wrapped; one paragraph per line stays one <p> per line, which is
	// what makes a text book readable.
	flushBlock := func() {
		for _, p := range blockParagraphs(block, mode) {
			paras = append(paras, p)
			runes += utf8.RuneCountInString(p)
			if runes >= maxChapterRunes {
				emit()
			}
		}
		block = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			flushBlock()
			continue
		}
		if looksLikeHeading(line) {
			flushBlock()
			emit()
			base, part = line, 0
			continue
		}
		block = append(block, raw)
	}
	flushBlock()
	emit()
	return chapters
}

// blockParagraphs turns the lines between two blank lines into paragraphs.
// modeLine is already one paragraph per line; otherwise a line starts a new
// paragraph when it is indented, when the line before it closed its paragraph,
// and in a block whose lines each read as a finished paragraph, on every line.
func blockParagraphs(block []string, mode paragraphMode) []string {
	if len(block) == 0 {
		return nil
	}
	if mode == modeLine || blockIsParagraphs(block) {
		out := make([]string, 0, len(block))
		for _, raw := range block {
			if line := strings.TrimSpace(raw); line != "" {
				out = append(out, line)
			}
		}
		return out
	}
	width := wrapWidth(block)
	var (
		out  []string
		acc  string
		prev string
	)
	for _, raw := range block {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case acc == "":
			acc = line
		case isIndentedLine(raw) || closesParagraph(prev, width):
			out = append(out, acc)
			acc = line
		default:
			acc = joinWrapped(acc, line)
		}
		prev = line
	}
	if acc != "" {
		out = append(out, acc)
	}
	return out
}

// wrapWidth is the width hard-wrapped lines are cut at: the longest line of the
// block, since a wrapped line only falls short when the next word did not fit.
func wrapWidth(block []string) int {
	width := 0
	for _, raw := range block {
		if n := utf8.RuneCountInString(strings.TrimSpace(raw)); n > width {
			width = n
		}
	}
	return width
}

// closesParagraph reports whether a hard-wrapped line ended its paragraph. Every
// line of a wrapped paragraph but the last one fills the wrap width, so a line
// well short of it that ends a sentence closed the paragraph.
func closesParagraph(line string, width int) bool {
	n := utf8.RuneCountInString(line)
	return width-n >= 2 && n*10 <= width*9 && endsParagraph(line)
}

// blockIsParagraphs reports whether a block holds whole paragraphs saved one
// per line, the shape most downloaded novels come in, instead of the lines of
// one hard-wrapped paragraph. Hard wrapping cuts most lines at the wrap width,
// so only the closing line of each paragraph ends a sentence; text saved one
// paragraph per line ends most of its lines that way.
func blockIsParagraphs(block []string) bool {
	if len(block) < 2 {
		return false
	}
	ends := 0
	for _, raw := range block {
		if endsParagraph(strings.TrimSpace(raw)) {
			ends++
		}
	}
	return ends*2 >= len(block)
}

// endsParagraph reports whether a line ends a sentence, looking past the
// closing quotes and brackets a quoted line may carry.
func endsParagraph(line string) bool {
	for line != "" {
		r, size := utf8.DecodeLastRuneInString(line)
		switch {
		case strings.ContainsRune("”’」』）】》\"'", r):
			line = strings.TrimRight(line[:len(line)-size], " \t\u3000")
		case strings.ContainsRune("。！？…‥.!?", r):
			return true
		default:
			return false
		}
	}
	return false
}

// detectParagraphMode picks how to find paragraphs. A blank line anywhere means
// the file is divided into blocks; a file with none is one paragraph per line.
func detectParagraphMode(lines []string) paragraphMode {
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			return modeBlock
		}
	}
	return modeLine
}

// isIndentedLine reports a paragraph indent: an ideographic space, or two
// spaces, before the first word.
func isIndentedLine(raw string) bool {
	return strings.HasPrefix(raw, "\u3000") || strings.HasPrefix(raw, "  ")
}

// joinWrapped joins two hard-wrapped lines, inserting a space only between
// ASCII word characters.
func joinWrapped(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	last, _ := utf8.DecodeLastRuneInString(a)
	first, _ := utf8.DecodeRuneInString(b)
	if isASCIISpace(last) {
		return a + b
	}
	if last < utf8.RuneSelf && first < utf8.RuneSelf && isASCIIWord(last) && isASCIIWord(first) {
		return a + " " + b
	}
	return a + b
}

func isASCIISpace(r rune) bool { return r < utf8.RuneSelf && (r == ' ' || r == '\t') }

func isASCIIWord(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// looksLikeHeading recognizes the chapter titles used by Chinese and English
// novels without a stylesheet. It is deliberately conservative: a line with
// sentence punctuation or more than maxHeadingRunes characters is body text.
func looksLikeHeading(line string) bool {
	if line == "" || utf8.RuneCountInString(line) > maxHeadingRunes {
		return false
	}
	for _, bad := range []string{"。", "！", "？", "；", "…", "，", ","} {
		if strings.Contains(line, bad) {
			return false
		}
	}
	for _, special := range specialHeadings {
		if line == special || strings.HasPrefix(line, special+" ") ||
			strings.HasPrefix(line, special+"\u3000") || strings.HasPrefix(line, special+"：") {
			return true
		}
	}
	return cnChapter.MatchString(line) || cnVolume.MatchString(line) || enHeading.MatchString(line)
}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// textChapter renders one chapter of plain text as the minimal XHTML the
// converter understands: an h1 for the heading and one p per paragraph.
func textChapter(title string, paragraphs []string) []byte {
	var b strings.Builder
	b.WriteString("<html><body>")
	if title != "" {
		b.WriteString("<h1>")
		b.WriteString(textEscaper.Replace(title))
		b.WriteString("</h1>")
	}
	for _, p := range paragraphs {
		b.WriteString("<p>")
		b.WriteString(textEscaper.Replace(p))
		b.WriteString("</p>")
	}
	b.WriteString("</body></html>")
	return []byte(b.String())
}
