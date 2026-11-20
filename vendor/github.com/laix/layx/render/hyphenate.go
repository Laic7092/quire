package render

import (
	"sort"
	"unicode"
)

// Hyphenator supplies intra-word break opportunities. The core defines only
// the interface and never bundles a dictionary; render/hyphen/en is an
// optional implementation that hosts may plug in.
type Hyphenator interface {
	// Breaks returns the positions inside word after which a hyphen may be
	// inserted. Positions are rune indexes into word, ascending, in the range
	// (0, len(word)). The core validates the range and ignores invalid ones.
	Breaks(word []rune) []int
	// Name identifies the dictionary in BreakKey. Changing dictionaries must
	// invalidate cached line boxes even when the LayoutOptions otherwise look
	// identical.
	Name() string
}

// hyphenatorName returns the dictionary name, or "" for nil.
func hyphenatorName(h Hyphenator) string {
	if h == nil {
		return ""
	}
	return h.Name()
}

// hyphenWordRune reports whether r may be part of a word handed to the
// hyphenator. Scripts without letters simply produce no words.
func hyphenWordRune(r rune) bool {
	return unicode.IsLetter(r)
}

// hyphenAt reports whether a line ending at rune offset i should get a hyphen.
func (p *paragraph) hyphenAt(i int32) bool {
	if len(p.hyphPoints) == 0 {
		return false
	}
	k := sort.Search(len(p.hyphPoints), func(j int) bool { return p.hyphPoints[j] >= i })
	return k < len(p.hyphPoints) && p.hyphPoints[k] == i
}

// hyphenatedParagraph inserts '-' before each allowed break point and returns
// a wrap-only copy of p. It also records the points on p so drawing can add
// the hyphen only when a line actually ends there, and returns a mapping from
// wrap-text boundary indexes back to original rune indexes.
//
// The original paragraph text is never modified: hit testing, selection,
// anchors and node offsets keep using original rune indexes.
func (w *textWorker) hyphenatedParagraph(p *paragraph, text []rune) (*paragraph, []rune, []int32, map[int32]struct{}) {
	p.hyphPoints = nil
	p.hyphAdv = nil
	if w.hyphenator == nil || len(text) == 0 {
		return nil, nil, nil, nil
	}
	insert := make(map[int32]struct{})
	for i := 0; i < len(text); {
		if !hyphenWordRune(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && hyphenWordRune(text[j]) {
			j++
		}
		if j-i >= 5 {
			word := text[i:j]
			for _, b := range w.hyphenator.Breaks(word) {
				if b > 0 && b < len(word) {
					insert[int32(i+b)] = struct{}{}
				}
			}
		}
		i = j
	}
	if len(insert) == 0 {
		return nil, nil, nil, nil
	}

	points := make([]int32, 0, len(insert))
	for at := range insert {
		points = append(points, at)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	p.hyphPoints = points

	wrapText := make([]rune, 0, len(text)+len(points))
	wrapIndex := make([]int32, len(text)+1)
	origIndex := make([]int32, 1, len(text)+len(points)+1)
	wrapHyph := make(map[int32]struct{}, len(points))
	for i, r := range text {
		if _, ok := insert[int32(i)]; ok {
			wrapHyph[int32(len(wrapText))] = struct{}{}
			wrapText = append(wrapText, '-')
			origIndex = append(origIndex, int32(i))
		}
		wrapIndex[i] = int32(len(wrapText))
		wrapText = append(wrapText, r)
		origIndex = append(origIndex, int32(i+1))
	}
	wrapIndex[len(text)] = int32(len(wrapText))

	segs := make([]paraSeg, len(p.segs))
	for i := range p.segs {
		seg := p.segs[i]
		seg.start = wrapIndex[seg.start]
		seg.end = wrapIndex[seg.end]
		segs[i] = seg
	}
	// The narrowed copy must keep the chapter ID: diagnostics raised while
	// breaking it name the chapter through paragraph.chapter.
	return &paragraph{segs: segs, style: p.style, chapter: p.chapter, runeLen: int32(len(wrapText))}, wrapText, origIndex, wrapHyph
}
