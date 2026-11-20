package render

import (
	"bytes"
	"sort"
	"strings"
	"unicode"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render/paint"
)

// FontFace describes one font file (or one face in a collection).
type FontFace struct {
	Family string
	// Weight is a CSS-style numeric weight (100..900). Zero means 400.
	Weight int
	Slant  Slant
	// Data is the complete font file. NewFontSet keeps the slice instead of
	// copying it: a 16 MB CJK collection would otherwise be duplicated for the
	// lifetime of the set, and the library only ever reads it. The caller must
	// not modify or reuse the buffer while the FontSet is alive (the same
	// contract paint.Font.Data documents).
	Data []byte
	// Index selects a face inside a TrueType/OpenType collection.
	Index int
}

type fontEntry struct {
	ref   *paint.Font
	font  *font.Font
	index int
}

// FontSet is an immutable collection of font faces with fallback resolution.
//
// A FontSet is safe for concurrent use. Internally each layout worker creates
// its own font.Face caches because go-text faces are not concurrency-safe.
//
// HasFamily, Coverage, Resolve and Face answer host questions about what the set
// can draw. They read only immutable parsed state, so they may be called from
// any number of goroutines; they allocate nothing and are meant for occasional
// queries (checking a paragraph or a font picker), not for per-rune work inside
// a layout pass.
type FontSet struct {
	entries []fontEntry
}

// NewFontSet parses faces and returns an immutable font set.
func NewFontSet(faces ...FontFace) (*FontSet, error) {
	if len(faces) == 0 {
		return nil, docErrorf(ErrNoFonts, "NewFontSet called without faces")
	}
	fs := &FontSet{entries: make([]fontEntry, 0, len(faces))}
	for i, face := range faces {
		if face.Index < 0 {
			return nil, docErrorf(ErrInvalidOptions, "face %d has negative index", i)
		}
		if len(face.Data) == 0 {
			return nil, docErrorf(ErrInvalidOptions, "face %d has no data", i)
		}
		if face.Slant != SlantNone && face.Slant != SlantItalic {
			return nil, docErrorf(ErrInvalidOptions, "face %d has unknown slant %d", i, face.Slant)
		}
		weight := face.Weight
		if weight == 0 {
			weight = 400
		}
		if face.Family == "" {
			face.Family = "default"
		}

		data := face.Data
		loaders, err := ot.NewLoaders(bytes.NewReader(data))
		if err != nil {
			return nil, docErrorf(ErrInvalidOptions, "face %d %q: %v", i, face.Family, err)
		}
		if face.Index >= len(loaders) {
			return nil, docErrorf(ErrInvalidOptions, "face %d %q index %d out of range (%d faces)",
				i, face.Family, face.Index, len(loaders))
		}
		parsed, err := font.NewFont(loaders[face.Index])
		if err != nil {
			return nil, docErrorf(ErrInvalidOptions, "face %d %q: %v", i, face.Family, err)
		}
		fs.entries = append(fs.entries, fontEntry{
			ref:   &paint.Font{Family: face.Family, Data: data, Index: face.Index, Weight: weight},
			font:  parsed,
			index: weight<<8 | int(face.Slant),
		})
	}
	return fs, nil
}

// FontCount returns the number of faces in the set.
func (fs *FontSet) FontCount() int { return len(fs.entries) }

// FaceID identifies one face in a FontSet.
//
// A FaceID is an index into the set it came from and is stable for the set's
// lifetime. It is scoped to that set: passing a FaceID to a different FontSet
// selects an unrelated face.
type FaceID int

// InvalidFace is the FaceID reported when no face matches. It is negative and
// can never collide with a valid FaceID.
const InvalidFace FaceID = -1

// Face returns the font file handle for id, for a host rasterizer that needs
// the bytes of a face Resolve picked. The returned value shares its Data slice
// with the set, so the caller must not modify it (the same contract
// paint.Font.Data documents). Face reports false for InvalidFace and for ids
// this set did not produce.
func (fs *FontSet) Face(id FaceID) (paint.Font, bool) {
	if id < 0 || int(id) >= len(fs.entries) {
		return paint.Font{}, false
	}
	return *fs.entries[id].ref, true
}

// HasFamily reports whether any face in the set declares the given family.
//
// It compares the family names the host supplied in FontFace, not the name
// table inside the font file: that is what layout resolves against. Comparison
// ignores case and surrounding space, like face selection does. An empty family
// names no face and returns false.
//
// Layout reports an IssueMissingFont for a style whose non-empty FontFamily is
// absent; HasFamily is the query behind that decision, so a host can warn
// before laying out instead of after.
func (fs *FontSet) HasFamily(family string) bool {
	family = strings.TrimSpace(family)
	if family == "" {
		return false
	}
	for i := range fs.entries {
		if strings.EqualFold(fs.entries[i].ref.Family, family) {
			return true
		}
	}
	return false
}

