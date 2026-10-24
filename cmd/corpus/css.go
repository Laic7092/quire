package main

// CSS census for the corpus ledger.
//
// The XHTML census in main.go counts tags and style="" attributes. It cannot
// see the biggest part of a book's styling: EPUBs keep their CSS in separate
// .css files, linked from every chapter. Without those files "what does the
// reader not implement" has no denominator, which is why this file exists.
//
// Nothing here is a CSS engine. It is a scanner: it walks a stylesheet far
// enough to name the declarations, selectors, at-rules and units, because
// those are the numbers that decide what a first CSS subset should cover. A
// declaration inside an unsupported @supports block is still counted, on
// purpose: the corpus question is "does this vocabulary appear", and a
// book that only guards a rule with @supports still expects it to work.

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// tally is one census row: how often something appeared, and in how many books.
// The book count is the number that matters; a single chapter repeating
// margin:0 two hundred times says less than fifty books using it once.
type tally struct {
	n     int
	books int
}

// census is one book's CSS, or the corpus after merging every book's.
type census struct {
	props     map[string]*tally // declaration property -> uses
	selectors map[string]*tally // tag, class, id, attr, pseudo, universal, descendant
	atRules   map[string]*tally
	units     map[string]*tally
	shorthand map[string]*tally // margin/padding/font/background/border spelled whole

	// books is set only on a merged census; a book's own census leaves it 0.
	books int

	links     int // distinct <link rel=stylesheet> hrefs per book
	sheets    int // .css files actually read out of the archive
	inline    int // <style> blocks and style="" attributes parsed
	missingN  int // referenced stylesheets the archive does not contain
	rules     int // style rules (one selector list each)
	decls     int // declarations
	attrDecls int // declarations that came from a style="" attribute
	important int
	imports   int

	missing []string // names of the stylesheets that were not found
}

func newCensus() *census {
	return &census{
		props:     map[string]*tally{},
		selectors: map[string]*tally{},
		atRules:   map[string]*tally{},
		units:     map[string]*tally{},
		shorthand: map[string]*tally{},
	}
}

func (c *census) bump(m map[string]*tally, key string) {
	t := m[key]
	if t == nil {
		t = &tally{}
		m[key] = t
	}
	t.n++
}

// merge folds a book's census into the corpus-wide one, which is what turns a
// declaration count into a book count.
func (c *census) merge(o *census) {
	if o == nil {
		return
	}
	fold := func(dst, src map[string]*tally) {
		for k, t := range src {
			if t.n == 0 {
				continue
			}
			d, ok := dst[k]
			if !ok {
				d = &tally{}
				dst[k] = d
			}
			d.n += t.n
			d.books++
		}
	}
	fold(c.props, o.props)
	fold(c.selectors, o.selectors)
	fold(c.atRules, o.atRules)
	fold(c.units, o.units)
	fold(c.shorthand, o.shorthand)
	c.links += o.links
	c.sheets += o.sheets
	c.inline += o.inline
	c.missingN += o.missingN
	c.rules += o.rules
	c.decls += o.decls
	c.attrDecls += o.attrDecls
	c.important += o.important
	c.imports += o.imports
	c.missing = append(c.missing, o.missing...)
}

// The units the census knows how to name. Anything else after a number (the
// "3d" in translate3d, the "b" in #a1b2c3) is not a unit and is ignored, which
// is why this is a closed list rather than "digits followed by letters".
var cssUnits = map[string]bool{
	"em": true, "rem": true, "ex": true, "ch": true, "px": true, "pt": true,
	"pc": true, "cm": true, "mm": true, "in": true, "q": true,
	"vw": true, "vh": true, "vmin": true, "vmax": true, "fr": true, "%": true,
}

// cssShorthands are the declarations whose expansion the reader would have to
// implement first, so they are counted apart from the longhands.
var cssShorthands = map[string]bool{
	"margin": true, "padding": true, "font": true, "background": true, "border": true,
}

// ---------------------------------------------------------------------------
// One book: find the sheets, read them out of the zip.

// sheetRef is a <link href> together with the document that named it. Hrefs in
// an EPUB are relative to the file that contains them, never to the archive
// root, so the base travels with the href.
type sheetRef struct{ base, href string }

// cssBook is the CSS side of scanning one EPUB.
type cssBook struct {
	files  map[string]*zip.File // exact entry names
	folded map[string]*zip.File // lowercase names, for archives with odd case
	opfDir string               // OPF directory, the fallback base for hrefs
	c      *census
	links  []sheetRef
	seen   map[string]bool // resolved href already attempted
	loaded map[string]bool // stylesheet already parsed
}

