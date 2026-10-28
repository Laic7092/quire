package reader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laix/quire/internal/book"
	"github.com/laix/layx/render/paint"
)

// A serif file that names the sans file is one face in the set, and it still is
// the system's serif face: reporting none would make a host that waits for one
// open the faces again on every publication — which, for the reader, is a loop
// of font loads and memory returns that never ends.
func TestOpenFontsRecordsASerifThatIsTheSansFile(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	same, err := OpenFonts(path, path)
	if err != nil {
		t.Fatal(err)
	}
	if !same.HasSerif() {
		t.Error("a serif face that is the sans file is reported as missing")
	}
	if got, want := same.SerifPath, path; got != want {
		t.Errorf("SerifPath = %q, want %q", got, want)
	}
	if got := same.Set.FontCount(); got != 3 {
		t.Errorf("the set holds %d faces, want the three every set has: the same file is one face", got)
	}

	without, err := OpenFonts(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if without.HasSerif() {
		t.Error("a set made without a serif path reports one")
	}
}

// A serif file that is there but is not a font fails with a message that names
// both files: the sans face loaded, and the one that did not.
func TestOpenFontsNamesBothFiles(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "not-a-font.ttf")
	if err := os.WriteFile(bad, []byte("this is not a font at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = OpenFonts(path, bad)
	if err == nil {
		t.Fatal("a garbage serif file was loaded as a face")
	}
	for _, want := range []string{path, bad} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// The chrome's family name is empty until the faces are read, and the reader's
// names after: the flag is read on the UI thread while a goroutine loads them.
func TestUIFontFamilyFollowsTheLoad(t *testing.T) {
	old := uiFontReady.Load()
	t.Cleanup(func() { uiFontReady.Store(old) })
	uiFontReady.Store(false)
	if got := UIFontFamily(); got != "" {
		t.Errorf("the chrome asks for %q before the faces are read", got)
	}
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	if _, err := OpenFont(path); err != nil {
		t.Fatal(err)
	}
	if got := UIFontFamily(); got != UIFont {
		t.Errorf("the chrome asks for %q after the faces are read, want %q", got, UIFont)
	}
}

// facesOf records which family every run of a page is set in.
type facesOf struct {
	recorder
	glyphs map[string]int
}

func (f *facesOf) DrawGlyphs(run paint.GlyphRun) {
	if f.glyphs == nil {
		f.glyphs = map[string]int{}
	}
	if run.Font != nil {
		f.glyphs[run.Font.Family] += len(run.Glyphs)
	}
	f.recorder.DrawGlyphs(run)
}

// A serif page sets its Latin in the bundled book face and reaches the system's
// CJK face through the set: the serif choice never draws nothing for the glyphs
// its own face lacks, whether or not the system has a serif of its own.
func TestSerifPageFallsBackForCJK(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := OpenFonts(path, FindSerif(""))
	if err != nil {
		t.Fatal(err)
	}
	bk := &book.Book{Title: "mixed", Chapters: []book.Chapter{{Title: "一", XHTML: []byte(
		"<html><body><p>中文 mixed with abc and 汉字 again.</p></body></html>")}}}
	doc, err := bk.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560,
		Options{FontSize: 20, Font: FontSerif})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}
	rec := &facesOf{}
	page.Draw(rec)
	if rec.glyphs[familySerif] == 0 {
		t.Errorf("the serif page set no glyph in %q: %v", familySerif, rec.glyphs)
	}
	if rec.glyphs[familySans] == 0 {
		t.Errorf("the serif page reached no fallback face for its CJK: %v", rec.glyphs)
	}
	var total int
	for _, n := range rec.glyphs {
		total += n
	}
	if total < 20 {
		t.Errorf("the page drew %d glyphs, want the whole line", total)
	}
}

// The reading families are the ones FindFont's set registers, and a layout asks
// for the one the settings name.
func TestFamilyFollowsTheChoice(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fonts.Family(FontSans); got != familySans {
		t.Errorf("sans family = %q, want %q", got, familySans)
	}
	if got := fonts.Family(FontSerif); got != familySerif {
		t.Errorf("serif family = %q, want %q", got, familySerif)
	}
	if got := fonts.Family("papyrus"); got != familySans {
		t.Errorf("an unknown face asks for %q, want the sans family", got)
	}
	var nilFonts *Fonts
	if nilFonts.HasSerif() {
		t.Error("no font set reports a serif face")
	}
}
