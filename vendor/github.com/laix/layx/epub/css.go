package epub

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/laix/layx/render"
)

// This file is a small CSS reader for EPUB content: a stylesheet parser, a
// selector matcher and the declarations the cascade understands. It is not a
// browser: the supported surface follows the CSS census the reader was built
// from, and everything outside it is dropped rather than approximated. What it
// drops is reported through Options.OnIssue when a collector is attached, so a
// host can explain the difference instead of silently losing it.

// declaration is one `property: value` pair, with !important folded out.
type declaration struct {
	prop      string
	value     string
	important bool
}

// cssRule is a selector list and the declarations that apply when one of the
// selectors matches.
type cssRule struct {
	selectors []selector
	decls     []declaration
}

// cssPart is one item of a stylesheet in source order: either a rule or an
// @import. Keeping the order lets imported rules cascade where CSS puts them.
type cssPart struct {
	rule       cssRule
	importHref string
}

type cssSheet struct {
	parts []cssPart
}

// parseCSS parses a stylesheet. Malformed input never fails the book: an
// unreadable rule is dropped and the rest of the sheet still applies.
func parseCSS(src string) cssSheet {
	return parseCSSWith(src, "", nil)
}

// parseCSSWith is parseCSS for the converter. label names the stylesheet in
// diagnostics (an archive path, or a chapter path for an inline <style>), and
// issues receives what the parser had to ignore: @font-face rules (embedded
// fonts are not loaded), at-rules the reader does not implement, and rules
// whose selectors the matcher cannot honour. A nil collector discards them,
// which is what the user-agent sheet and most unit tests use.
func parseCSSWith(src, label string, issues *issueCollector) cssSheet {
	var sheet cssSheet
	src = stripCSSComments(src)
	// Old HTML wrappers around <style> content: `<!-- ... -->`. XHTML does
	// not need them, but a few hand-made EPUBs still ship them.
	src = strings.ReplaceAll(src, "<!--", " ")
	src = strings.ReplaceAll(src, "-->", " ")
	p := cssParser{label: label, issues: issues}
	p.parseLevel(src, &sheet)
	if p.droppedRules > 0 {
		p.report("%d rule(s) were ignored because none of their selectors are supported", p.droppedRules)
	}
	return sheet
}

// cssParser is one stylesheet parse. It exists to carry the diagnostic label
// and counters through the recursive @media/@supports handling.
type cssParser struct {
	label        string
	issues       *issueCollector
	droppedRules int
}

// report records one stylesheet diagnostic, prefixed with the sheet's label.
func (p *cssParser) report(format string, args ...any) {
	if p.issues == nil {
		return
	}
	detail := fmt.Sprintf(format, args...)
	if p.label != "" {
		detail = p.label + ": " + detail
	}
	p.issues.add(render.IssueDroppedContent, "", render.InvalidNode, "%s", detail)
}

func (p *cssParser) parseLevel(src string, sheet *cssSheet) {
	i := 0
	for i < len(src) {
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r' || src[i] == '\f' || src[i] == ';') {
			i++
		}
		if i >= len(src) {
			return
		}
		if src[i] == '@' {
			j := i + 1
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			name := strings.ToLower(src[i+1 : j])
			k, delim := scanCSS(src, j, "{;")
			prelude := strings.TrimSpace(src[j:k])
			switch {
			case delim == '{':
				end := matchBrace(src, k)
				// Media queries are treated as unconditional: the EPUB corpus
				// uses them only to pick screen vs print, never to react to a
				// width. @supports is equally unconditional here.
				if name == "media" || name == "supports" || name == "document" {
					p.parseLevel(src[k+1:end], sheet)
				} else {
					p.droppedAtRule(name, src[k+1:end])
				}
				i = end + 1
			case name == "import":
				if href := cssImportHref(prelude); href != "" {
					sheet.parts = append(sheet.parts, cssPart{importHref: href})
				}
				i = k + 1
			default:
				// A statement at-rule such as @namespace or @layer: there is no
				// block to parse, and nothing here implements it. @charset is
				// metadata, not content, so it stays silent.
				if name != "" && name != "charset" {
					p.report("@%s is not supported; the rule is ignored", name)
				}
				i = k
				if delim == ';' {
					i++
				}
			}
			continue
		}
		k, delim := scanCSS(src, i, "{")
		if delim != '{' {
			return
		}
		end := matchBrace(src, k)
		selText := strings.TrimSpace(src[i:k])
		selectors := parseSelectorList(selText)
		decls := parseDeclarations(src[k+1 : end])
		if len(decls) > 0 && len(selectors) == 0 && selText != "" {
			// The rule can never match, so every declaration in it is dead.
			// One aggregated note per sheet keeps this from flooding the list.
			p.droppedRules++
		}
		if len(selectors) > 0 && len(decls) > 0 {
			sheet.parts = append(sheet.parts, cssPart{rule: cssRule{selectors: selectors, decls: decls}})
		}
		i = end + 1
	}
}