func newCSSBook(zr *zip.ReadCloser, opfPath string) *cssBook {
	b := &cssBook{
		files:  make(map[string]*zip.File, len(zr.File)),
		folded: make(map[string]*zip.File, len(zr.File)),
		opfDir: path.Dir(path.Clean(opfPath)),
		c:      newCensus(),
		seen:   map[string]bool{},
		loaded: map[string]bool{},
	}
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		b.files[name] = f
		b.folded[strings.ToLower(name)] = f
	}
	return b
}

// link records a <link rel="stylesheet">. The same three sheets are linked from
// every chapter, so a book is counted once per sheet, not once per chapter.
func (b *cssBook) link(base, href string) {
	key := b.candidates(sheetRef{base: base, href: href})[0]
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	b.c.links++
	b.links = append(b.links, sheetRef{base: base, href: href})
}

// inline records a <style> block, which is a full rule list. base is the
// document that carries it, so an @import inside a <style> resolves the same
// way a <link> in that document would.
func (b *cssBook) inline(base, css string) {
	if strings.TrimSpace(css) == "" {
		return
	}
	sub, imports := parseCSS(css)
	b.c.merge(sub)
	b.c.inline++
	b.importRecursive(imports, base, 1)
}

// attr records a style="" attribute, which is a declaration list and nothing
// else: no selectors, so it cannot hide a rule from the census.
func (b *cssBook) attr(css string) {
	if strings.TrimSpace(css) == "" {
		return
	}
	c := newCensus()
	c.parseDecls(css)
	b.c.attrDecls += c.decls
	b.c.merge(c)
	b.c.inline++
}

// load reads every linked stylesheet, then what it imports.
func (b *cssBook) load() {
	for _, ref := range b.links {
		b.sheet(ref, 2)
	}
}

// sheet reads one stylesheet and follows its @imports down to depth. Depth is
// bounded rather than trusting the archive: import cycles exist in the wild and
// the ledger must not hang on one.
func (b *cssBook) sheet(ref sheetRef, depth int) bool {
	key := b.candidates(ref)[0]
	if b.loaded[key] {
		return true
	}
	b.loaded[key] = true
	name, f := b.find(ref)
	if f == nil {
		b.c.missingN++
		b.c.missing = append(b.c.missing, ref.href)
		return false
	}
	rc, err := f.Open()
	if err != nil {
		b.c.missingN++
		b.c.missing = append(b.c.missing, ref.href)
		return false
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		b.c.missingN++
		b.c.missing = append(b.c.missing, ref.href)
		return false
	}
	b.c.sheets++
	sub, imports := parseCSS(string(data))
	b.c.merge(sub)
	if depth > 0 {
		b.importRecursive(imports, name, depth-1)
	}
	return true
}

func (b *cssBook) importRecursive(imports []string, base string, depth int) {
	for _, href := range imports {
		if strings.TrimSpace(href) == "" {
			continue
		}
		b.sheet(sheetRef{base: base, href: href}, depth)
	}
}

// candidates lists where an href may live. The first is the correct EPUB
// resolution and the one used as the identity of the sheet; the others are
// fallbacks for archives that resolve against the package root instead, which
// is common enough in hand-made books to be worth trying before crying missing.
func (b *cssBook) candidates(ref sheetRef) []string {
	var out []string
	href := strings.TrimSpace(ref.href)
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if unescaped, err := pathUnescape(href); err == nil {
		href = unescaped
	}
	if href == "" {
		if ref.base == "" {
			return []string{""}
		}
		out = append(out, path.Clean(ref.base))
	} else if strings.HasPrefix(href, "/") {
		out = append(out, path.Clean(strings.TrimPrefix(href, "/")))
	} else if ref.base == "" {
		out = append(out, path.Clean(href))
	} else {
		out = append(out, path.Clean(path.Join(path.Dir(path.Clean(ref.base)), href)))
	}
	if b.opfDir != "." && b.opfDir != "/" {
		cand := path.Clean(path.Join(b.opfDir, strings.TrimPrefix(href, "/")))
		if cand != out[0] {
			out = append(out, cand)
		}
	}
	return out
}

