// Package reader turns a parsed book into pages with the layx render engine and
// paints those pages into MyGo's native ui.Painter.
package reader

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/go-text/typesetting/font"
	"github.com/laix/layx/render"
)

// DefaultFonts are tried in order when neither -font nor LAYX_FONT is set.
var DefaultFonts = []string{
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
}

// Fonts is one font file prepared both for layout (FontSet) and for glyph
// outlines (Face).
type Fonts struct {
	Set  *render.FontSet
	Face *font.Face
	Path string
}

// OpenFont loads path, which may be a .ttf, .otf or .ttc collection.
func OpenFont(path string) (*Fonts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reader: font: %w", err)
	}
	faces, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reader: parse font %s: %w", path, err)
	}
	if len(faces) == 0 {
		return nil, fmt.Errorf("reader: font %s has no faces", path)
	}
	fs, err := render.NewFontSet(render.FontFace{
		Family: "book",
		Weight: 400,
		Data:   data,
		Index:  0,
	})
	if err != nil {
		return nil, fmt.Errorf("reader: load font %s: %w", path, err)
	}
	return &Fonts{Set: fs, Face: faces[0], Path: path}, nil
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