// droppedAtRule reports an at-rule the reader does not implement. @font-face is
// the one that changes reading: the book's embedded face never reaches the
// FontSet, so its text is drawn with the host's fallback.
func (p *cssParser) droppedAtRule(name, body string) {
	switch name {
	case "font-face":
		family, source := "", ""
		for _, d := range parseDeclarations(body) {
			switch d.prop {
			case "font-family":
				family = unquoteCSS(strings.TrimSpace(d.value))
			case "src":
				if source == "" {
					source = firstCSSURL(d.value)
				}
			}
		}
		msg := "an embedded @font-face font is not loaded; its text falls back to the host font"
		if family != "" {
			msg = fmt.Sprintf("embedded @font-face font %q is not loaded; its text falls back to the host font", family)
		}
		if source != "" {
			msg += " (source " + source + ")"
		}
		p.report("%s", msg)
	case "page":
		p.report("@page rules are ignored: page geometry for print does not apply to reflowed output")
	case "charset":
		// An encoding declaration is metadata, not content: nothing is lost.
	case "namespace":
		p.report("@namespace is not supported: namespaced selectors cannot match, so their rules are ignored")
	case "":
		p.report("a malformed @-rule was ignored")
	default:
		p.report("@%s is not supported; its contents were ignored", name)
	}
}

// firstCSSURL returns the URL of a src-ish value, either "url(x)" or a quoted
// string, or "".
func firstCSSURL(v string) string {
	if idx := strings.Index(strings.ToLower(v), "url("); idx >= 0 {
		if end := strings.IndexByte(v[idx:], ')'); end > 0 {
			return unquoteCSS(strings.TrimSpace(v[idx+4 : idx+end]))
		}
	}
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : 1+end]
		}
	}
	return ""
}

