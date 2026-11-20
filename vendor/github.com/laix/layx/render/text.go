package render

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"github.com/laix/layx/render/paint"
	"golang.org/x/image/math/fixed"
)

type paraSeg struct {
	node  NodeID
	style Style
	// text is the segment's source string. Paragraph runes are not retained:
	// they are materialized on demand from these strings (see paraText), which
	// keeps a whole book's text out of the layout.
	text string
	// start and end are rune offsets into the materialized paragraph text.
	start, end int32
	lang       string
	link       string
}

type paragraph struct {
	segs []paraSeg
	// runeLen is the length of the materialized text, i.e. the last segment's
	// end. It is stored so callers do not have to walk the segments.
	runeLen int32
	// style is the block style that supplies alignment and indentation.
	style Style
	// chapter is the ID of the chapter the paragraph belongs to. It is only
	// used to attach diagnostics to their source; it may be empty when the
	// paragraph is not part of a chapter.
	chapter string
	// width is the content width the paragraph was broken at, i.e. the
	// available width of every line except the indented first one.
	width float32
	// hyphPoints are original rune indexes where a hyphen may be appended
	// when a line breaks there. They are populated only when the block enables
	// hyphenation; nil means the line breaker may never add a hyphen.
	hyphPoints []int32
	// hyphAdv stores the measured advance of each synthetic hyphen (including
	// letter spacing) so drawing matches the width used for line breaking.
	hyphAdv map[int32]float32
}

func (p *paragraph) segAt(off int) *paraSeg {
	i := sort.Search(len(p.segs), func(i int) bool { return int(p.segs[i].end) > off })
	if i < len(p.segs) && int(p.segs[i].start) <= off {
		return &p.segs[i]
	}
	if len(p.segs) == 0 {
		return nil
	}
	if i >= len(p.segs) {
		return &p.segs[len(p.segs)-1]
	}
	return &p.segs[i]
}

// textWorker is one goroutine's shaping scratch space. It is leased from a pool
// for the duration of a phase and must not be shared by goroutines.
type textWorker struct {
	shaper      shaping.HarfbuzzShaper
	wrapper     shaping.LineWrapper
	segmenter   shaping.Segmenter
	fs          *FontSet
	typo        Typography
	defaultLang language.Language
	hyphenator  Hyphenator
	faces       []*font.Face
	refs        []*paint.Font
	orders      map[fontKey][]int
	// shapes counts Shaper.Shape calls for layout benchmarks and tests. It is
	// per Engine; a nil counter disables counting.
	shapes *atomic.Int64
	// issues receives diagnostics raised while breaking text. It is per layout
	// and only set on leased workers during the break phase; nil disables
	// reporting. issueSink is concurrency-safe, so every worker may share it.
	issues *issueSink
	// paraBuf is this worker's scratch buffer for materializing paragraph
	// runes. It is owned by the worker, so no two goroutines share it.
	paraBuf []rune
	// bufPara and bufText memoize the buffer's owner: a page is built line by
	// line and most of its lines come from one paragraph, so materializing per
	// line would copy the same text dozens of times.
	bufPara *paragraph
	bufText []rune
	// Segmentation scratch, reused per input run (see segment.go).
	scriptRuns []shaping.Input
	faceRuns   []shaping.Input
	bidiRuns   []shaping.Input
	delimStack []delimEntry
}

// paraText materializes a paragraph's runes into the worker's scratch buffer.
//
// Paragraphs do not retain their rune copy: a 5M-rune book would keep 20 MB
// alive for text the Document already owns. The returned slice is only valid
// until the next paraText call on the same worker, so callers either use it
// immediately or copy what they need.
func (w *textWorker) paraText(p *paragraph) []rune {
	if w.bufPara == p && w.bufText != nil {
		return w.bufText
	}
	// Grow the buffer to the paragraph length first: append would otherwise
	// reallocate on every paragraph that is longer than the last one.
	if n := int(p.runeLen); cap(w.paraBuf) < n {
		w.paraBuf = make([]rune, 0, n)
	}
	buf := w.paraBuf[:0]
	for si := range p.segs {
		// A range loop over a string does not allocate, unlike []rune(s).
		for _, r := range p.segs[si].text {
			buf = append(buf, r)
		}
	}
	w.paraBuf = buf
	w.bufPara, w.bufText = p, buf
	return buf
}