func (b *cssBook) find(ref sheetRef) (string, *zip.File) {
	cands := b.candidates(ref)
	for _, name := range cands {
		if f, ok := b.files[name]; ok {
			return name, f
		}
		if f, ok := b.folded[strings.ToLower(name)]; ok {
			return name, f
		}
	}
	return cands[0], nil
}

// pathUnescape decodes %20 and friends without pulling in net/url for one call.
func pathUnescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+2 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// The scanner.

// parseCSS parses a stylesheet or a <style> block. It returns the census it
// describes and the hrefs of any @import, which the caller resolves against the
// archive (a scanner cannot open files).
func parseCSS(src string) (*census, []string) {
	c := newCensus()
	s := &cssScanner{s: stripComments(src)}
	c.parseRules(s)
	return c, s.imports
}

// cssScanner walks the text of a stylesheet. It is not a tokenizer in the CSS
// spec sense: it finds the braces and semicolons that separate rules and
// declarations, and leaves the interpretation to the census methods.
type cssScanner struct {
	s       string
	pos     int
	imports []string
}

func (p *cssScanner) eof() bool { return p.pos >= len(p.s) }

func (p *cssScanner) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\r', '\n', '\f':
			p.pos++
		case '/':
			if strings.HasPrefix(p.s[p.pos:], "/*") {
				p.skipComment()
				continue
			}
			return
		default:
			return
		}
	}
}

func (p *cssScanner) skipComment() {
	if i := strings.Index(p.s[p.pos+2:], "*/"); i >= 0 {
		p.pos += 2 + i + 2
		return
	}
	p.pos = len(p.s)
}

// block consumes a {...} body, the opening brace already eaten, and returns its
// text. Nested blocks and strings are skipped rather than interpreted.
func (p *cssScanner) block() string {
	start := p.pos
	depth := 1
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '"', '\'':
			p.pos = skipString(p.s, p.pos)
		case '{':
			depth++
			p.pos++
		case '}':
			depth--
			p.pos++
			if depth == 0 {
				return p.s[start : p.pos-1]
			}
		default:
			p.pos++
		}
	}
	return p.s[start:]
}

// prelude reads up to a top-level '{' or ';' and consumes it. open reports
// whether a block follows.
func (p *cssScanner) prelude() (text string, open bool) {
	start := p.pos
	depth := 0
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '"', '\'':
			p.pos = skipString(p.s, p.pos)
		case '(':
			depth++
			p.pos++
		case ')':
			if depth > 0 {
				depth--
			}
			p.pos++
		case ';':
			if depth == 0 {
				t := p.s[start:p.pos]
				p.pos++
				return t, false
			}
			p.pos++
		case '{':
			if depth == 0 {
				t := p.s[start:p.pos]
				p.pos++
				return t, true
			}
			p.pos++
		case '}':
			// Unbalanced: let the rules loop see it and close the level.
			return p.s[start:p.pos], false
		default:
			p.pos++
		}
	}
	return p.s[start:], false
}

// parseRules walks a rule list: the top level of a stylesheet, or the inside of
// an @media / @supports block, which holds rules rather than declarations.
func (c *census) parseRules(p *cssScanner) {
	for {
		p.skipSpace()
		if p.eof() {
			return
		}
		if p.s[p.pos] == '}' {
			p.pos++
			return
		}
		prelude, open := p.prelude()
		prelude = strings.TrimSpace(prelude)
		if prelude == "" {
			continue
		}
		if name, args := atRuleName(prelude); name != "" {
			if !open {
				// @import, @charset, @namespace: statements, no block.
				c.bump(c.atRules, name)
				if name == "import" {
					c.imports++
					if href := importHref(args); href != "" {
						p.imports = append(p.imports, href)
					}
				}
				continue
			}
			c.bump(c.atRules, name)
			body := p.block()
			switch name {
			case "media", "supports", "layer", "container", "document", "scope", "starting-style":
				// Rules inside, not declarations: recurse so the declarations
				// a book only uses under @media are counted too.
				c.parseRules(&cssScanner{s: body})
			case "keyframes":
				// Keyframe selectors (0%, to) are neither tags nor classes and
				// their declarations never reach static text. Just the at-rule.
			default:
				// @font-face, @page, @counter-style: declaration lists.
				c.parseDecls(body)
			}
			continue
		}
		c.rules++
		c.countSelectors(prelude)
		c.parseDecls(p.block())
	}
}

