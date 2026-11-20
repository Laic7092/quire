// Package reader turns a parsed book into pages with the layx render engine and
// paints those pages into MyGo's native ui.Painter.
package reader

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

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

	// UIFont is the family the window's own chrome is set in: Inter, which
	// stands in for the system's SF Pro with the same proportions, so the
	// toolbar and the panels measure the same on every platform.
	UIFont = "layx-ui"
)

// The reading faces travel with the app: XCharter for the Latin of a book,
// and Inter for the chrome. Both are open fonts; the system font still
// supplies the CJK the book face does not cover.
//
//go:embed fonts/XCharter-Roman.otf
var serifBook []byte

//go:embed fonts/Inter-Regular.ttf
var uiSans []byte

// cjkFallback is the face a sandbox reads CJK in: phones give an app no path to
// the system's fonts, so the reading faces have nothing to measure Chinese,
// Japanese and Korean with. It is the last face of the set on every platform,
// after the system's own, so desktops keep their own shapes.
//
//go:embed fonts/DroidSansFallback.ttf
var cjkFallback []byte

// BundledFont is what FindFont returns when the system has no CJK face the app
// can read: OpenFonts takes it as the path of the embedded cjkFallback.
const BundledFont = "bundled:DroidSansFallback"

// uiFontReady says the chrome's face has been registered with MyGo. The faces
// are read on a goroutine, off the UI thread, and the chrome asks for the family
// on that thread while they load: the flag is atomic so the answer does not race
// the load.
var uiFontReady atomic.Bool

// UIFontFamily is the family name the chrome should ask for, or "" before the
// faces have loaded and the system's own is the only one there is.
func UIFontFamily() string {
	if uiFontReady.Load() {
		return UIFont
	}
	return ""
}

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
//
// Every face of a set is a copy of a font file with tables parsed out of it, so
// a set is tens of megabytes once a system CJK face is in it: one is made when a
// book opens, not before, and the serif file is only read when the book is set
// in the serif face.
type Fonts struct {
	Set *render.FontSet
	// Path is the sans file, the one CJK text needs; SerifPath the serif one,
	// empty when the set was made without one, or with one that could not be read
	// — the system has none, or the book is set in the sans face — and the serif
	// choice reads in the sans one. A serif path that names the sans file is
	// recorded here too, since that file is in the set and is what the serif
	// choice falls back to: reporting no serif face would make a host that waits
	// for one ask for it again and again.
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
//
// A face costs about three times its file in memory once parsed, so a caller
// that may read in either face passes the serif path only for a book set in it,
// and loads the other set later if the reader changes their mind.
func OpenFonts(sansPath, serifPath string) (*Fonts, error) {
	sans := cjkFallback
	if sansPath != BundledFont {
		var err error
		sans, err = os.ReadFile(sansPath)
		if err != nil {
			return nil, fmt.Errorf("reader: font: %w", err)
		}
	}
	f := &Fonts{Path: sansPath}
	// The chrome is set in the bundled Inter, and the book's serif choice in
	// the bundled XCharter; the system's CJK face follows both in the set, so
	// Chinese and Japanese keep the system's own shapes. The chrome is the only
	// text MyGo lays out here, so only Inter is registered with it: a system CJK
	// face handed to MyGo would be copied again into the platform's own font
	// cache, a copy the reading view never draws from — its glyphs come from the
	// set below.
	_ = ui.RegisterFont(uiSans, UIFont)
	uiFontReady.Store(true)
	faces := []render.FontFace{
		{Family: familySans, Weight: 400, Data: uiSans},
		{Family: familySans, Weight: 400, Data: sans},
		{Family: familySerif, Weight: 400, Data: serifBook},
	}
	if serifPath != "" {
		// A serif file the system has but cannot hand over is left out rather
		// than failing the book: the serif choice then reads in the sans one,
		// which is the same fallback as a system without a serif face.
		if serif, err := os.ReadFile(serifPath); err == nil {
			f.SerifPath = serifPath
			// The same file for both choices is one face in the set: a second
			// parsed copy of it is exactly what a system CJK face cannot afford.
			if serifPath != sansPath {
				faces = append(faces, render.FontFace{Family: familySerif, Weight: 400, Data: serif})
			}
		}
	}
	// A serif run falls back to the sans faces without a face of the sans font
	// tagged serif: the set resolves every glyph against all of its faces, the
	// serif family first (see render.FontSet.order). Tagging the sans font
	// serif as well put a second parsed copy of it in the set, which is what a
	// system CJK face cannot afford.
	fs, err := render.NewFontSet(faces...)
	if err != nil {
		if f.SerifPath != "" && f.SerifPath != sansPath {
			return nil, fmt.Errorf("reader: load fonts %s and %s: %w", sansPath, f.SerifPath, err)
		}
		return nil, fmt.Errorf("reader: load font %s: %w", sansPath, err)
	}
	f.Set = fs
	return f, nil
}

// HasSerif reports whether the set holds the system's serif face. Without one
// the serif choice is set in the sans face and the bundled XCharter, which has
// no CJK: a reader who picked the serif face has the set made again with it. A
// serif file that names the sans file counts as the serif face too — that file
// is in the set — so a host waiting for one does not ask for it forever.
func (f *Fonts) HasSerif() bool { return f != nil && f.SerifPath != "" }

// Family is the layout family name for a face choice. The serif family always
// holds the bundled book face, whether or not the system adds a serif one after
// it, and the sans family follows both for the glyphs they lack.
func (f *Fonts) Family(choice string) string {
	if choice == FontSerif {
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
	// No CJK face the app can read: a phone, or a desktop without one. The
	// bundled face stands in, so a book still opens instead of failing.
	return BundledFont, nil
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