type fontKey struct {
	family string
	weight int
	slant  Slant
}

func newTextWorker(fs *FontSet, typo Typography, hyphenator Hyphenator) *textWorker {
	if typo.FontSize <= 0 || typo.LineHeight <= 0 {
		typo = typo.withDefaults()
	}
	w := &textWorker{
		fs:         fs,
		typo:       typo,
		hyphenator: hyphenator,
		faces:      make([]*font.Face, len(fs.entries)),
		refs:       make([]*paint.Font, len(fs.entries)),
		orders:     make(map[fontKey][]int),
	}
	for i := range fs.entries {
		w.faces[i] = font.NewFace(fs.entries[i].font)
		w.refs[i] = fs.entries[i].ref
	}
	w.shaper.SetFontCacheSize(8)
	return w
}

// prepare points a pooled worker at one layout variant. Workers outlive the
// Layout that leased them, so everything that differs between variants — the
// typography, the dictionary and the document language — is set on lease, not
// at construction. The font set is fixed: each pool belongs to one FontSet.
func (w *textWorker) prepare(typo Typography, hyphenator Hyphenator, lang language.Language) {
	w.typo = typo
	w.hyphenator = hyphenator
	w.defaultLang = lang
	// The paragraph memo is deliberately kept: it is keyed by the paragraph
	// pointer, so a stale entry can never be mistaken for another paragraph.
	// Keeping it means a worker that paints several lines of the same paragraph
	// materializes that text once instead of once per line, even though it was
	// leased again between lines.
}

func (w *textWorker) order(st Style) []int {
	key := fontKey{family: st.FontFamily, weight: st.FontWeight, slant: st.FontSlant}
	if o, ok := w.orders[key]; ok {
		return o
	}
	o := w.fs.order(st.FontFamily, st.FontWeight, st.FontSlant)
	w.orders[key] = o
	return o
}

type workerFontmap struct {
	faces []*font.Face
	order []int
}

func (m workerFontmap) ResolveFace(r rune) *font.Face {
	for _, i := range m.order {
		if _, ok := m.faces[i].NominalGlyph(r); ok {
			return m.faces[i]
		}
	}
	if len(m.order) != 0 {
		return m.faces[m.order[0]]
	}
	return m.faces[0]
}

// runStyle is the compact subset of Style that shaping and drawing need per
// run. Keeping a full Style here made every shapeRange slice element 48 bytes
// larger once box/decoration fields were added, which showed up in the 5M-rune
// layout allocation benchmark without changing any output.
type runStyle struct {
	letterSpacing   float32
	wordSpacing     float32
	fontWeight      int
	color           paint.Color
	decoration      TextDecoration
	decorationColor paint.Color
	// link marks link text: a link whose style names no colour takes the
	// layout's Typography.LinkColor rather than its ink.
	link bool
}

func compactRunStyle(st Style, link bool) runStyle {
	return runStyle{
		letterSpacing:   st.LetterSpacing,
		wordSpacing:     st.WordSpacing,
		fontWeight:      st.FontWeight,
		color:           st.Color,
		decoration:      st.TextDecoration,
		decorationColor: st.TextDecorationColor,
		link:            link,
	}
}

type shapedRun struct {
	out   shaping.Output
	style runStyle
	ref   *paint.Font
}