// atRuleName splits "@media screen" into ("media", "screen"). The leading @ is
// dropped and the name is lowercased; anything without an @ is not an at-rule.
func atRuleName(s string) (name, args string) {
	if !strings.HasPrefix(s, "@") {
		return "", ""
	}
	rest := s[1:]
	if i := strings.IndexAny(rest, " \t\r\n\f("); i >= 0 {
		return strings.ToLower(rest[:i]), strings.TrimSpace(rest[i:])
	}
	return strings.ToLower(rest), ""
}

// importHref pulls the URL out of "@import url(x.css) print" or "@import "x.css"".
func importHref(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	if len(args) >= 4 && strings.EqualFold(args[:4], "url(") {
		rest := args[4:]
		if i := strings.IndexByte(rest, ')'); i >= 0 {
			rest = rest[:i]
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	if args[0] == '"' || args[0] == '\'' {
		q := args[0]
		if i := strings.IndexByte(args[1:], q); i >= 0 {
			return args[1 : 1+i]
		}
		return strings.Trim(args[1:], `"'`)
	}
	if i := strings.IndexAny(args, " \t\r\n"); i >= 0 {
		return args[:i]
	}
	return args
}

// countSelectors counts one rule's selector list. "descendant" means a complex
// selector with more than one compound, which is the thing a naive tag-only
// cascade gets wrong: the count is per complex selector, not per part.
func (c *census) countSelectors(list string) {
	for _, sel := range splitTop(list, ',') {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		c.countSelector(sel)
	}
}

func isCombinator(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', '\f', '>', '+', '~':
		return true
	}
	return false
}

func isIdentStart(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
		return true
	case ch == '_' || ch == '-' || ch == '*' || ch >= 0x80:
		return true
	}
	return false
}

func isIdentChar(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		return true
	case ch == '_' || ch == '-' || ch >= 0x80:
		return true
	}
	return false
}

// countSelector walks one complex selector. It counts every simple selector it
// meets by kind, and the whole selector once if it glues more than one compound
// together with a combinator.
func (c *census) countSelector(sel string) {
	compounds := 0
	i := 0
	for i < len(sel) {
		if isCombinator(sel[i]) {
			i++
			continue
		}
		compounds++
		for i < len(sel) && !isCombinator(sel[i]) {
			switch ch := sel[i]; {
			case ch == '.':
				i++
				for i < len(sel) && isIdentChar(sel[i]) {
					i++
				}
				c.bump(c.selectors, "class")
			case ch == '#':
				i++
				for i < len(sel) && isIdentChar(sel[i]) {
					i++
				}
				c.bump(c.selectors, "id")
			case ch == '[':
				i++
				depth := 1
				for i < len(sel) && depth > 0 {
					switch sel[i] {
					case '"', '\'':
						i = skipString(sel, i)
						continue
					case '[':
						depth++
					case ']':
						depth--
					}
					i++
				}
				c.bump(c.selectors, "attr")
			case ch == ':':
				i++
				if i < len(sel) && sel[i] == ':' {
					i++
				}
				for i < len(sel) && isIdentChar(sel[i]) {
					i++
				}
				if i < len(sel) && sel[i] == '(' {
					depth := 1
					i++
					for i < len(sel) && depth > 0 {
						switch sel[i] {
						case '"', '\'':
							i = skipString(sel, i)
							continue
						case '(':
							depth++
						case ')':
							depth--
						}
						i++
					}
				}
				c.bump(c.selectors, "pseudo")
			case ch == '*':
				i++
				c.bump(c.selectors, "universal")
			default:
				if isIdentStart(ch) {
					for i < len(sel) && isIdentChar(sel[i]) {
						i++
					}
					c.bump(c.selectors, "tag")
					continue
				}
				// Something the census does not model (a stray comma, an
				// escape). Step over it so the loop always advances.
				i++
			}
		}
	}
	if compounds > 1 {
		c.bump(c.selectors, "descendant")
	}
}

// parseDecls counts a declaration list: a rule's body, an @font-face body, or a
// style="" attribute.
func (c *census) parseDecls(body string) {
	for _, decl := range splitTop(body, ';') {
		decl = strings.TrimSpace(decl)
		if decl == "" || strings.HasPrefix(decl, "@") {
			continue
		}
		colon := strings.IndexByte(decl, ':')
		if colon <= 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(decl[:colon]))
		if prop == "" || strings.HasPrefix(prop, "--") || strings.ContainsAny(prop, " \t{}") {
			// Custom properties and nested-rule leftovers are not declarations
			// the reader could implement, so they stay out of the ledger.
			continue
		}
		value := decl[colon+1:]
		c.decls++
		c.bump(c.props, prop)
		if cssShorthands[prop] {
			c.bump(c.shorthand, prop)
		}
		c.countValue(value)
	}
}

