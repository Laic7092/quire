package render

import (
	"unicode"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
)

// This file is the LTR fast path of input segmentation.
//
// shaping.Segmenter.Split runs the Unicode bidi algorithm on every input run,
// even though this engine only produces LTR text. go-text short-circuits full
// LTR text, but its guard is deliberately conservative: a rune whose bidi class
// is not in {L, EN, ES, CS, WS} forces the full algorithm. CJK punctuation is
// ON, so every Chinese paragraph pays for it. Measured on a 160-rune CJK
// paragraph: Split allocates 6.3 KB (39.7 B/rune) with punctuation and 0 B
// without it — the whole cost is bidi running for text that has no direction.
//
// splitInput therefore does the script and face splitting itself and only calls
// the full segmenter when the text actually needs it. The rules below are copied
// from go-text v0.3.5 (shaping/input.go: splitByScript, splitByFace,
// enforceLanguages) and its paired-delimiter table; keep them in sync when the
// dependency is updated. The observable behaviour is pinned by scripts/linecmp.sh
// and render/cjk_test.go.

// ltrSimpleScripts are the scripts the fast path shapes directly. Anything else
// (Arabic, Hebrew, the Indic scripts, Thai, ...) needs the full segmenter: it
// may require bidi reordering or script-specific run splitting that this path
// does not implement.
func ltrSimpleScript(s language.Script) bool {
	switch s {
	case language.Latin, language.Greek, language.Cyrillic,
		language.Han, language.Hiragana, language.Katakana, language.Hangul,
		language.Bopomofo, language.Katakana_Or_Hiragana,
		language.Common, language.Inherited:
		return true
	}
	return false
}

// bidiControl reports whether r is an explicit bidirectional formatting
// character. Their script is Common, so they would otherwise let a paragraph
// take the LTR fast path while still carrying directionality.
func bidiControl(r rune) bool {
	switch r {
	case 0x061c, // ARABIC LETTER MARK
		0x200e, 0x200f, // LRM / RLM
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e, // embeddings and overrides
		0x2066, 0x2067, 0x2068, 0x2069: // isolates
		return true
	}
	return false
}

// delimEntry is one open paired delimiter on the script stack.
type delimEntry struct {
	index  int // in the pairedDelims table
	script language.Script
}

// isBidiParagraphSeparator reports whether r is a paragraph separator for the
// Unicode bidirectional algorithm (Bidi_Class B): LF, CR, U+001C..U+001E,
// U+0085 and U+2029. A CRLF pair is one separator; splitComplex treats it as
// such.
func isBidiParagraphSeparator(r rune) bool {
	switch r {
	case '\n', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2029:
		return true
	}
	return false
}

// splitInput segments one shaping input by script and then by face. It returns
// a slice owned by the worker, valid until the next call on the same worker.
//
// Text that may need bidi reordering or complex-script splitting is delegated
// to the full shaping.Segmenter, which is always correct but allocates. The
// segmenter's text is split at bidi paragraph separators first: UAX #9 defines
// one paragraph per separator-terminated run, and go-text's Segmenter stops its
// bidi analysis at the first separator, returning runs covering only the text
// before it. Feeding whole multi-paragraph text nodes to Split therefore used to
// leave the tail unshaped (audit §2.1). Calling Split per fragment both matches
// the standard and keeps coverage total; the separator rune stays in exactly one
// run, so every source rune has a glyph cluster and line breaking can map rune
// offsets.
func (w *textWorker) splitInput(in shaping.Input, fm shaping.Fontmap) []shaping.Input {
	if in.RunStart >= in.RunEnd {
		return nil
	}
	if !w.ltrSimple(in.Text[in.RunStart:in.RunEnd]) {
		return w.splitComplex(in, fm)
	}
	runs := w.splitByScript(in)
	return w.splitByFace(runs, fm)
}