// shapeRange shapes paragraph runes [start,end). It deliberately does not
// retain the shaped output beyond the line wrapping / drawing operation: the
// layout itself stores only line boxes and rune ranges.
//
// Invariant: the returned runs cover every rune in [start,end), so a caller
// that hands the whole range to a line wrapper can always map a rune offset to a
// glyph cluster. splitInput is supposed to guarantee that on its own; fillShapeGaps
// is the defensive net that makes a short list impossible (audit §2.1). In
// particular wrapParagraph relies on the runs covering [0,len(wrapText)).
func (w *textWorker) shapeRange(p *paragraph, text []rune, start, end int, defaultLang language.Language) []shapedRun {
	start = clampInt(start, 0, len(text))
	end = clampInt(end, start, len(text))
	if start == end {
		return nil
	}
	out := make([]shapedRun, 0, 16)
	for si := range p.segs {
		seg := &p.segs[si]
		s := maxInt(start, int(seg.start))
		e := minInt(end, int(seg.end))
		if s >= e {
			continue
		}
		lang := defaultLang
		if seg.lang != "" {
			lang = language.NewLanguage(seg.lang)
		}
		size := resolveFontSize(seg.style, w.typo)
		in := shaping.Input{
			Text:      text,
			RunStart:  s,
			RunEnd:    e,
			Direction: di.DirectionLTR,
			Size:      fixed.Int26_6(float64(size) * 64),
			Language:  lang,
		}
		fm := workerFontmap{faces: w.faces, order: w.order(seg.style)}
		for _, split := range w.splitInput(in, fm) {
			// The wrapper's fit decision subtracts the last glyph's advance
			// when its Width is zero (it assumes a whitespace glyph). With
			// ShapeNoExtents every glyph has Width zero, so every line is
			// under-measured by one glyph and justified lines overflow the
			// right margin. Extents are therefore required here; drawing still
			// uses the outlines directly.
			if w.shapes != nil {
				w.shapes.Add(1)
			}
			shaped := w.shaper.Shape(split)
			out = append(out, shapedRun{out: shaped, style: compactRunStyle(seg.style, seg.link != ""), ref: w.refs[faceIndex(w.faces, shaped.Face)]})
		}
	}
	out = w.fillShapeGaps(p, text, start, end, defaultLang, out)
	for i := range out {
		ls := fixed.Int26_6(out[i].style.letterSpacing * 64)
		if ls != 0 {
			out[i].out.AddLetterSpacing(ls, i == 0, i == len(out)-1)
		}
	}
	w.addWordSpacing(text, out)
	if w.typo.Rules.Autospacing {
		w.addAutospacing(text, out)
	}
	if w.typo.Rules.PunctuationCompression {
		w.compressPunctuation(text, out, start, end)
	}
	return out
}

// fillShapeGaps enforces shapeRange's coverage invariant. The common case is a
// contiguous, ordered list and costs one pass with no allocation; if the shaped
// runs leave any rune of [start,end) without a glyph cluster, that hole is
// filled with a placeholder run shaped in the fallback face. A placeholder is
// never dropped: a rune with .notdef glyphs is still a rune the line wrapper can
// cut at, which is what keeps go-text's wrapping from panicking on a short list.
func (w *textWorker) fillShapeGaps(p *paragraph, text []rune, start, end int, defaultLang language.Language, runs []shapedRun) []shapedRun {
	span := end - start
	if span <= 0 {
		return runs
	}
	// Cheap coverage test: the rune counts add up to the span and every run
	// stays inside it. This is the shape every normal paragraph has, in any run
	// order (bidi runs may be stored in visual order).
	total := 0
	inBounds := true
	for i := range runs {
		out := &runs[i].out
		if out.Runes.Count <= 0 || out.Runes.Offset < start || out.Runes.Offset+out.Runes.Count > end {
			inBounds = false
			break
		}
		total += out.Runes.Count
	}
	if inBounds && total == span {
		return runs
	}
	// Slow path: find the holes exactly and append a placeholder run for each.
	covered := make([]bool, span)
	for i := range runs {
		lo := maxInt(runs[i].out.Runes.Offset, start)
		hi := minInt(runs[i].out.Runes.Offset+runs[i].out.Runes.Count, end)
		for r := lo; r < hi; r++ {
			covered[r-start] = true
		}
	}
	for i := 0; i < span; {
		if covered[i] {
			i++
			continue
		}
		j := i
		for j < span && !covered[j] {
			j++
		}
		runs = append(runs, w.shapeGapRun(p, text, start+i, start+j, defaultLang))
		i = j
	}
	return runs
}