func isIdentByte(c byte) bool {
	return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// stripCSSComments removes /* ... */ while leaving string literals untouched.
func stripCSSComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		if s[i] == '"' || s[i] == '\'' {
			q := s[i]
			b.WriteByte(s[i])
			i++
			for i < len(s) {
				b.WriteByte(s[i])
				if s[i] == '\\' && i+1 < len(s) {
					i++
					b.WriteByte(s[i])
					i++
					continue
				}
				if s[i] == q {
					i++
					break
				}
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// scanCSS returns the index of the first byte in stops that is outside a
// string, bracket or parenthesis, or len(s).
func scanCSS(s string, from int, stops string) (int, byte) {
	depth := 0
	var quote byte
	for i := from; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && strings.IndexByte(stops, c) >= 0 {
				return i, c
			}
		}
	}
	return len(s), 0
}

// matchBrace returns the index of the '}' matching the '{' at open.
func matchBrace(s string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

// cssImportHref extracts the URL of an @import prelude, either "x" or url(x).
func cssImportHref(prelude string) string {
	prelude = strings.TrimSpace(prelude)
	if prelude == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(prelude), "url(") {
		open := strings.IndexByte(prelude, '(')
		close := strings.LastIndexByte(prelude, ')')
		if open < 0 || close <= open {
			return ""
		}
		return unquoteCSS(strings.TrimSpace(prelude[open+1 : close]))
	}
	if prelude[0] == '"' || prelude[0] == '\'' {
		end := strings.IndexByte(prelude[1:], prelude[0])
		if end < 0 {
			return ""
		}
		return prelude[1 : 1+end]
	}
	fields := strings.Fields(prelude)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func unquoteCSS(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// parseDeclarations turns a declaration block into (property, value) pairs.
// Custom properties are dropped: they are not declarations a reader can
// implement.
func parseDeclarations(src string) []declaration {
	var out []declaration
	for _, raw := range splitTopLevel(src, ';') {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prop, value, ok := cutTopLevel(raw, ':')
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		if prop == "" || strings.HasPrefix(prop, "--") {
			continue
		}
		value = strings.TrimSpace(value)
		important := false
		if v, ok := trimImportant(value); ok {
			value, important = v, true
		}
		if value == "" {
			continue
		}
		out = append(out, declaration{prop: prop, value: value, important: important})
	}
	return out
}

func trimImportant(v string) (string, bool) {
	lower := strings.ToLower(v)
	idx := strings.LastIndex(lower, "!important")
	if idx < 0 {
		return v, false
	}
	if strings.TrimSpace(lower[idx+len("!important"):]) != "" {
		return v, false
	}
	return strings.TrimSpace(v[:idx]), true
}

// splitTopLevel splits on sep outside strings, brackets and parentheses.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	start, depth := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func cutTopLevel(s string, sep byte) (string, string, bool) {
	parts := splitTopLevel(s, sep)
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], string(sep)), true
}

// selector is a chain of compound selectors joined by combinators, stored
// left to right (`div > p.cls` is [div, p.cls] with combChild).
type selector struct {
	parts []compound
	combs []combinator
}

type combinator uint8

const (
	combDescendant combinator = iota
	combChild
	combAdjacent
	combSibling
)

type compound struct {
	tag     string // "" means any element
	id      string
	classes []string
	attrs   []attrSelector
	pseudos []string
}

type attrSelector struct {
	name  string
	op    string // "", "=", "~=", "^=", "$=", "*=", "|="
	value string
}

// parseSelectorList parses a comma-separated selector list. Selectors this
// reader cannot honour (pseudo-elements, :hover, :not(...)) are dropped so a
// rule never half-applies.
func parseSelectorList(src string) []selector {
	var out []selector
	for _, part := range splitTopLevel(src, ',') {
		if sel, ok := parseSelector(part); ok {
			out = append(out, sel)
		}
	}
	return out
}

func parseSelector(src string) (selector, bool) {
	src = strings.TrimSpace(src)
	if src == "" {
		return selector{}, false
	}
	var out selector
	var cur strings.Builder
	pending := combDescendant
	flush := func() bool {
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		if text == "" {
			return true
		}
		c, ok := parseCompound(text)
		if !ok {
			return false
		}
		if len(out.parts) > 0 {
			out.combs = append(out.combs, pending)
		}
		out.parts = append(out.parts, c)
		pending = combDescendant
		return true
	}
	depth := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 {
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
				if !flush() {
					return selector{}, false
				}
				continue
			}
			switch c {
			case '>':
				if !flush() {
					return selector{}, false
				}
				pending = combChild
				continue
			case '+':
				if !flush() {
					return selector{}, false
				}
				pending = combAdjacent
				continue
			case '~':
				if !flush() {
					return selector{}, false
				}
				pending = combSibling
				continue
			}
		}
		cur.WriteByte(c)
	}
	if !flush() {
		return selector{}, false
	}
	if len(out.parts) == 0 || len(out.combs) != len(out.parts)-1 {
		return selector{}, false
	}
	return out, true
}

