package main

// The CSS census has to be right on a corpus we cannot ship. These tests carry
// their own tiny stylesheet and their own tiny EPUB, so they check the scanner
// and the archive walk without /home/laix/epub-corpus.

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleCSS is small on purpose but uses one of everything the census reports:
// a comment that must not be counted, two @import forms, a selector list with
// combinators, an @media and an @supports with rules inside, an @font-face with
// declarations, !important, a shorthand, and numbers that are not units.
const sampleCSS = `@charset "utf-8";
/* margin: 99em in a comment must not be counted */
@import url("reset.css");
@import "extra.css" print;

p, .lead > span, #title[lang] {
  margin: 1em 2em;
  text-indent: 2%;
  color: #a1b2c3;
  font: 12px/1.4 Georgia, serif !important;
}

@media screen and (min-width: 40em) {
  body.tei-text { font-size: 1.2rem; padding: 0.5em 0 3pt; }
}

@font-face { font-family: "X"; src: url(x.woff); }

@supports (display: grid) { .grid { display: grid; } }
`

// uses reads a tally, treating a name the census never saw as zero instead of
// panicking: "0" is a legitimate expectation in these tests.
func uses(m map[string]*tally, name string) int {
	if t := m[name]; t != nil {
		return t.n
	}
	return 0
}

func TestParseCSSCounts(t *testing.T) {
	c, imports := parseCSS(sampleCSS)

	// imports are returned for the caller to resolve, in document order.
	if got := strings.Join(imports, ","); got != "reset.css,extra.css" {
		t.Errorf("imports = %q, want reset.css,extra.css", got)
	}

	props := map[string]int{
		"margin": 1, "text-indent": 1, "color": 1, "font": 1, "font-size": 1,
		"padding": 1, "font-family": 1, "src": 1, "display": 1,
	}
	for name, want := range props {
		if got := uses(c.props, name); got != want {
			t.Errorf("property %s = %d, want %d", name, got, want)
		}
	}
	if got, want := len(c.props), len(props); got != want {
		t.Errorf("distinct properties = %d, want %d (%v)", got, want, c.props)
	}
	if got, want := c.decls, 9; got != want {
		t.Errorf("declarations = %d, want %d", got, want)
	}

	// Selectors: p, .lead > span, #title[lang], body.tei-text, .grid.
	selectors := map[string]int{
		"tag": 3, "class": 3, "id": 1, "attr": 1,
		"descendant": 1, "universal": 0, "pseudo": 0,
	}
	for kind, want := range selectors {
		if got := uses(c.selectors, kind); got != want {
			t.Errorf("selector %s = %d, want %d", kind, got, want)
		}
	}

	atRules := map[string]int{
		"charset": 1, "import": 2, "media": 1, "font-face": 1, "supports": 1,
	}
	for name, want := range atRules {
		if got := uses(c.atRules, name); got != want {
			t.Errorf("at-rule %s = %d, want %d", name, got, want)
		}
	}

	// 40em is in an @media prelude, so it is not a declaration value; the hex
	// colour and the 1.4 line height are not units either.
	units := map[string]int{
		"em": 3, "%": 1, "px": 1, "rem": 1, "pt": 1,
		"vw": 0, "vh": 0,
	}
	for name, want := range units {
		if got := uses(c.units, name); got != want {
			t.Errorf("unit %s = %d, want %d", name, got, want)
		}
	}

	if got, want := c.important, 1; got != want {
		t.Errorf("!important = %d, want %d", got, want)
	}
	if got, want := uses(c.shorthand, "margin"), 1; got != want {
		t.Errorf("margin shorthand = %d, want %d", got, want)
	}
	if got, want := uses(c.shorthand, "font"), 1; got != want {
		t.Errorf("font shorthand = %d, want %d", got, want)
	}
	if got, want := uses(c.shorthand, "padding"), 1; got != want {
		t.Errorf("padding shorthand = %d, want %d", got, want)
	}
	// The sample never writes a border, so that row stays at zero and the
	// report says so instead of hiding it.
	if got := uses(c.shorthand, "border"); got != 0 {
		t.Errorf("border shorthand = %d, want 0", got)
	}
	// Three style rules: the selector list, and one inside each conditional
	// group. @font-face holds declarations, not a rule.
	if got, want := c.rules, 3; got != want {
		t.Errorf("rules = %d, want %d", got, want)
	}
}

