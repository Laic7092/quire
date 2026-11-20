package render

import (
	"github.com/go-text/typesetting/shaping"
)

// punctClass groups the characters that CSS Text L4 text-spacing-trim treats as
// fullwidth punctuation, plus the ambiguous-width quotation marks that browsers
// trim as opening/closing punctuation. The spec lists the CJK forms explicitly;
// U+2018/2019/201C/201D are not in those lists, but they are what Chinese
// dialogue uses and what the browser oracle trims.
type punctClass uint8

const (
	punctNone punctClass = iota
	punctOpen
	punctClose
	punctColon
	punctComma
	punctMiddleDot
	punctIdeographicSpace
)

func punctClassOf(r rune) punctClass {
	switch r {
	case '\u3008', '\u300a', '\u300c', '\u300e', '\u3010', '\u3014', '\u3016',
		'\u3018', '\u301a', '\u301d', '\uff08', '\uff3b', '\uff5b', '\uff5f',
		'\uff62', '\u2018', '\u201c':
		return punctOpen
	case '\u3009', '\u300b', '\u300d', '\u300f', '\u3011', '\u3015', '\u3017',
		'\u3019', '\u301b', '\u301e', '\u301f', '\uff09', '\uff3d', '\uff5d',
		'\uff60', '\uff63', '\u2019', '\u201d':
		return punctClose
	case '\uff1a', '\uff1b', '\ufe13', '\ufe14':
		return punctColon
	case '\u3001', '\u3002', '\uff0c', '\uff0e', '\uff64',
		'\ufe10', '\ufe11', '\ufe12', '\ufe50', '\ufe51', '\ufe52':
		return punctComma
	case '\u30fb', '\uff65':
		return punctMiddleDot
	case '\u3000':
		return punctIdeographicSpace
	}
	return punctNone
}

// trimsAfter reports whether a punctuation class may give up its trailing half
// (closing, colon and comma forms have their blank half there).
func (c punctClass) trimsAfter() bool {
	return c == punctClose || c == punctColon || c == punctComma
}

// trimmableOpening reports whether an opening mark may give up its leading
// half, and trimmableClosing the same for a closing mark's trailing half.
//
// Only the quote-like marks are compressed, and the pair rules below are
// measured, not guessed: with Droid Sans Fallback (the linecmp corpus font)
// Chromium's text-spacing-trim halves exactly these glyphs and leaves the
// fullwidth brackets and commas alone. Chromium decides per glyph whether the
// font supports trimming, and the CJK face used for the corpus does not (the
// quotes come from a fallback face that does). scripts/linecmp.sh punct
// measures the same pairs per line, so the engine has to follow the observable
// browser rule instead of the full CSS class list; see docs/text-rules.md.
func trimmableOpening(r rune) bool {
	switch r {
	case '\u2018', '\u201c', '\u3008':
		return true
	}
	return false
}

func trimmableClosing(r rune) bool {
	switch r {
	case '\u2019', '\u201d', '\u3009':
		return true
	}
	return false
}

// trimTarget returns the rune index whose advance punctuation compression
// shortens for the adjacent pair text[a], text[b] (b == a+1). The second return
// value is false when the pair is not compressed.
//
// The rules follow text-spacing-trim: normal as the browser applies it:
//
//   - two adjacent fullwidth punctuation marks trim half an em between them;
//   - the trim is taken from the mark that owns the blank side: an opening mark
//     gives up its leading half, a closing mark (or colon/comma) its trailing
//     half;
//   - an opening mark wins the pair, so a pair never loses more than half an
//     em, and an opening mark that cannot trim blocks the pair entirely (its
//     leading blank is where the trim would have to come from).
func trimTarget(text []rune, a, b int) (int, bool) {
	if a < 0 || b >= len(text) || b != a+1 {
		return 0, false
	}
	left, right := punctClassOf(text[a]), punctClassOf(text[b])
	if left == punctNone || right == punctNone {
		return 0, false
	}
	if right == punctOpen {
		if trimmableOpening(text[b]) {
			return b, true
		}
		return 0, false
	}
	if left.trimsAfter() && trimmableClosing(text[a]) {
		return a, true
	}
	return 0, false
}