func parseCompound(text string) (compound, bool) {
	var c compound
	i := 0
	if i < len(text) && text[i] == '*' {
		i++
	} else if i < len(text) && (unicode.IsLetter(rune(text[i])) || text[i] == '_' || text[i] == '-') {
		start := i
		for i < len(text) && (text[i] == '-' || text[i] == '_' || isASCIIAlnum(text[i]) || text[i] >= 0x80) {
			i++
		}
		c.tag = strings.ToLower(text[start:i])
	}
	for i < len(text) {
		switch text[i] {
		case '.':
			i++
			start := i
			for i < len(text) && (text[i] == '-' || text[i] == '_' || isASCIIAlnum(text[i]) || text[i] >= 0x80) {
				i++
			}
			if i == start {
				return compound{}, false
			}
			c.classes = append(c.classes, text[start:i])
		case '#':
			i++
			start := i
			for i < len(text) && (text[i] == '-' || text[i] == '_' || isASCIIAlnum(text[i]) || text[i] >= 0x80) {
				i++
			}
			if i == start {
				return compound{}, false
			}
			c.id = text[start:i]
		case '[':
			end := strings.IndexByte(text[i:], ']')
			if end < 0 {
				return compound{}, false
			}
			a, ok := parseAttrSelector(text[i+1 : i+end])
			if !ok {
				return compound{}, false
			}
			c.attrs = append(c.attrs, a)
			i += end + 1
		case ':':
			i++
			if i < len(text) && text[i] == ':' {
				// A pseudo-element is content this reader does not generate.
				return compound{}, false
			}
			start := i
			for i < len(text) && (text[i] == '-' || text[i] == '_' || isASCIIAlnum(text[i])) {
				i++
			}
			name := strings.ToLower(text[start:i])
			if i < len(text) && text[i] == '(' {
				// Functional pseudo-classes are not supported.
				return compound{}, false
			}
			switch name {
			case "link", "visited", "first-child", "last-child":
				c.pseudos = append(c.pseudos, name)
			default:
				return compound{}, false
			}
		default:
			return compound{}, false
		}
	}
	if c.tag == "" && c.id == "" && len(c.classes) == 0 && len(c.attrs) == 0 && len(c.pseudos) == 0 {
		return compound{}, false
	}
	return c, true
}

func parseAttrSelector(text string) (attrSelector, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return attrSelector{}, false
	}
	op := ""
	idx := -1
	for i := 0; i < len(text); i++ {
		if i+1 < len(text) && text[i+1] == '=' {
			switch text[i] {
			case '~', '^', '$', '*', '|':
				op, idx = text[i:i+2], i
			}
		}
		if text[i] == '=' {
			if idx < 0 || idx != i-1 {
				op, idx = "=", i
			}
			break
		}
	}
	a := attrSelector{}
	if idx < 0 {
		a.name = strings.ToLower(text)
		return a, a.name != ""
	}
	a.name = strings.ToLower(strings.TrimSpace(text[:idx]))
	a.op = op
	a.value = unquoteCSS(strings.TrimSpace(text[idx+len(op):]))
	return a, a.name != ""
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// matchPath is the ancestor chain of the element being matched: the last frame
// is the element itself, the first is the document root.
type matchPath []matchFrame

type matchFrame struct {
	node  *hnode
	index int
}

func (s selector) matches(path matchPath) bool {
	return len(path) > 0 && s.matchFrom(path, len(s.parts)-1)
}

func (s selector) matchFrom(path matchPath, i int) bool {
	if len(path) == 0 || !s.parts[i].matches(path[len(path)-1], path) {
		return false
	}
	if i == 0 {
		return true
	}
	switch s.combs[i-1] {
	case combDescendant:
		for j := len(path) - 2; j >= 0; j-- {
			if s.matchFrom(path[:j+1], i-1) {
				return true
			}
		}
	case combChild:
		if len(path) >= 2 {
			return s.matchFrom(path[:len(path)-1], i-1)
		}
	case combAdjacent:
		if len(path) >= 2 {
			if prev, index := previousElement(path, path[len(path)-1].index); prev != nil {
				next := make(matchPath, len(path))
				copy(next, path[:len(path)-1])
				next[len(path)-1] = matchFrame{node: prev, index: index}
				return s.matchFrom(next, i-1)
			}
		}
	case combSibling:
		if len(path) >= 2 {
			parent := path[len(path)-2].node
			for index := path[len(path)-1].index - 1; index >= 0; index-- {
				prev := parent.children[index]
				if prev.tag == "" {
					continue
				}
				next := make(matchPath, len(path))
				copy(next, path[:len(path)-1])
				next[len(path)-1] = matchFrame{node: prev, index: index}
				if s.matchFrom(next, i-1) {
					return true
				}
			}
		}
	}
	return false
}

