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
	// modeBlank uses blank lines: the usual shape of Gutenberg texts and many
	// exported novels. Lines inside a block are hard-wrapped and joined.
	modeBlank paragraphMode = iota
	// modeIndent starts a paragraph at an indented line.
	modeIndent
	// modeLine uses one paragraph per line, the shape most Chinese web novels
	// are saved in.
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
// chapters by headings and paragraphs, and reuses the same Chapter → render
// pipeline as EPUB.
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
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	b := &Book{
		Title:    textTitle(file),
		Language: textLanguage(text),
		Chapters: chapters,
		path:     abs,
		images:   map[string]imageEntry{},
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
		acc      string
		base     string // the heading of the logical chapter
		part     int    // fragments of one logical chapter
		runes    int
	)

	flushParagraph := func() {
		if acc == "" {
			return
		}
		paras = append(paras, acc)
		runes += utf8.RuneCountInString(acc)
		acc = ""
	}
	emit := func() {
		flushParagraph()
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

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			flushParagraph()
			continue
		}
		if looksLikeHeading(line) {
			emit()
			base, part = line, 0
			continue
		}
		switch mode {
		case modeBlank:
			if acc == "" {
				acc = line
			} else {
				acc = joinWrapped(acc, line)
			}
		case modeIndent:
			if isIndentedLine(raw) {
				flushParagraph()
				acc = line
			} else if acc == "" {
				acc = line
			} else {
				acc = joinWrapped(acc, line)
			}
		default: // modeLine
			paras = append(paras, line)
			runes += utf8.RuneCountInString(line)
		}
		if runes >= maxChapterRunes || utf8.RuneCountInString(acc) >= maxChapterRunes {
			emit()
		}
	}
	emit()
	return chapters
}

// detectParagraphMode samples the file and picks how to find paragraphs.
func detectParagraphMode(lines []string) paragraphMode {
	sampled, blanks, indented, nonEmpty := 0, 0, 0, 0
	for _, raw := range lines {
		if sampled++; sampled > 400 {
			break
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			blanks++
			continue
		}
		nonEmpty++
		if isIndentedLine(raw) {
			indented++
		}
	}
	switch {
	case blanks > 0:
		return modeBlank
	case nonEmpty > 0 && indented*2 >= nonEmpty:
		return modeIndent
	default:
		return modeLine
	}
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
