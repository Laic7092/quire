// Command corpus lays out every EPUB in a directory and prints what the book
// contains against what the reader actually represents. It is a development
// tool: it runs headless, needs no window and no font outlines, and it takes
// the same layout options as the reader so page counts are comparable.
//
// The point is not a benchmark. It is a ledger: for a book we want to know
// either "these constructs were laid out" or "these constructs were dropped",
// never a silent third case. The adapter's own gaps are not visible from the
// inside today, so the raw XHTML is counted here as well and printed next to
// what the conversion produced.
//
// Usage:
//
//	go run ./cmd/corpus -dir ~/epub-corpus
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
	"golang.org/x/net/html"
)

// Layout constants mirror internal/reader. They are duplicated rather than
// imported because internal/reader also imports the native UI package, and
// this tool must build and run without a display, GTK or cgo.
const (
	margin     = 56
	fontSize   = 18
	lineHeight = 1.6
)

type counters map[string]int

func (c counters) add(k string) { c[k]++ }

// scan counts the constructs in a book's markup, independently of the
// adapter. Both the tag census and the CSS property census are crude on
// purpose: they answer "does this book lean on X", not "did we parse X".
type scan struct {
	tags     counters
	attrs    counters
	css      counters
	chapters int
	bytes    int
}

var (
	textTags   = []string{"p", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote", "br", "hr"}
	mediaTags  = []string{"img", "svg", "image", "figure", "figcaption"}
	tableTags  = []string{"table", "tr", "td", "th", "thead", "tbody", "caption"}
	inlineTags = []string{"em", "i", "b", "strong", "sup", "sub", "code", "pre", "a", "ruby", "rt", "small"}
	// layoutCSS are the declarations that change geometry or hide/show text.
	// Anything here that the adapter ignores is a visible difference, so the
	// report prints them instead of lumping them with decoration.
	layoutCSS = []string{
		"display", "float", "position", "width", "height", "margin", "padding",
		"text-align", "text-indent", "text-transform", "white-space",
		"writing-mode", "vertical-align", "line-height", "font-size", "font-family",
		"font-weight", "font-style", "font-variant", "letter-spacing", "word-spacing",
		"columns", "column-count", "flex", "grid", "list-style", "page-break",
		"break-before", "break-after", "break-inside",
	}
)

func scanBook(path string) (scan, error) {
	s := scan{tags: counters{}, attrs: counters{}, css: counters{}}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return s, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".html") && !strings.HasSuffix(name, ".htm") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return s, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return s, err
		}
		s.chapters++
		s.bytes += len(data)
		scanMarkup(data, &s)
	}
	return s, nil
}

func scanMarkup(data []byte, s *scan) {
	z := html.NewTokenizer(bytes.NewReader(data))
	inStyle := 0
	var style strings.Builder
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(bytes.ToLower(name))
			s.tags.add(tag)
			if tag == "style" {
				inStyle++
			}
			for hasAttr {
				k, v, more := z.TagAttr()
				key := string(bytes.ToLower(k))
				s.attrs.add(key)
				if key == "style" {
					countCSS(string(v), s)
				}
				hasAttr = more
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(bytes.ToLower(name)) == "style" && inStyle > 0 {
				inStyle--
				countCSS(style.String(), s)
				style.Reset()
			}
		case html.TextToken:
			if inStyle > 0 {
				style.Write(z.Text())
			}
		}
	}
}

// countCSS counts the properties from a fixed vocabulary. A real CSS parser is
// out of scope for a ledger: what matters is whether a book uses a property at
// all, and where.
func countCSS(css string, s *scan) {
	low := strings.ToLower(css)
	for _, prop := range layoutCSS {
		if n := strings.Count(low, prop+":"); n > 0 {
			s.css[prop] += n
		}
	}
}

type result struct {
	name     string
	sizeKB   int
	tag      scan // raw markup census
	title    string
	lang     string
	chapters int
	nodes    int
	chars    int
	images   int
	pages    int
	openMS   int64
	buildMS  int64
	layoutMS int64
	paintMS  int64
	ops      int
	err      error
	panicky  string
	// imageRes is what the adapter handed to the painter; imageNodes is how
	// often the document actually places one. They differ when a chapter uses
	// the same asset twice, and a gap between them is worth seeing.
	imageRes   int
	imageNodes int
}