func previousElement(path matchPath, before int) (*hnode, int) {
	if len(path) < 2 {
		return nil, 0
	}
	parent := path[len(path)-2].node
	for i := before - 1; i >= 0; i-- {
		if parent.children[i].tag != "" {
			return parent.children[i], i
		}
	}
	return nil, 0
}

func (c compound) matches(frame matchFrame, path matchPath) bool {
	n := frame.node
	if c.tag != "" && !strings.EqualFold(n.tag, c.tag) {
		return false
	}
	if c.id != "" && attr(n, "id") != c.id {
		return false
	}
	if len(c.classes) > 0 {
		classes := strings.Fields(attr(n, "class"))
		for _, want := range c.classes {
			found := false
			for _, got := range classes {
				if got == want {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for _, a := range c.attrs {
		if !a.matches(attr(n, a.name)) {
			return false
		}
	}
	for _, pseudo := range c.pseudos {
		switch pseudo {
		case "link", "visited":
			if n.tag != "a" || attr(n, "href") == "" {
				return false
			}
		case "first-child":
			if firstElement(path) != path[len(path)-1].index {
				return false
			}
		case "last-child":
			if lastElement(path) != path[len(path)-1].index {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func firstElement(path matchPath) int {
	if len(path) < 2 {
		return 0
	}
	parent := path[len(path)-2].node
	for i := 0; i < len(parent.children); i++ {
		if parent.children[i].tag != "" {
			return i
		}
	}
	return -1
}

func lastElement(path matchPath) int {
	if len(path) < 2 {
		return 0
	}
	parent := path[len(path)-2].node
	for i := len(parent.children) - 1; i >= 0; i-- {
		if parent.children[i].tag != "" {
			return i
		}
	}
	return -1
}

func (a attrSelector) matches(value string) bool {
	switch a.op {
	case "":
		return value != "" || a.value != ""
	case "=":
		return value == a.value
	case "~=":
		for _, f := range strings.Fields(value) {
			if f == a.value {
				return true
			}
		}
	case "^=":
		return a.value != "" && strings.HasPrefix(value, a.value)
	case "$=":
		return a.value != "" && strings.HasSuffix(value, a.value)
	case "*=":
		return a.value != "" && strings.Contains(value, a.value)
	case "|=":
		return value == a.value || strings.HasPrefix(value, a.value+"-")
	}
	return false
}

// specificity returns the three counts of the CSS cascade weight.
func (s selector) specificity() (ids, classes, tags int) {
	for _, p := range s.parts {
		if p.id != "" {
			ids++
		}
		classes += len(p.classes) + len(p.attrs) + len(p.pseudos)
		if p.tag != "" {
			tags++
		}
	}
	return ids, classes, tags
}

// parseFloatPrefix parses the number at the start of v and returns the rest.
func parseFloatPrefix(v string) (float32, string, bool) {
	v = strings.TrimSpace(v)
	end := 0
	for end < len(v) && (v[end] >= '0' && v[end] <= '9' || v[end] == '.' || v[end] == '+' || v[end] == '-' || v[end] == 'e' || v[end] == 'E') {
		end++
	}
	if end == 0 {
		return 0, v, false
	}
	f, err := strconv.ParseFloat(v[:end], 32)
	if err != nil {
		return 0, v, false
	}
	return float32(f), strings.TrimSpace(v[end:]), true
}