// shapeGapRun shapes [lo,hi) with the fallback face for fillShapeGaps. Its
// output covers exactly [lo,hi) runes even when the face has no glyph for them:
// the glyphs are .notdef, but the cluster indexes are what the wrapper needs.
func (w *textWorker) shapeGapRun(p *paragraph, text []rune, lo, hi int, defaultLang language.Language) shapedRun {
	st := p.styleAt(lo)
	size := resolveFontSize(st, w.typo)
	in := shaping.Input{
		Text:      text,
		RunStart:  lo,
		RunEnd:    hi,
		Direction: di.DirectionLTR,
		Face:      w.faces[0],
		Size:      fixed.Int26_6(float64(size) * 64),
		Language:  defaultLang,
	}
	if w.shapes != nil {
		w.shapes.Add(1)
	}
	out := w.shaper.Shape(in)
	link := false
	if seg := p.segAt(lo); seg != nil {
		link = seg.link != ""
	}
	return shapedRun{out: out, style: compactRunStyle(st, link), ref: w.refs[0]}
}

// issueSkipRune reports whether a rune must not be reported as a missing
// glyph. Controls, bidirectional formatting characters, default ignorables and
// spaces legitimately shape to .notdef or have no ink of their own; reporting
// them would drown the real tofu in noise.
func issueSkipRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
		return true
	}
	if harfbuzz.IsDefaultIgnorable(r) {
		return true
	}
	// Surrogates are not valid text runes; go-text cannot draw them by design.
	return r >= 0xd800 && r <= 0xdfff
}

// reportMissingFonts reports one IssueMissingFont for every paragraph segment
// whose resolved Style.FontFamily is set but has no face in the font set. It
// runs on the break path only; drawing reuses the shaped result.
func (w *textWorker) reportMissingFonts(p *paragraph) {
	if w.issues == nil {
		return
	}
	for si := range p.segs {
		seg := &p.segs[si]
		family := strings.TrimSpace(seg.style.FontFamily)
		if family == "" || w.fs.HasFamily(family) {
			continue
		}
		w.issues.add(IssueMissingFont, p.chapter, seg.node, "font family %q is not in the font set", family)
	}
}

// missingGlyphSet collects the unique code points one node could not draw.
type missingGlyphSet struct {
	order []rune
	seen  map[rune]struct{}
}

// add records one missing code point, keeping first-seen order for a stable
// Detail string.
func (s *missingGlyphSet) add(r rune) {
	if s.seen == nil {
		s.seen = make(map[rune]struct{}, 8)
	}
	if _, ok := s.seen[r]; ok {
		return
	}
	s.seen[r] = struct{}{}
	s.order = append(s.order, r)
}

// missingGlyphDetail formats at most maxMissingGlyphs code points plus how many
// further unique code points were dropped, so a paragraph full of unsupported
// text still produces a readable, bounded diagnostic.
func missingGlyphDetail(runes []rune) string {
	const maxMissingGlyphs = 8
	var b strings.Builder
	b.WriteString("missing glyphs for ")
	n := len(runes)
	if n > maxMissingGlyphs {
		n = maxMissingGlyphs
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "U+%04X", runes[i])
	}
	if extra := len(runes) - n; extra > 0 {
		fmt.Fprintf(&b, " (+%d more code points)", extra)
	}
	return b.String()
}