func TestParseCSSEdgeCases(t *testing.T) {
	// A semicolon inside url() and one inside a string stay part of the value;
	// a data URL must not split into bogus declarations.
	c, _ := parseCSS(`a { background: url("data:image/svg+xml;base64,AA=AA"); content: "a;b"; }`)
	if got, want := c.decls, 2; got != want {
		t.Fatalf("declarations = %d, want %d (%v)", got, want, c.props)
	}
	if _, ok := c.props["a=aa"]; ok {
		t.Errorf("data URL leaked a property: %v", c.props)
	}

	// Unbalanced input must terminate and still count what it saw.
	c, _ = parseCSS(`p { margin: 1em; .oops { color: red; }`)
	if got := uses(c.props, "margin"); got != 1 {
		t.Errorf("margin = %d, want 1", got)
	}

	// A style="" attribute is a declaration list: no selectors, no rules.
	c = newCensus()
	c.parseDecls(`; text-indent: 0; margin-top:0`)
	if got, want := c.decls, 2; got != want {
		t.Errorf("declarations = %d, want %d", got, want)
	}
	if got := len(c.selectors); got != 0 {
		t.Errorf("a style attribute produced %d selectors", got)
	}

	// Custom properties are not declarations the reader could implement.
	c = newCensus()
	c.parseDecls(`--brand: #123; color: var(--brand)`)
	if _, ok := c.props["--brand"]; ok {
		t.Errorf("custom property counted as a property: %v", c.props)
	}
	if got := uses(c.props, "color"); got != 1 {
		t.Errorf("color = %d, want 1", got)
	}
}

func TestStripCommentsAndSplitTop(t *testing.T) {
	if got, want := stripComments("a/**/b/* x\ny */c"), "a b c"; got != want {
		t.Errorf("stripComments = %q, want %q", got, want)
	}
	// The comment markers inside a string are not a comment.
	if got, want := stripComments(`content: "/* kept */"`), `content: "/* kept */"`; got != want {
		t.Errorf("stripComments = %q, want %q", got, want)
	}
	got := splitTop(`:not(a, b), p[title="x,y"]`, ',')
	if len(got) != 2 {
		t.Fatalf("splitTop = %q, want 2 parts", got)
	}
	if strings.TrimSpace(got[1]) != `p[title="x,y"]` {
		t.Errorf("splitTop part 2 = %q", got[1])
	}
}

// writeTestEPUB writes a minimal EPUB whose chapters link a stylesheet that
// itself imports another, plus one link to a file the archive does not have.
// It returns the archive's path.
func writeTestEPUB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := filepath.Join(dir, "tiny.epub")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	add("OEBPS/style/main.css", `body { writing-mode: horizontal-tb; } @import "parts/extra.css"; p { text-align: justify; }`)
	add("OEBPS/style/parts/extra.css", `h1 { page-break-before: always; }`)
	add("OEBPS/ch1.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head>
  <title>one</title>
  <link rel="stylesheet" type="text/css" href="style/main.css"/>
  <link rel="icon" href="style/main.css"/>
  <link rel="stylesheet" href="style/gone.css"/>
  <style>@media print { p { orphans: 2; } }</style>
</head><body><p style="margin-top: 1em; text-indent: 2em">hi</p></body></html>`)
	add("OEBPS/ch2.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head>
  <title>two</title>
  <link rel="stylesheet" href="style/main.css"/>
</head><body><p>again</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestScanBookCSS(t *testing.T) {
	s, err := scanBook(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("scanBook: %v", err)
	}
	c := s.sheet

	// Two links, repeated from two chapters, resolve to one sheet; the icon is
	// not a stylesheet at all.
	if got, want := c.links, 2; got != want {
		t.Errorf("linked stylesheets = %d, want %d (main.css, gone.css)", got, want)
	}
	// main.css and the sheet it imports.
	if got, want := c.sheets, 2; got != want {
		t.Errorf("stylesheets read = %d, want %d", got, want)
	}
	if got, want := c.missingN, 1; got != want {
		t.Errorf("missing stylesheets = %d, want %d", got, want)
	}
	if got, want := strings.Join(c.missing, ","), "style/gone.css"; got != want {
		t.Errorf("missing = %q, want %q", got, want)
	}
	for _, prop := range []string{"writing-mode", "text-align", "page-break-before", "orphans", "margin-top", "text-indent"} {
		if c.props[prop] == nil {
			t.Errorf("property %s was not counted: %v", prop, c.props)
		}
	}
	// @import one level down, and the @media inside the <style> block.
	if uses(c.atRules, "import") != 1 || uses(c.atRules, "media") != 1 {
		t.Errorf("at-rules = %v, want import=1 media=1", c.atRules)
	}
	if got, want := c.inline, 2; got != want {
		t.Errorf("inline CSS parsed = %d, want %d (one <style>, one style=)", got, want)
	}
	// The chapters themselves must still be counted as before.
	if got, want := s.chapters, 2; got != want {
		t.Errorf("chapters = %d, want %d", got, want)
	}
}

// A broken archive is a skipped book, never a failed run: the corpus contains
// truncated downloads and the ledger has to survive them.
func TestBrokenArchiveIsSkipped(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.epub")
	if err := os.WriteFile(bad, []byte("PK\x03\x04 not really a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanBook(bad); err == nil {
		t.Fatal("scanBook accepted a broken archive")
	}
	r := runBook(bad, nil, 900, 700, 0, false)
	if r.skipped == "" {
		t.Fatalf("broken archive reported as %+v, want skipped", r)
	}
	if r.err != nil || r.panicky != "" {
		t.Errorf("broken archive counted as a failure: err=%v panic=%v", r.err, r.panicky)
	}
	if n := countFailures([]result{r}); n != 0 {
		t.Errorf("countFailures = %d, want 0", n)
	}
}