func main() {
	dir := flag.String("dir", filepath.Join(os.Getenv("HOME"), "epub-corpus"), "directory of .epub files")
	font := flag.String("font", "", "font file (default: first available system CJK font)")
	width := flag.Float64("w", 900, "viewport width in DIPs")
	height := flag.Float64("h", 700, "viewport height in DIPs")
	verbose := flag.Bool("v", false, "print per-book construct detail")
	paintPages := flag.Bool("paint", false, "build and draw every page's display list through a null painter (slower, exercises the paint path)")
	timeout := flag.Duration("timeout", 5*time.Minute, "layout timeout per book")
	flag.Parse()

	fontPath, err := findFont(*font)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
	face, err := os.ReadFile(fontPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
	set, err := render.NewFontSet(render.FontFace{Family: "book", Weight: 400, Data: face, Index: 0})
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}

	files, err := filepath.Glob(filepath.Join(*dir, "*.epub"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		os.Exit(1)
	}
	texts, err := filepath.Glob(filepath.Join(*dir, "*.txt"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		os.Exit(1)
	}
	files = append(files, texts...)
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "corpus: no .epub or .txt files in %s\n", *dir)
		os.Exit(1)
	}
	sort.Strings(files)
	fmt.Printf("layx corpus ledger: %d books, font %s, viewport %.0fx%.0f\n\n", len(files), filepath.Base(fontPath), *width, *height)
	fmt.Printf("%-24s %6s %4s %6s %7s %6s %6s %5s %8s  %s\n",
		"book", "KB", "ch", "nodes", "chars", "imgs", "pages", "ms", "ms/page", "status")

	lossTotals := map[string]int{}
	var results []result
	for _, f := range files {
		r := runBook(f, set, float32(*width), float32(*height), *timeout, *paintPages)
		results = append(results, r)
		printRow(r)
		if *verbose {
			printDetail(r)
		}
		if r.err == nil && r.panicky == "" {
			if lossy, _, _ := findings(r.tag); len(lossy) > 0 {
				for _, f := range lossy {
					lossTotals[f.tag] += f.count
				}
			}
		}
	}
	printTotals(results)
	printLossTotals(lossTotals)

	if n := countFailures(results); n > 0 {
		fmt.Printf("\n%d book(s) did not lay out; see status above\n", n)
		os.Exit(1)
	}
}

func runBook(path string, set *render.FontSet, w, h float32, timeout time.Duration, paintPages bool) (r result) {
	ext := filepath.Ext(path)
	r.name = filepath.Base(path)
	if st, err := os.Stat(path); err == nil {
		r.sizeKB = int(st.Size() / 1024)
	}
	// Only markup has constructs to lose; a text book is entirely made of
	// paragraphs, so there is nothing for the ledger to compare.
	if strings.EqualFold(ext, ".epub") {
		if s, err := scanBook(path); err != nil {
			if st, statErr := os.Stat(path); statErr == nil && time.Since(st.ModTime()) < 10*time.Minute {
				r.err = fmt.Errorf("scan: %w (file modified %s ago: still downloading?)", err, time.Since(st.ModTime()).Round(time.Second))
			} else {
				r.err = fmt.Errorf("scan: %w", err)
			}
			return r
		} else {
			r.tag = s
		}
	}

	defer func() {
		if p := recover(); p != nil {
			r.panicky = fmt.Sprint(p)
		}
	}()

	start := time.Now()
	b, err := book.Open(path)
	if err != nil {
		r.err = err
		return r
	}
	r.openMS = time.Since(start).Milliseconds()
	r.title, r.lang = b.Title, b.Language
	r.chapters = len(b.Chapters)

	start = time.Now()
	doc, images, err := b.Build(fontSize)
	if err != nil {
		r.err = err
		return r
	}
	r.buildMS = time.Since(start).Milliseconds()
	r.imageRes = len(images)
	r.count(doc)

	start = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	eng, err := render.New(render.Options{})
	if err != nil {
		r.err = err
		return r
	}
	lay, err := eng.Layout(ctx, doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: w, Height: h},
		Typography: render.Typography{
			FontFamily: "book",
			FontSize:   fontSize,
			LineHeight: lineHeight,
			Color:      paint.Color{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff},
			Background: paint.Color{R: 0xfb, G: 0xf8, B: 0xef, A: 0xff},
		},
		Fonts:   set,
		Margins: paint.Insets{Top: margin, Right: margin, Bottom: margin, Left: margin},
	})
	r.layoutMS = time.Since(start).Milliseconds()
	if err != nil {
		r.err = err
		return r
	}
	r.pages = lay.PageCount()
	if paintPages {
		var np nullPainter
		start = time.Now()
		for i := 0; i < r.pages; i++ {
			if pg := lay.Page(i); pg != nil {
				pg.Draw(&np)
			}
		}
		r.paintMS = time.Since(start).Milliseconds()
		r.ops = np.ops
	}
	return r
}