// splitComplex runs the full segmenter over each bidi paragraph of in. The runs
// it returns cover exactly [in.RunStart, in.RunEnd): Split's result is copied
// out of the segmenter-owned buffer before the next call (each call reuses and
// invalidates it), and any hole is filled with a plain LTR run over the gap so a
// caller never sees a short list even if the segmenter misbehaves on hostile
// input.
func (w *textWorker) splitComplex(in shaping.Input, fm shaping.Fontmap) []shaping.Input {
	w.bidiRuns = w.bidiRuns[:0]
	covered := in.RunStart
	for fragStart := in.RunStart; fragStart < in.RunEnd; {
		fragEnd := fragStart
		for fragEnd < in.RunEnd {
			r := in.Text[fragEnd]
			fragEnd++
			if isBidiParagraphSeparator(r) {
				// CRLF is a single separator: keep the pair in one fragment
				// rather than starting the next one with a stray LF.
				if r == '\r' && fragEnd < in.RunEnd && in.Text[fragEnd] == '\n' {
					fragEnd++
				}
				break
			}
		}
		frag := in
		frag.RunStart, frag.RunEnd = fragStart, fragEnd
		pieces := w.segmenter.Split(frag, fm)
		for pi := range pieces {
			// Copy the Input: pieces points into the segmenter, and the next
			// Split call overwrites it.
			run := pieces[pi]
			if run.RunStart < covered {
				run.RunStart = covered
			}
			if run.RunStart >= in.RunEnd || run.RunEnd <= run.RunStart {
				continue
			}
			if run.RunEnd > in.RunEnd {
				run.RunEnd = in.RunEnd
			}
			if run.RunStart > covered {
				w.appendFallbackRun(in, covered, run.RunStart, fm)
			}
			w.bidiRuns = append(w.bidiRuns, run)
			if run.RunEnd > covered {
				covered = run.RunEnd
			}
		}
		if covered < fragEnd {
			w.appendFallbackRun(in, covered, fragEnd, fm)
			covered = fragEnd
		}
		fragStart = fragEnd
	}
	if covered < in.RunEnd {
		w.appendFallbackRun(in, covered, in.RunEnd, fm)
	}
	return w.bidiRuns
}

// appendFallbackRun adds a plain LTR run for [lo, hi) shaped with the fallback
// face. It is only used when segmentation failed to cover the input; the run
// goes to the shaper like any other, so its glyph clusters still cover the
// gap's runes.
func (w *textWorker) appendFallbackRun(in shaping.Input, lo, hi int, fm shaping.Fontmap) {
	if lo >= hi {
		return
	}
	run := in
	run.RunStart, run.RunEnd = lo, hi
	run.Direction = di.DirectionLTR
	run.Script = language.Common
	run.Face = w.faces[0]
	if fm != nil {
		run.Face = fm.ResolveFace(in.Text[lo])
	}
	w.bidiRuns = append(w.bidiRuns, run)
}

// ltrSimple reports whether text can be shaped as LTR without bidi analysis.
func (w *textWorker) ltrSimple(text []rune) bool {
	for _, r := range text {
		if bidiControl(r) {
			return false
		}
		if !ltrSimpleScript(language.LookupScript(r)) {
			return false
		}
	}
	return true
}

