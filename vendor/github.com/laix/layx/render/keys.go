package render

import (
	"hash/maphash"
	"math"

	"github.com/laix/layx/render/paint"
)

// keyHash is a 128-bit digest. Two independent 64-bit words keep accidental
// collisions out of the cache even for very large documents.
type keyHash struct {
	a, b uint64
}

// keyer incrementally hashes layout inputs. The string hash is maphash, which
// is fast enough to re-hash a 5M-rune document on every settings change
// (unlike FNV or SHA over the same bytes).
//
// Seeds are per keyer, so digests are only comparable within one layout chain.
// That is exactly the lifetime of the variant cache.
type keyer struct {
	seedA, seedB maphash.Seed
	a, b         uint64
}

const (
	keySeedA = 0xcbf29ce484222325
	keySeedB = 0x9e3779b97f4a7c15
)

func newKeyer() *keyer {
	return &keyer{
		seedA: maphash.MakeSeed(),
		seedB: maphash.MakeSeed(),
	}
}

func (k *keyer) reset() {
	k.a = keySeedA
	k.b = keySeedB
}

func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (k *keyer) word(v uint64) {
	k.a = mix64(k.a ^ v)
	k.b = mix64(k.b ^ (v + keySeedB))
}

func (k *keyer) str(s string) {
	// Hash empty strings too: their position in the stream is significant.
	k.a = mix64(k.a ^ maphash.String(k.seedA, s))
	k.b = mix64(k.b ^ maphash.String(k.seedB, s))
}

func (k *keyer) u32(v uint32) { k.word(uint64(v)) }
func (k *keyer) i(v int)      { k.word(uint64(int64(v))) }
func (k *keyer) f32(v float32) {
	k.word(uint64(math.Float32bits(v)))
}
func (k *keyer) flag(v bool) {
	if v {
		k.word(1)
	} else {
		k.word(0)
	}
}
func (k *keyer) hash(h keyHash) {
	k.word(h.a)
	k.word(h.b)
}
func (k *keyer) color(c paint.Color) {
	k.u32(uint32(c.R))
	k.u32(uint32(c.G))
	k.u32(uint32(c.B))
	k.u32(uint32(c.A))
}

// textRules hashes a whole TextRules value. Callers never enumerate its fields,
// so a policy added to the struct automatically participates in every key that
// hashes it.
func (k *keyer) textRules(r TextRules) {
	k.flag(r.Autospacing)
	k.flag(r.PunctuationCompression)
	k.flag(r.HangingPunctuation)
	k.i(r.Widows)
	k.i(r.Orphans)
}

func (k *keyer) done() keyHash { return keyHash{a: k.a, b: k.b} }

// chapterBreakKey hashes everything that can change horizontal line breaking
// for one chapter:
//
//	text, resolved font faces/sizes, letter spacing, width, language,
//	alignment, first-line indent and TextRules
//
// TextRules is hashed whole, including the page-only widow/orphan counts, so
// that adding a policy cannot silently miss this key. The cost is that changing
// only Widows re-breaks lines; that is rare, and a stale line box would be a
// silent wrong layout.
//
// Vertical properties (line height, paragraph gap, page height) and paint
// properties (colors) are deliberately absent, so changing them keeps this
// key and reuses the chapter's line boxes.
//
// Reusing resolved styles (rather than the raw document style table plus
// typography) is what gives per-chapter granularity: a chapter whose styles
// pin every font size keeps its BreakKey when only Typography.FontSize moves.
func chapterBreakKey(k *keyer, doc *Document, ch *Chapter, opts LayoutOptions, typo Typography) keyHash {
	k.reset()
	k.str(doc.meta.Language)
	k.f32(opts.Viewport.Width)
	k.f32(opts.Margins.Left)
	k.f32(opts.Margins.Right)
	k.flag(opts.Hyphenator != nil)
	k.str(hyphenatorName(opts.Hyphenator))
	k.textRules(typo.Rules)

	var walk func(id NodeID, parent Style, parentLang string)
	walk = func(id NodeID, parent Style, parentLang string) {
		n := &ch.Nodes[id]
		st := inheritStyle(parent, doc.style(n.Style), typo)

		k.u32(uint32(n.Kind))
		k.str(n.Text)
		k.u32(uint32(n.Image))
		k.f32(n.Width)
		k.f32(n.Height)

		lang := n.Lang
		if lang == "" {
			lang = parentLang
		}
		k.str(lang)

		k.str(st.FontFamily)
		k.i(st.FontWeight)
		k.u32(uint32(st.FontSlant))
		k.f32(st.FontSize)
		k.f32(st.LetterSpacing)
		k.f32(st.WordSpacing)
		k.flag(st.Hyphenate)
		// Border widths shrink the content box (and the top/bottom widths add
		// to the block height), so they are horizontal layout inputs. Border
		// colors, backgrounds and corner radii are paint-only and absent.
		k.f32(borderWidth(st.BorderTop))
		k.f32(borderWidth(st.BorderRight))
		k.f32(borderWidth(st.BorderBottom))
		k.f32(borderWidth(st.BorderLeft))
		k.u32(uint32(st.TextAlign))
		k.f32(st.FirstLineIndent)
		k.f32(st.MarginLeft)
		k.f32(st.MarginRight)

		for _, c := range n.Children {
			walk(c, st, lang)
		}
	}
	walk(ch.Root, Style{}, doc.meta.Language)
	return k.done()
}

// pageKey adds the vertical inputs that turn line boxes into pages: line
// height, paragraph gap and page content height. Widows/Orphans are already
// part of breakKey because TextRules is hashed whole there.
func pageKey(k *keyer, breakKey keyHash, typo Typography, contentH float32) keyHash {
	k.reset()
	k.hash(breakKey)
	k.f32(typo.LineHeight)
	k.f32(typo.ParagraphGap)
	k.f32(contentH)
	return k.done()
}

// paintKey adds the theme below PageKey. It is not a layout input; changing it
// must only invalidate display lists, which are built lazily per Layout.
func paintKey(k *keyer, pageKeys []keyHash, typo Typography) keyHash {
	k.reset()
	for _, pk := range pageKeys {
		k.hash(pk)
	}
	k.color(typo.Color)
	k.color(typo.Background)
	k.color(typo.LinkColor)
	return k.done()
}

// explicitLineHeights returns the line-height multiplier that each node
// inherits from the document, with typography defaults excluded (zero). The
// break cache stores line boxes independently of Typography.LineHeight, so
// paragraph segments are normalised to this value before shaping and stacking
// can re-apply the current default.
func explicitLineHeights(doc *Document, ch *Chapter) map[NodeID]float32 {
	var out map[NodeID]float32
	var walk func(id NodeID, parent float32)
	walk = func(id NodeID, parent float32) {
		n := &ch.Nodes[id]
		lh := doc.style(n.Style).LineHeight
		if lh <= 0 {
			lh = parent
		}
		if lh > 0 {
			if out == nil {
				out = make(map[NodeID]float32)
			}
			out[id] = lh
		}
		for _, c := range n.Children {
			walk(c, lh)
		}
	}
	walk(ch.Root, 0)
	return out
}
