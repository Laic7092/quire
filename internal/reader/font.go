// Package reader turns a parsed book into pages with the layx render engine and
// paints those pages into MyGo's native ui.Painter.
package reader

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/go-text/typesetting/font"
	"github.com/laix/layx/render"
)

// The family names the layout asks for. Every face of the set is a fallback for
// the others, so a serif book sets Latin in the serif face and falls back to the
// sans one for the CJK glyphs it does not have.
const (
	familySans  = "book"
	familySerif = "book-serif"
)

// DefaultFonts are tried in order when neither -font nor LAYX_FONT is set:
// a CJK face of the system, for the sans choice and as the fallback of both.
var DefaultFonts = []string{
	"/System/Library/Fonts/PingFang.ttc",
	"/System/Library/Fonts/Hiragino Sans GB.ttc",
	"C:\\Windows\\Fonts\\msyh.ttc",
	"C:\\Windows\\Fonts\\simhei.ttf",
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
}

// DefaultSerifFonts are what the serif choice is set in, when the system has
// them: a book face with CJK glyphs, then anything serif for the Latin text.
var DefaultSerifFonts = []string{
	"/System/Library/Fonts/Supplemental/Songti.ttc",
	"/System/Library/Fonts/Supplemental/Times New Roman.ttf",
	"C:\\Windows\\Fonts\\simsun.ttc",
	"C:\\Windows\\Fonts\\georgia.ttf",
	"/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc",
	"/usr/share/fonts/truetype/noto/NotoSerifCJK-Regular.ttc",
	"/usr/share/fonts/truetype/arphic/uming.ttc",
	"/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf",
	"/usr/share/fonts/truetype/liberation/LiberationSerif-Regular.ttf",
}

// Fonts are the faces a book is set in, prepared both for layout (FontSet) and
// for glyph outlines (the files themselves, which the painter parses once per
// face it draws).
type Fonts struct {
	Set *render.FontSet
	// Path is the sans file, the one CJK text needs; SerifPath the serif one,
	// empty when the system has none and the sans face stands in.
	Path      string
	SerifPath string
}

// OpenFont loads path, which may be a .ttf, .otf or .ttc collection, as both
// choices: the serif one falls back to it for the glyphs it lacks.
func OpenFont(path string) (*Fonts, error) {
	return OpenFonts(path, "")
}

// OpenFonts loads the sans face and, when serifPath is not empty, the serif one.
// Both are in one set, so a serif page falls back to the sans face — a CJK
// serif font often has no Latin italics, and DejaVu Serif no CJK at all.
func OpenFonts(sansPath, serifPath string) (*Fonts, error) {
	sans, err := os.ReadFile(sansPath)
	if err != nil {
		return nil, fmt.Errorf("reader: font: %w", err)
	}
	f := &Fonts{Path: sansPath}
	faces := []render.FontFace{{Family: familySans, Weight: 400, Data: sans}}
	// The window draws its own text — the library's headings, the toolbar —
	// through MyGo, which keeps its own faces: register both under the names
	// the view asks for, so the chrome and the page are set in the same fonts.
	_ = ui.RegisterFont(sans, familySans)
	if serifPath != "" && serifPath != sansPath {
		if serif, err := os.ReadFile(serifPath); err == nil {
			faces = append(faces,
				render.FontFace{Family: familySerif, Weight: 400, Data: serif},
				render.FontFace{Family: familySerif, Weight: 400, Data: sans},
			)
			f.SerifPath = serifPath
			_ = ui.RegisterFont(serif, familySerif)
		}
	}
	fs, err := render.NewFontSet(faces...)
	if err != nil {
		return nil, fmt.Errorf("reader: load font %s: %w", sansPath, err)
	}
	f.Set = fs
	return f, nil
}

// Family is the layout family name for a face choice. A system without a serif
// face sets both choices in the sans one, rather than failing.
func (f *Fonts) Family(choice string) string {
	if f != nil && choice == FontSerif && f.SerifPath != "" {
		return familySerif
	}
	return familySans
}

// FindFont resolves the font to read with. An explicit path wins; then
// LAYX_FONT; then the first default that exists.
func FindFont(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("reader: font %s: %w", explicit, err)
		}
		return explicit, nil
	}
	if p := strings.TrimSpace(os.Getenv("LAYX_FONT")); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("reader: LAYX_FONT=%s: %w", p, err)
		}
		return p, nil
	}
	for _, p := range DefaultFonts {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("reader: no font found; set LAYX_FONT or pass -font")
}

// FindSerif resolves the serif face, or "" when the system has none: then the
// serif choice reads in the sans face instead of failing.
func FindSerif(explicit string) string {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
		return ""
	}
	if p := strings.TrimSpace(os.Getenv("LAYX_SERIF")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	for _, p := range DefaultSerifFonts {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// face returns the go-text face of one font of the set, parsed once. The
// painter calls it with the font each run names, so a page that mixes the serif
// and sans faces draws both.
func faceOf(data []byte, index int) *font.Face {
	faces, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil || len(faces) == 0 {
		return nil
	}
	if index < 0 || index >= len(faces) {
		index = 0
	}
	return faces[index]
}