func (c *census) countValue(value string) {
	if hasImportant(value) {
		c.important++
	}
	c.countUnits(value)
}

func hasImportant(value string) bool {
	low := strings.ToLower(value)
	for i := 0; i < len(low); i++ {
		if low[i] != '!' {
			continue
		}
		j := i + 1
		for j < len(low) && (low[j] == ' ' || low[j] == '\t' || low[j] == '\n' || low[j] == '\r') {
			j++
		}
		if strings.HasPrefix(low[j:], "important") {
			return true
		}
	}
	return false
}

// countUnits counts em/%/pt/... in a value. Only names in cssUnits count, so a
// hex colour or a translate3d never invents a unit.
func (c *census) countUnits(value string) {
	i := 0
	for i < len(value) {
		ch := value[i]
		if !(ch >= '0' && ch <= '9') && !(ch == '.' && i+1 < len(value) && value[i+1] >= '0' && value[i+1] <= '9') {
			i++
			continue
		}
		for i < len(value) && ((value[i] >= '0' && value[i] <= '9') || value[i] == '.') {
			i++
		}
		if i < len(value) && value[i] == '%' {
			c.bump(c.units, "%")
			i++
			continue
		}
		start := i
		for i < len(value) && ((value[i] >= 'a' && value[i] <= 'z') || (value[i] >= 'A' && value[i] <= 'Z')) {
			i++
		}
		if u := strings.ToLower(value[start:i]); cssUnits[u] {
			c.bump(c.units, u)
		}
	}
}

// ---------------------------------------------------------------------------
// Text helpers.

// stripComments removes /* ... */ while leaving strings alone, because a URL
// may contain the comment markers and a stylesheet that only comments out a
// rule must not have that rule counted. The comment is replaced by a space so
// "a/**/b" does not become "ab".
func stripComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch {
		case s[i] == '"' || s[i] == '\'':
			j := skipString(s, i)
			b.WriteString(s[i:j])
			i = j
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			if j := strings.Index(s[i+2:], "*/"); j >= 0 {
				i = i + 2 + j + 2
			} else {
				i = len(s)
			}
			b.WriteByte(' ')
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// skipString returns the index just past the string starting at i, escapes and
// all, or the end of the input when it is unterminated.
func skipString(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	q := s[i]
	i++
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			return i + 1
		}
		i++
	}
	return len(s)
}

// splitTop splits on sep outside quotes, parentheses, brackets and braces, so
// "font: 12px/1.4 Georgia, serif" stays one declaration and
// ":not(a, b), p" stays two selectors.
func splitTop(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			i = skipString(s, i) - 1
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		default:
			if s[i] == sep && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// ---------------------------------------------------------------------------
// Reporting.

type cssRow struct {
	name  string
	n     int
	books int
}

// rankRows sorts by book count, then by uses. The first column answers "would
// a reader meet this"; the second, "how much work is it".
func rankRows(m map[string]*tally) []cssRow {
	rows := make([]cssRow, 0, len(m))
	for k, t := range m {
		rows = append(rows, cssRow{name: k, n: t.n, books: t.books})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.books != b.books {
			return a.books > b.books
		}
		if a.n != b.n {
			return a.n > b.n
		}
		return a.name < b.name
	})
	return rows
}

// joinRows renders at most max rows as one line, for the places where a table
// would be taller than the information deserves.
func joinRows(rows []cssRow, max int, format func(cssRow) string) string {
	if max > 0 && len(rows) > max {
		rows = rows[:max]
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, format(r))
	}
	return strings.Join(parts, " ")
}

// The vocabularies the report always shows, even at zero. "we looked and the
// corpus has none of this" is a finding; a missing row is not.
var (
	selectorOrder = []string{"tag", "class", "id", "attr", "descendant", "pseudo", "universal"}
	atRuleOrder   = []string{"media", "font-face", "import", "supports"}
	unitOrder     = []string{"em", "%", "rem", "pt", "px", "vw", "vh"}
)