// reportMissingGlyphs scans the shaped runs of a paragraph for .notdef glyphs
// (GlyphID 0) and reports one IssueMissingGlyphs per paragraph segment node,
// naming the code points no configured face could draw. It is called by
// wrapParagraph, which shapes a whole paragraph once per layout; the per-line
// drawing path shapes lines again and must not report duplicates.
//
// wrapText is the text that was shaped and origText the paragraph's own runes;
// they differ when hyphenation inserted synthetic hyphens (origIndex maps a
// wrap index back to the original rune, and wrapHyph marks the inserted ones).
func (w *textWorker) reportMissingGlyphs(p *paragraph, wrapText, origText []rune, origIndex []int32, wrapHyph map[int32]struct{}, runs []shapedRun) {
	if w.issues == nil {
		return
	}
	// The maps are created on the first .notdef only: a clean paragraph, which
	// is the overwhelmingly common case, pays one glyph scan and no allocation.
	var sets map[NodeID]*missingGlyphSet
	var order []NodeID
	add := func(node NodeID, r rune) {
		if sets == nil {
			sets = make(map[NodeID]*missingGlyphSet, 1)
		}
		s := sets[node]
		if s == nil {
			s = &missingGlyphSet{}
			sets[node] = s
			order = append(order, node)
		}
		s.add(r)
	}
	for ri := range runs {
		out := &runs[ri].out
		for gi := range out.Glyphs {
			g := &out.Glyphs[gi]
			if g.GlyphID != 0 {
				continue
			}
			// One glyph cluster may span several runes; every rune in it that
			// is not a control/ignorable is genuinely missing.
			n := g.RunesCount()
			if n < 1 {
				n = 1
			}
			for k := 0; k < n; k++ {
				wi := g.TextIndex() + k
				if wi < 0 || wi >= len(wrapText) {
					continue
				}
				orig := wi
				if origIndex != nil {
					if _, synthetic := wrapHyph[int32(wi)]; synthetic {
						continue
					}
					// origIndex[wi] is one past the original rune at wi; the
					// last entry maps the end of the text.
					orig = int(origIndex[wi]) - 1
					if orig < 0 || orig >= len(origText) {
						continue
					}
				}
				r := origText[orig]
				if issueSkipRune(r) {
					continue
				}
				seg := p.segAt(orig)
				if seg == nil {
					continue
				}
				add(seg.node, r)
			}
		}
	}
	for _, node := range order {
		w.issues.add(IssueMissingGlyphs, p.chapter, node, "%s", missingGlyphDetail(sets[node].order))
	}
}

// shapeHyphen shapes a synthetic hyphen for a line that ended at an allowed
// hyphenation point. The hyphen is not part of paragraph.text, so it is shaped
// on demand and only on the lines that actually use it.
func (w *textWorker) shapeHyphen(st Style, langTag string) (*paint.Font, float32, shaping.Glyph, bool) {
	size := resolveFontSize(st, w.typo)
	lang := w.defaultLang
	if langTag != "" {
		lang = language.NewLanguage(langTag)
	}
	fm := workerFontmap{faces: w.faces, order: w.order(st)}
	in := shaping.Input{
		Text:      []rune{'-'},
		RunStart:  0,
		RunEnd:    1,
		Direction: di.DirectionLTR,
		Face:      fm.ResolveFace('-'),
		Size:      fixed.Int26_6(float64(size) * 64),
		Language:  lang,
	}
	if w.shapes != nil {
		w.shapes.Add(1)
	}
	out := w.shaper.Shape(in)
	if len(out.Glyphs) == 0 {
		return nil, size, shaping.Glyph{}, false
	}
	return w.refs[faceIndex(w.faces, out.Face)], float32(out.Size) / 64, out.Glyphs[0], true
}

// isWordSpace reports whether r is a word-separating space for word-spacing.
// Only the two ASCII/Latin spaces are affected; the ideographic space U+3000
// is a full-width glyph, not a word gap.
func isWordSpace(r rune) bool {
	return r == ' ' || r == '\u00a0'
}

// addWordSpacing widens word-separating spaces by Style.WordSpacing. It runs
// before wrapping, so the extra advance participates in line breaking, and
// the shaped output is reused by drawing.
func (w *textWorker) addWordSpacing(text []rune, runs []shapedRun) {
	for ri := range runs {
		run := &runs[ri]
		if run.style.wordSpacing == 0 {
			continue
		}
		delta := fixed.Int26_6(float64(run.style.wordSpacing) * 64)
		for gi := range run.out.Glyphs {
			g := &run.out.Glyphs[gi]
			idx := g.TextIndex()
			if idx < 0 || idx >= len(text) || !isWordSpace(text[idx]) {
				continue
			}
			g.Advance += delta
			run.out.Advance += delta
		}
	}
}