// count walks the converted document: what the adapter produced, which is the
// other half of the ledger.
func (r *result) count(doc *render.Document) {
	for i := 0; i < doc.ChapterCount(); i++ {
		ch, ok := doc.Chapter(i)
		if !ok {
			continue
		}
		r.nodes += len(ch.Nodes)
		for _, n := range ch.Nodes {
			r.chars += len([]rune(n.Text))
			if n.Kind == render.NodeImage {
				r.imageNodes++
			}
		}
	}
}

// The three buckets below are the ledger's whole point. A tag the adapter
// does not map is not one kind of gap: a container such as <nav> loses
// nothing, because its text is still walked; <cite> keeps its text and loses
// only styling; <table> or <pre> loses structure or spacing that the reader
// was meant to show. Only the last kind is a difference a reader would call a
// bug, so the report keeps them apart.
var (
	// containerTags carry no content of their own: dropping the element and
	// keeping its children is the correct mapping.
	containerTags = map[string]bool{
		"nav": true, "header": true, "footer": true, "aside": true, "main": true,
		"template": true, "noscript": true, "wbr": true, "colgroup": true,
		"col": true, "tfoot": true, "dl": true, "dt": true, "dd": true,
	}
	// stylingTags keep their text but lose a visual treatment the browser
	// would apply: italics, small print, inserted/deleted marks.
	stylingTags = map[string]bool{
		"cite": true, "small": true, "ins": true, "del": true, "u": true,
		"s": true, "mark": true, "q": true, "abbr": true, "dfn": true,
		"kbd": true, "samp": true, "var": true, "time": true, "bdo": true,
		"font": true, "center": true, "tt": true, "big": true, "strike": true,
	}
	// lossyTags change what the page shows or how the text is read: table
	// structure, preformatted spacing, footnote markers, ruby annotations,
	// images inside SVG, and so on.
	lossyTags = map[string]bool{
		"table": true, "tr": true, "td": true, "th": true, "thead": true,
		"tbody": true, "caption": true, "pre": true, "ruby": true, "rt": true,
		"rp": true, "sup": true, "sub": true, "figure": true, "figcaption": true,
		"iframe": true, "audio": true, "video": true, "canvas": true,
		"math": true, "object": true, "embed": true,
	}
)

type finding struct {
	tag   string
	count int
	why   string
}

// findings turns the markup census into three lists: real losses, styling that
// is gone, and containers that were walked and dropped on purpose. Markers and
// links are called out separately because the tags exist in the IR but the
// adapter does not carry what makes them visible.
func findings(s scan) (lossy, styling []finding, containers []string) {
	for tag, n := range s.tags {
		switch {
		case lossyTags[tag]:
			lossy = append(lossy, finding{tag, n, lossyWhy[tag]})
		case stylingTags[tag]:
			styling = append(styling, finding{tag, n, ""})
		case containerTags[tag]:
			containers = append(containers, fmt.Sprintf("%s=%d", tag, n))
		}
	}
	if n := s.tags["li"]; n > 0 {
		lossy = append(lossy, finding{"li", n, "list markers are not drawn"})
	}
	if n := s.tags["a"]; n > 0 {
		lossy = append(lossy, finding{"a", n, "links are not navigable"})
	}
	sortFindings(lossy)
	sortFindings(styling)
	sort.Strings(containers)
	return lossy, styling, containers
}

var lossyWhy = map[string]string{
	"table": "table structure is flattened", "tr": "", "td": "", "th": "",
	"thead": "", "tbody": "", "caption": "",
	"pre":  "preformatted spacing is collapsed",
	"ruby": "ruby annotations become inline text", "rt": "", "rp": "",
	"sup": "superscript becomes a plain character", "sub": "subscript becomes a plain character",
	"figure": "", "figcaption": "",
	"iframe": "embedded content is skipped", "audio": "", "video": "",
	"canvas": "", "math": "math is skipped", "object": "", "embed": "",
}

func sortFindings(fs []finding) {
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].count != fs[j].count {
			return fs[i].count > fs[j].count
		}
		return fs[i].tag < fs[j].tag
	})
}

// nullPainter implements paint.Painter and throws the drawing away. Using the
// real interface means a bad rectangle, a missing glyph run or a nil image
// fails here instead of on a user's screen.
type nullPainter struct{ ops int }

func (p *nullPainter) Save()                                                   { p.ops++ }
func (p *nullPainter) Restore()                                                { p.ops++ }
func (p *nullPainter) ClipRect(paint.Rect)                                     { p.ops++ }
func (p *nullPainter) Transform(paint.Affine)                                  { p.ops++ }
func (p *nullPainter) FillRect(paint.Rect, paint.Color)                        { p.ops++ }
func (p *nullPainter) StrokeRect(paint.Rect, paint.Color, float32)             { p.ops++ }
func (p *nullPainter) FillRoundRect(paint.Rect, float32, paint.Color)          { p.ops++ }
func (p *nullPainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) { p.ops++ }
func (p *nullPainter) DrawGlyphs(paint.GlyphRun)                               { p.ops++ }