// Coverage reports whether at least one face in the set has a nominal glyph for
// r.
//
// "Covered" means the font's character map maps r to a glyph. Coverage consults
// the cmap only: it applies no GSUB substitutions and no variation selectors, so
// it is a coverage query, not a shaping guarantee. A covered rune can still be
// shaped as .notdef by some feature configurations, and a rune that is not
// covered is exactly the tofu a host should pre-empt by choosing another font.
// Layout reports uncovered runes through IssueMissingGlyphs.
//
// The query is O(faces) and allocates nothing; it is for host checks such as
// "can this font render the sample text", not per-rune layout work.
func (fs *FontSet) Coverage(r rune) bool {
	if r < 0 || r > unicode.MaxRune {
		return false
	}
	for i := range fs.entries {
		if _, ok := fs.entries[i].font.NominalGlyph(r); ok {
			return true
		}
	}
	return false
}

// Resolve returns the face layout would use to draw r in style st, or false when
// no face in the set covers r.
//
// Faces are considered in the same preference order layout uses for fallback:
// requested family first, then matching slant, then the closest weight, then
// insertion order. Resolve answers "which fallback face will draw this rune"
// without shaping anything; a false result is the condition Layout reports as
// IssueMissingGlyphs. The query is O(faces) and allocates nothing, and it is
// safe for concurrent use.
func (fs *FontSet) Resolve(st Style, r rune) (FaceID, bool) {
	if r < 0 || r > unicode.MaxRune || len(fs.entries) == 0 {
		return InvalidFace, false
	}
	weight := st.FontWeight
	if weight == 0 {
		weight = 400
	}
	family := strings.TrimSpace(st.FontFamily)
	wantSlant := int(st.FontSlant)

	best := -1
	bestFam, bestSlantPenalty, bestDist := false, 0, 0
	for i := range fs.entries {
		if _, ok := fs.entries[i].font.NominalGlyph(r); !ok {
			continue
		}
		e := &fs.entries[i]
		fam := family != "" && strings.EqualFold(e.ref.Family, family)
		ew := e.index >> 8
		dist := ew - weight
		if dist < 0 {
			dist = -dist
		}
		slantPenalty := 0
		if e.index&0xff != wantSlant {
			slantPenalty = 1
		}
		// Strictly better only, so an unrelated tie keeps the earlier face and
		// the result matches order's stable sort exactly.
		better := best < 0 ||
			(fam && !bestFam) ||
			(fam == bestFam && slantPenalty < bestSlantPenalty) ||
			(fam == bestFam && slantPenalty == bestSlantPenalty && dist < bestDist)
		if better {
			best = i
			bestFam = fam
			bestSlantPenalty = slantPenalty
			bestDist = dist
		}
	}
	if best < 0 {
		return InvalidFace, false
	}
	return FaceID(best), true
}

// order returns face indices ordered by preference for the requested family,
// weight and slant. Faces from other families remain available as fallbacks.
func (fs *FontSet) order(family string, weight int, slant Slant) []int {
	if weight == 0 {
		weight = 400
	}
	family = strings.TrimSpace(family)
	wantSlant := int(slant)

	type scored struct {
		idx   int
		fam   bool
		dist  int
		slant int
	}
	all := make([]scored, len(fs.entries))
	for i := range fs.entries {
		e := &fs.entries[i]
		fam := family != "" && strings.EqualFold(e.ref.Family, family)
		ew := e.index >> 8
		es := e.index & 0xff
		dist := ew - weight
		if dist < 0 {
			dist = -dist
		}
		slantPenalty := 0
		if es != wantSlant {
			slantPenalty = 1
		}
		all[i] = scored{idx: i, fam: fam, dist: dist, slant: slantPenalty}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.fam != b.fam {
			return a.fam
		}
		if a.slant != b.slant {
			return a.slant < b.slant
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		return a.idx < b.idx
	})
	out := make([]int, len(all))
	for i := range all {
		out[i] = all[i].idx
	}
	return out
}

// emulateBold reports whether a host should fake a bold face for a run.
//
// Font matching has already chosen the face carried by ref. When the requested
// weight is bold (>= 600) but the matched face is not (its actual weight is
// below 600), the core sets paint.GlyphRun.EmulateBold. It deliberately does
// not alter advances or synthesize outlines: whether to embolden, and how, is
// host policy.
func emulateBold(requested int, ref *paint.Font) bool {
	if requested < 600 {
		return false
	}
	actual := 400
	if ref != nil && ref.Weight > 0 {
		actual = ref.Weight
	}
	return actual < 600
}