// addAutospacing inserts a 1/8 em gap where an ideographic character meets a
// Latin letter or digit, mirroring CSS text-autospace. The gap is added to the
// preceding glyph's advance so line wrapping sees the final width. Applied
// before wrapping, so it also affects where lines break. It only runs when
// TextRules.Autospacing is set; the gap is not a CSS initial value.
func (w *textWorker) addAutospacing(text []rune, runs []shapedRun) {
	if len(runs) == 0 {
		return
	}
	var prevRun *shapedRun
	var prev *shaping.Glyph
	for ri := range runs {
		run := &runs[ri]
		delta := fixed.Int26_6(run.out.Size) / 8
		for gi := range run.out.Glyphs {
			g := &run.out.Glyphs[gi]
			if prev != nil {
				a, b := prev.TextIndex(), g.TextIndex()
				if b == a+1 && needsAutospace(text, a, b) {
					prev.Advance += delta
					prevRun.out.Advance += delta
				}
			}
			prev, prevRun = g, run
		}
	}
}

// needsAutospace reports whether the rune boundary a|b separates an ideographic
// character from a Latin letter or digit.
func needsAutospace(text []rune, a, b int) bool {
	if a < 0 || b >= len(text) {
		return false
	}
	left, right := text[a], text[b]
	if isJustifySpace(left) || isJustifySpace(right) {
		return false
	}
	return (isIdeographic(left) && isLatinish(right)) || (isLatinish(left) && isIdeographic(right))
}

// isIdeographic reports whether r is a CJK/Hangul ideograph or kana.
func isIdeographic(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Bopomofo)
}