func printRow(r result) {
	status := r.err
	line := fmt.Sprintf("%-24s %6d %4d %6d %7d %6d %6d %5d %8s  ",
		truncate(r.name, 24), r.sizeKB, r.chapters, r.nodes, r.chars, r.images, r.pages, r.layoutMS, msPerPage(r))
	switch {
	case r.panicky != "":
		line += "PANIC: " + truncate(r.panicky, 60)
	case status != nil:
		line += "ERROR: " + truncate(status.Error(), 70)
	default:
		line += "ok"
	}
	fmt.Println(line)
}

func msPerPage(r result) string {
	if r.pages == 0 || r.layoutMS == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(r.layoutMS)/float64(r.pages))
}

func printDetail(r result) {
	if r.err != nil || r.panicky != "" {
		return
	}
	c := r.tag
	var parts []string
	addGroup := func(label string, tags []string) {
		var got []string
		for _, t := range tags {
			if n := c.tags[t]; n > 0 {
				got = append(got, fmt.Sprintf("%s=%d", t, n))
			}
		}
		if len(got) > 0 {
			parts = append(parts, label+"["+strings.Join(got, " ")+"]")
		}
	}
	addGroup("text", textTags)
	addGroup("media", mediaTags)
	addGroup("table", tableTags)
	addGroup("inline", inlineTags)
	if len(parts) > 0 {
		fmt.Println("    markup:", strings.Join(parts, " "))
	}
	if len(c.css) > 0 {
		keys := make([]string, 0, len(c.css))
		for k := range c.css {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return c.css[keys[i]] > c.css[keys[j]] })
		if len(keys) > 12 {
			keys = keys[:12]
		}
		var got []string
		for _, k := range keys {
			got = append(got, fmt.Sprintf("%s=%d", k, c.css[k]))
		}
		fmt.Println("    css:   ", strings.Join(got, " "))
	}
	lossy, styling, containers := findings(c)
	if len(lossy) > 0 {
		var got []string
		for _, f := range lossy {
			item := fmt.Sprintf("%s=%d", f.tag, f.count)
			if f.why != "" {
				item += " (" + f.why + ")"
			}
			got = append(got, item)
		}
		fmt.Println("    lost:  ", strings.Join(got, ", "))
	}
	if len(styling) > 0 {
		var got []string
		for _, f := range styling {
			got = append(got, fmt.Sprintf("%s=%d", f.tag, f.count))
		}
		fmt.Println("    unstyled:", strings.Join(got, " "), "(text kept, styling dropped)")
	}
	if len(containers) > 0 {
		fmt.Println("    walked:", strings.Join(containers, " "), "(containers, no content lost)")
	}
	if r.imageNodes != 0 || r.imageRes != 0 {
		fmt.Printf("    images: %d placed, %d resources\n", r.imageNodes, r.imageRes)
	}
	if r.ops > 0 {
		fmt.Printf("    painted: %d ops in %d ms\n", r.ops, r.paintMS)
	}
	if r.title != "" {
		fmt.Printf("    title:  %s (%s)\n", truncate(r.title, 60), r.lang)
	}
}

func printTotals(rs []result) {
	var pages, chars, nodes, books int
	var ms int64
	for _, r := range rs {
		if r.err != nil || r.panicky != "" {
			continue
		}
		books++
		pages += r.pages
		chars += r.chars
		nodes += r.nodes
		ms += r.layoutMS
	}
	if books == 0 {
		return
	}
	fmt.Printf("\n%d/%d books, %d pages, %d chars, %d nodes, %.1fs layout total, %.1f ms/page\n",
		books, len(rs), pages, chars, nodes, float64(ms)/1000, float64(ms)/float64(pages))
}

// printLossTotals is the corpus-wide denominator: what the books actually use,
// summed. A construct that appears zero times here cannot justify a core
// feature on its own, whatever the browser does with it.
func printLossTotals(totals map[string]int) {
	if len(totals) == 0 {
		return
	}
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return totals[keys[i]] > totals[keys[j]] })
	var got []string
	for _, k := range keys {
		got = append(got, fmt.Sprintf("%s=%d", k, totals[k]))
	}
	fmt.Println("constructs the reader does not represent, corpus-wide:")
	fmt.Println(" ", strings.Join(got, " "))
}

func countFailures(rs []result) int {
	n := 0
	for _, r := range rs {
		if r.err != nil || r.panicky != "" {
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func findFont(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if p := strings.TrimSpace(os.Getenv("LAYX_FONT")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	for _, p := range []string{
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no font found; set LAYX_FONT or pass -font")
}