// namedRows keeps a fixed vocabulary first, in a fixed order: selectors are a
// taxonomy and units are a checklist, so sorting them by count would change
// the shape of the table without saying anything. Anything the corpus uses
// beyond the vocabulary follows, ranked.
func namedRows(m map[string]*tally, names []string) []cssRow {
	rows := make([]cssRow, 0, len(names)+len(m))
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
		var t tally
		if got := m[name]; got != nil {
			t = *got
		}
		rows = append(rows, cssRow{name: name, n: t.n, books: t.books})
	}
	for _, r := range rankRows(m) {
		if !seen[r.name] {
			rows = append(rows, r)
		}
	}
	return rows
}

// printTable writes one aligned console table. max<=0 means every row.
func printTable(title, countHeader string, rows []cssRow, max int) {
	if len(rows) == 0 {
		return
	}
	hidden := 0
	if max > 0 && len(rows) > max {
		hidden = len(rows) - max
		rows = rows[:max]
	}
	fmt.Printf("  %-30s %8s %6s\n", title, countHeader, "books")
	for _, r := range rows {
		fmt.Printf("  %-30s %8d %6d\n", r.name, r.n, r.books)
	}
	if hidden > 0 {
		fmt.Printf("  ... %d more (see docs/css-census.md, or -css-top 0)\n", hidden)
	}
}

// printCSSLedger writes the corpus-wide CSS section. It is short on purpose:
// the full tables belong in docs/css-census.md, and the console has to stay
// readable next to the per-book layout rows.
func printCSSLedger(c *census, top int) {
	if c == nil || (c.decls == 0 && c.sheets == 0 && c.links == 0) {
		return
	}
	fmt.Printf("\ncss ledger: %d books, %d stylesheets linked (%d missing), %d rules, %d declarations\n",
		c.books, c.links, c.missingN, c.rules, c.decls)
	if c.inline > 0 {
		// Half of a corpus can hide in style="" attributes, which no cascade on
		// the linked sheets would ever reach. The share is worth printing.
		fmt.Printf("  inline: %d style=\"\" attributes and <style> blocks, %d declarations (%.0f%% of all)\n",
			c.inline, c.attrDecls, 100*float64(c.attrDecls)/float64(c.decls))
	}
	printTable("property", "decls", rankRows(c.props), top)
	printTable("selector type", "count", namedRows(c.selectors, selectorOrder), 0)
	printTable("at-rule", "count", namedRows(c.atRules, atRuleOrder), 0)
	printTable("unit", "count", namedRows(c.units, unitOrder), 0)
	if len(c.shorthand) > 0 {
		parts := make([]string, 0, len(c.shorthand))
		for _, r := range rankRows(c.shorthand) {
			parts = append(parts, fmt.Sprintf("%s=%d[%db]", r.name, r.n, r.books))
		}
		fmt.Printf("  !important=%d  shorthands: %s\n", c.important, strings.Join(parts, " "))
	}
	if c.missingN > 0 {
		fmt.Printf("  missing stylesheets: %s\n", truncate(strings.Join(uniqSorted(c.missing), " "), 100))
	}
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// printCSSMarkdown writes the tables docs/css-census.md carries. Keeping the
// generating code here means the document can be regenerated instead of
// hand-edited, which is the only way a ledger stays true.
func printCSSMarkdown(c *census) {
	if c == nil || len(c.props) == 0 {
		return
	}
	mdTable := func(title, name, count string, rows []cssRow) {
		fmt.Printf("\n### %s\n\n| %s | %s | books |\n|---|---:|---:|\n", title, name, count)
		for _, r := range rows {
			fmt.Printf("| `%s` | %d | %d |\n", r.name, r.n, r.books)
		}
	}
	fmt.Printf("\ncss: %d books, %d stylesheets linked (%d missing), %d rules, %d declarations, %d in style=\"\" attributes, %d !important\n",
		c.books, c.links, c.missingN, c.rules, c.decls, c.attrDecls, c.important)
	mdTable("Properties", "property", "declarations", rankRows(c.props))
	mdTable("Selector types", "kind", "occurrences", namedRows(c.selectors, selectorOrder))
	mdTable("At-rules", "at-rule", "occurrences", namedRows(c.atRules, atRuleOrder))
	mdTable("Units", "unit", "occurrences", namedRows(c.units, unitOrder))
	mdTable("Shorthands", "property", "declarations", rankRows(c.shorthand))
	if c.missingN > 0 {
		fmt.Printf("\nmissing stylesheets (%d): %s\n", c.missingN, strings.Join(uniqSorted(c.missing), ", "))
	}
}