// isLatinish reports whether r is a non-ideographic letter or digit, i.e. the
// kind of character that gets an autospace gap next to CJK text.
func isLatinish(r rune) bool {
	if isIdeographic(r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

// isCJKPunctuation reports whether r is CJK punctuation or a fullwidth form.
// Gaps next to punctuation stretch less than gaps between ideographs.
func isCJKPunctuation(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303f:
		return true
	case r >= 0xfe10 && r <= 0xfe19, r >= 0xfe30 && r <= 0xfe6f:
		return true
	case r >= 0xff00 && r <= 0xff65:
		return true
	}
	switch r {
	case '\u2014', '\u2026', '\u2018', '\u2019', '\u201c', '\u201d':
		return true
	}
	return false
}

func faceIndex(faces []*font.Face, target *font.Face) int {
	for i, f := range faces {
		if f == target {
			return i
		}
	}
	return 0
}

type lineBox struct {
	para  *paragraph
	start int32
	end   int32
	x     float32
	width float32
	avail float32
	last  bool
	align TextAlign

	// Vertical metrics captured at break time so a layout can be re-stacked
	// with a different Typography.LineHeight without re-shaping. The line
	// height and baseline themselves are derived by lineMetrics.
	ascent  float32
	descent float32
	// implicitSize is the largest font size on the line that inherits the
	// typographic line height. explicitHeight is the largest font-size x
	// line-height product from runs that override it. The effective line
	// height is max(explicitHeight, implicitSize*LineHeight).
	implicitSize   float32
	explicitHeight float32
}

// wrapParagraph breaks p into lines of at most width pixels. It returns line
// boxes positioned relative to the paragraph origin (x/y start at zero).
func (w *textWorker) wrapParagraph(
	ctx context.Context,
	p *paragraph,
	width float32,
	typo Typography,
	blockStyle Style,
	defaultLang language.Language,
) ([]lineBox, error) {
	// origText keeps the paragraph's own runes: hyphenation below replaces
	// wrapText with a copy that also contains synthetic hyphens.
	origText := w.paraText(p)
	wrapPara := p
	wrapText := origText
	p.width = width
	var (
		origIndex []int32
		wrapHyph  map[int32]struct{}
	)
	if blockStyle.Hyphenate {
		if wp, wt, orig, hyph := w.hyphenatedParagraph(p, wrapText); wp != nil {
			wrapPara = wp
			wrapText = wt
			origIndex = orig
			wrapHyph = hyph
		}
	}
	// Diagnostics belong to the break path: this is the one place that shapes
	// the whole paragraph per layout, so a missing font or a .notdef glyph is
	// reported once instead of once per drawn line.
	w.reportMissingFonts(p)
	runs := w.shapeRange(wrapPara, wrapText, 0, len(wrapText), defaultLang)
	w.reportMissingGlyphs(p, wrapText, origText, origIndex, wrapHyph, runs)
	if len(runs) == 0 {
		return nil, nil
	}
	// Synthetic hyphens are break opportunities, not text: measure them as
	// zero-width so unused points inside a word cannot widen the line. When a
	// line actually ends at one, its real advance is added back below, and the
	// wrap limit is reduced by the widest hyphen so the result still fits.
	var hyphAdv map[int32]float32
	maxHyph := float32(0)
	if len(wrapHyph) != 0 {
		hyphAdv = make(map[int32]float32, len(wrapHyph))
		for ri := range runs {
			out := &runs[ri].out
			for gi := range out.Glyphs {
				g := &out.Glyphs[gi]
				idx := int32(g.TextIndex())
				if _, ok := wrapHyph[idx]; !ok {
					continue
				}
				adv := float32(g.Advance) / 64
				out.Advance -= g.Advance
				g.Advance = 0
				hyphAdv[idx] = adv
				if adv > maxHyph {
					maxHyph = adv
				}
			}
		}
	}
	if len(hyphAdv) != 0 {
		p.hyphAdv = hyphAdv
	}
	outs := make([]shaping.Output, len(runs))
	for i := range runs {
		outs[i] = runs[i].out
	}
	config := shaping.WrapConfig{
		Direction:   di.DirectionLTR,
		BreakPolicy: shaping.WhenNecessary,
	}
	align := blockStyle.TextAlign
	indent := blockStyle.FirstLineIndent
	if indent < 0 {
		indent = 0
	}

	// Hanging punctuation needs each rune's advance at line boundaries. It is
	// off by default and interacts with synthetic hyphens and re-mapped rune
	// indices, so those two features turn it off for the paragraph.
	hang := typo.Rules.HangingPunctuation && origIndex == nil && len(wrapHyph) == 0
	var runeAdv []float32
	if hang {
		runeAdv = make([]float32, len(wrapText))
		for oi := range outs {
			for _, g := range outs[oi].Glyphs {
				idx := g.TextIndex()
				if idx >= 0 && idx < len(runeAdv) {
					runeAdv[idx] += float32(g.Advance) / 64
				}
			}
		}
	}

	// wrapOnce lays out the paragraph, measuring synthetic hyphens as zero
	// width. A break at a hyphen adds its real advance afterwards. If that
	// makes any line exceed its width, a second pass reserves the widest
	// hyphen for every line, which guarantees used hyphens fit.
	wrapOnce := func(pad float32) ([]lineBox, bool, error) {
		w.wrapper.Prepare(config, wrapText, shaping.NewSliceIterator(outs))
		lines := make([]lineBox, 0, len(wrapText)/24+1)
		lineStart := 0
		// placed is the first rune not yet put on a line. Hanging punctuation
		// moves a rune or two past the wrapper's break, so following wrapper
		// lines may start before it; the box then starts at placed and its
		// width loses the already-placed advance.
		placed := 0
		first := true
		overflow := false
		for {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			maxW := width
			if first {
				maxW = width - indent
			}
			if maxW <= 0 {
				return nil, false, docErrorf(ErrInvalidOptions, "first-line indent leaves no line width")
			}
			wrapW := maxW
			if pad > 0 && wrapW > pad {
				wrapW -= pad
			}
			wrapped, done := w.wrapper.WrapNextLineF(fixed.Int26_6(float64(wrapW) * 64))
			if wrapped.Line == nil {
				break
			}
			lineEnd := wrapped.NextLine
			if lineEnd < lineStart {
				lineEnd = lineStart
			}
			if lineEnd == lineStart && !done {
				// Defensive: the wrapper should always make progress. Rather
				// than spin forever on corrupt input, stop the paragraph here.
				break
			}
			dispStart := lineStart
			if placed > dispStart {
				dispStart = placed
			}
			if dispStart >= lineEnd {
				// This wrapper line was placed entirely on the previous one;
				// nothing is left to put here.
				if done {
					if len(lines) > 0 {
						lines[len(lines)-1].last = true
					}
					break
				}
				lineStart = lineEnd
				first = false
				continue
			}

			var (
				lineWidth    float32
				ascent       float32
				descent      float32
				implicitSize float32
				explicitHMax float32
			)
			for _, out := range wrapped.Line {
				lineWidth += float32(out.Advance) / 64
				if a := float32(out.LineBounds.Ascent) / 64; a > ascent {
					ascent = a
				}
				if d := float32(-out.LineBounds.Descent) / 64; d > descent {
					descent = d
				}
				st := wrapPara.styleAt(out.Runes.Offset)
				size := resolveFontSize(st, typo)
				if st.LineHeight > 0 {
					if h := size * st.LineHeight; h > explicitHMax {
						explicitHMax = h
					}
				} else if size > implicitSize {
					implicitSize = size
				}
			}
			// The prefix [lineStart, dispStart) already belongs to the
			// previous line.
			for i := lineStart; i < dispStart; i++ {
				lineWidth -= runeAdv[i]
			}
			if len(wrapHyph) != 0 {
				if adv, ok := hyphAdv[int32(lineEnd-1)]; ok {
					if lineWidth+adv > maxW+0.01 {
						overflow = true
					}
					lineWidth += adv
				}
			}
			// Try to hang a closing mark that the wrapper pushed to the next
			// line. allow-end lets the mark overhang by half its own advance;
			// when that is enough, the mark (and the character it was glued
			// to) stays on this line.
			startRune := dispStart
			if hang && !done {
				if k := hangingPullCount(wrapText, lineEnd); k > 0 {
					adv := float32(0)
					for i := 0; i < k; i++ {
						adv += runeAdv[lineEnd+i]
					}
					over := lineWidth + adv - maxW
					if over <= 0 || over <= 0.5*runeAdv[lineEnd+k-1]+0.01 {
						lineWidth += adv
						lineEnd += k
						if lineEnd >= len(wrapText) {
							done = true
						}
					}
				}
			}
			placed = lineEnd

			x := float32(0)
			switch align {
			case AlignRight:
				x = maxW - lineWidth
			case AlignCenter:
				x = (maxW - lineWidth) / 2
			}
			if first {
				x += indent
			}
			start, endRune := int32(startRune), int32(lineEnd)
			if origIndex != nil {
				start = origIndex[startRune]
				endRune = origIndex[lineEnd]
			}
			lines = append(lines, lineBox{
				para:           p,
				start:          start,
				end:            endRune,
				x:              x,
				width:          lineWidth,
				avail:          maxW,
				last:           done,
				align:          align,
				ascent:         ascent,
				descent:        descent,
				implicitSize:   implicitSize,
				explicitHeight: explicitHMax,
			})
			lineStart = wrapped.NextLine
			first = false
			if done {
				break
			}
		}
		return lines, overflow, nil
	}

	lines, overflow, err := wrapOnce(0)
	if err != nil {
		return nil, err
	}
	if overflow && maxHyph > 0 {
		retry, _, err := wrapOnce(maxHyph)
		if err != nil {
			return nil, err
		}
		return retry, nil
	}
	return lines, nil
}

func (p *paragraph) styleAt(off int) Style {
	if seg := p.segAt(off); seg != nil {
		return seg.style
	}
	return p.style
}

func resolveFontSize(st Style, typo Typography) float32 {
	if st.FontSize > 0 {
		return st.FontSize
	}
	if typo.FontSize > 0 {
		return typo.FontSize
	}
	return 18
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampF16 narrows a metric in 1/64 px to the range lineBreak stores, which
// caps a line bound at 512 px. Line bounds are within a few hundred pixels in
// practice; clamping keeps a pathological font from wrapping around.
func clampF16(v float32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// clampI16 narrows a count (widows/orphans) to the range breakUnit stores.
func clampI16(v int) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