// splitByScript is go-text's splitByScript, writing into the worker's buffer.
func (w *textWorker) splitByScript(in shaping.Input) []shaping.Input {
	w.scriptRuns = w.scriptRuns[:0]
	currentInput := in
	currentInput.Script = language.Common

	for i := in.RunStart; i < in.RunEnd; i++ {
		r := in.Text[i]
		rScript := language.LookupScript(r)

		// Common characters take the script of their paired delimiter, if any.
		delimIndex := -1
		if !rScript.Strong() {
			delimIndex = lookupDelimIndex(r)
		}
		if delimIndex >= 0 {
			if delimIndex%2 == 0 { // open: remember the script in effect
				w.delimStack = append(w.delimStack, delimEntry{delimIndex, currentInput.Script})
			} else { // close: take the script of the matching open
				counterPartIndex := delimIndex - 1
				j := len(w.delimStack) - 1
				for ; j >= 0; j-- {
					if w.delimStack[j].index == counterPartIndex {
						rScript = w.delimStack[j].script
						break
					}
				}
				if j == -1 {
					j = 0
				}
				w.delimStack = w.delimStack[:j]
			}
		}

		if !rScript.Strong() || rScript == currentInput.Script {
			continue
		}
		if currentInput.Script == language.Common {
			// Attribute the resolved script to the pending run instead of
			// starting a new one: leading punctuation belongs to the script
			// that follows it.
			for k := range w.delimStack {
				w.delimStack[k].script = rScript
			}
			currentInput.Script = rScript
			continue
		}
		if i != in.RunStart {
			currentInput.RunEnd = i
			w.scriptRuns = append(w.scriptRuns, currentInput)
		}
		currentInput.RunStart = i
		currentInput.Script = rScript
	}
	currentInput.RunEnd = in.RunEnd
	w.scriptRuns = append(w.scriptRuns, currentInput)

	// Resolve the language of each script run, like go-text's enforceLanguages:
	// the document language is kept when it is written in that script, and
	// replaced by a language that is otherwise.
	for i := range w.scriptRuns {
		run := &w.scriptRuns[i]
		run.Language = enforceLang(run.Language, run.Script)
	}
	return w.scriptRuns
}

// enforceLang is go-text's enforceLang over exported language API.
func enforceLang(lang language.Language, s language.Script) language.Language {
	id, ok := language.NewLangID(lang)
	if !ok {
		return lang
	}
	if id.UseScript(s) {
		return lang
	}
	if l := language.ScriptToLang[s]; l != 0 {
		return l.Language()
	}
	return lang
}

// splitByFace is go-text's splitByFace, writing into the worker's buffer. Runs
// that change face are split; runes whose face does not matter (controls,
// spaces, default-ignorables) stay on the current run.
func (w *textWorker) splitByFace(runs []shaping.Input, fm shaping.Fontmap) []shaping.Input {
	w.faceRuns = w.faceRuns[:0]
	for ri := range runs {
		in := runs[ri]
		isLast := ri == len(runs)-1
		currentInput := in
		for i := in.RunStart; i < in.RunEnd; i++ {
			r := in.Text[i]
			if ignoreFaceChange(r) && (currentInput.Face != nil || !isLast || i < in.RunEnd-1) {
				continue
			}
			selectedFace := fm.ResolveFace(r)
			if currentInput.Face == nil {
				currentInput.Face = selectedFace
			}
			if currentInput.Face == selectedFace {
				continue
			}
			if i != in.RunStart {
				currentInput.RunEnd = i
				w.faceRuns = append(w.faceRuns, currentInput)
			}
			currentInput = in
			currentInput.RunStart = i
			currentInput.Face = selectedFace
		}
		currentInput.RunEnd = in.RunEnd
		w.faceRuns = append(w.faceRuns, currentInput)
	}
	return w.faceRuns
}

// ignoreFaceChange reports whether changing the face for r is pointless: the
// rune either has no visible glyph or cannot be shaped on its own.
func ignoreFaceChange(r rune) bool {
	if r >= 0xd800 && r <= 0xdfff { // surrogate
		return true
	}
	if unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
		return true
	}
	if unicode.Is(unicode.Zs, r) && r != '\u1680' { // space separator except OGHAM SPACE MARK
		return true
	}
	return harfbuzz.IsDefaultIgnorable(r)
}

// lookupDelimIndex returns the index of r in pairedDelims, or -1. Openings are
// at even indexes, their closings at the following odd index.
func lookupDelimIndex(r rune) int {
	lower, upper := 0, len(pairedDelims)-1
	for lower <= upper {
		mid := (lower + upper) / 2
		switch {
		case r < pairedDelims[mid]:
			upper = mid - 1
		case r > pairedDelims[mid]:
			lower = mid + 1
		default:
			return mid
		}
	}
	return -1
}