// glyphCursor walks shaped glyphs in text order. It is only used on compression
// targets, which are strictly increasing, so a single forward cursor turns the
// whole operation linear.
type glyphCursor struct {
	runs   []shapedRun
	ri, gi int
}

// find returns the run and glyph whose paragraph rune index is target, or nil
// when the glyph is not part of runs.
func (c *glyphCursor) find(target int) (*shapedRun, *shaping.Glyph) {
	for c.ri < len(c.runs) {
		run := &c.runs[c.ri]
		for c.gi < len(run.out.Glyphs) {
			g := &run.out.Glyphs[c.gi]
			idx := g.TextIndex()
			if idx < target {
				c.gi++
				continue
			}
			if idx == target {
				return run, g
			}
			return nil, nil
		}
		c.ri++
		c.gi = 0
	}
	return nil, nil
}

// compressPunctuation narrows adjacent punctuation pairs in runs, which must
// cover the paragraph rune range [start,end): the whole paragraph before
// wrapping, and one line while drawing. It runs before line breaking, so the
// shortened advances decide where lines end. Glyphs outside the range are never
// touched, but the pairs straddling a range boundary are considered, so a
// per-line shaping reproduces exactly the advances the wrap pass measured for
// the glyphs on that line.
func (w *textWorker) compressPunctuation(text []rune, runs []shapedRun, start, end int) {
	if len(text) < 2 || len(runs) == 0 {
		return
	}
	start = clampInt(start, 0, len(text))
	end = clampInt(end, start, len(text))
	lo := start - 1
	if lo < 0 {
		lo = 0
	}
	cursor := glyphCursor{runs: runs}
	for a := lo; a+1 < len(text) && a < end; a++ {
		target, ok := trimTarget(text, a, a+1)
		if !ok || target < start || target >= end {
			continue
		}
		run, g := cursor.find(target)
		if g == nil {
			continue
		}
		// Half an em: Output.Size is the font size in 26.6 fixed point.
		delta := run.out.Size / 2
		if delta > g.Advance {
			delta = g.Advance
		}
		if delta <= 0 {
			continue
		}
		g.Advance -= delta
		run.out.Advance -= delta
		if target == a+1 && punctClassOf(text[target]) == punctOpen {
			// Opening punctuation gives up its leading blank half, so its ink
			// moves left with it. The pen already advanced past the previous
			// character, so this is what closes the gap.
			g.XOffset -= delta
		}
	}
}

// isHangingPunctuation reports whether r is a closing mark that
// hanging-punctuation: allow-end may let hang into the right margin. The set
// covers the CJK closing marks (。”」，；：？！) and the other bracket styles, so
// a paragraph set in any of the usual conventions behaves the same.
func isHangingPunctuation(r rune) bool {
	switch r {
	case '\u3001', '\u3002', '\u3009', '\u300b', '\u300d', '\u300f', '\u3011',
		'\u3015', '\u3017', '\u3019', '\u301b', '\u301f',
		'\uff09', '\uff0c', '\uff0e', '\uff1a', '\uff1b', '\uff1f', '\uff01',
		'\uff3d', '\uff5d', '\uff60', '\u2019', '\u201d':
		return true
	}
	return false
}

// hangingPullCount returns how many runes at the start of the next line may be
// pulled onto the current one: the closing mark itself, or the character it is
// glued to plus the mark (UAX #14 does not break before a closing mark, so the
// wrapper usually leaves the pair together).
func hangingPullCount(text []rune, end int) int {
	if end < 0 || end >= len(text) {
		return 0
	}
	if isHangingPunctuation(text[end]) {
		return 1
	}
	if end+1 < len(text) && !isHangingPunctuation(text[end]) && !isJustifySpace(text[end]) &&
		isHangingPunctuation(text[end+1]) {
		return 2
	}
	return 0
}
