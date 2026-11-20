package epub

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// DefaultRuleColor paints horizontal rules and borders that name no colour.
// Black text is the common case, so a neutral grey reads on both light and
// dark paper.
var DefaultRuleColor = paint.Color{R: 0x80, G: 0x80, B: 0x80, A: 0xff}

// display is the CSS display role the converter cares about: it decides
// whether an element becomes a block, an inline run or disappears.
type display uint8

const (
	displayInline display = iota
	displayBlock
	displayListItem
	displayTable
	displayNone
)

// listStyle is the marker a list item draws, from list-style-type.
type listStyle uint8

const (
	listDecimal listStyle = iota
	listDisc
	listCircle
	listSquare
	listLowerAlpha
	listUpperAlpha
	listLowerRoman
	listUpperRoman
	listNone
)

// computed is one element's cascaded style plus the state the converter needs
// and render.Style cannot express.
type computed struct {
	style     render.Style
	fontSize  float32 // effective size in CSS px, for em/% resolution
	display   display
	listStyle listStyle
	pre       bool // white-space preserves newlines and runs of spaces

	padTop, padRight, padBottom, padLeft float32

	// borderCurrent records an edge whose colour was not named, so it can be
	// resolved to currentColor once the whole cascade has run.
	borderCurrent [4]bool

	changed bool // an element declaration matched, so it needs its own node
}

// inherit copies the inherited fields from a parent style. Box properties are
// deliberately not inherited: CSS margins and borders are local to an element.
func inherit(p computed) computed {
	out := computed{fontSize: p.fontSize, listStyle: p.listStyle, pre: p.pre}
	out.style.FontFamily = p.style.FontFamily
	out.style.FontWeight = p.style.FontWeight
	out.style.FontSlant = p.style.FontSlant
	out.style.FontSize = p.style.FontSize
	out.style.LineHeight = p.style.LineHeight
	out.style.LetterSpacing = p.style.LetterSpacing
	out.style.WordSpacing = p.style.WordSpacing
	out.style.Hyphenate = p.style.Hyphenate
	out.style.Color = p.style.Color
	out.style.TextAlign = p.style.TextAlign
	out.style.TextDecoration = p.style.TextDecoration
	out.style.TextDecorationColor = p.style.TextDecorationColor
	out.style.FirstLineIndent = p.style.FirstLineIndent
	return out
}

// cascade holds one level of styles for one chapter: the user-agent rules,
// the author rules in document order, and the lengths that only the host can
// resolve.
type cascade struct {
	base        float32 // root font size in CSS px; rem is relative to it
	width       float32 // containing block width for %, 0 when unknown
	ua          []cssRule
	uaByTag     map[string][]indexedRule
	uaDecls     map[string][]declaration
	uaOther     []indexedRule
	rules       []cssRule
	ruleColor   paint.Color
	ignoreColor bool
	// cands is reused across elements: one cascade runs at a time, so the
	// per-element candidate list does not need a fresh allocation.
	cands []cssCandidate
}

type cssPriority struct {
	important          bool
	origin             uint8 // 0 user agent, 1 author, 2 style=""
	ids, classes, tags int
	order              int
}

func (a cssPriority) less(b cssPriority) bool {
	if a.important != b.important {
		return !a.important
	}
	if a.origin != b.origin {
		return a.origin < b.origin
	}
	if a.ids != b.ids {
		return a.ids < b.ids
	}
	if a.classes != b.classes {
		return a.classes < b.classes
	}
	if a.tags != b.tags {
		return a.tags < b.tags
	}
	return a.order < b.order
}

type cssCandidate struct {
	prio cssPriority
	decl declaration
}

// indexedRule keeps a rule's position in its sheet, so splitting the
// user-agent sheet by tag does not change cascade order.
type indexedRule struct {
	rule  cssRule
	order int
}

// indexUA splits the user-agent sheet by element tag. Its selectors are all
// bare tags, so matching an element only has to look at its own tag instead of
// walking every UA rule.
func indexUA(rules []cssRule) (map[string][]indexedRule, []indexedRule) {
	byTag := make(map[string][]indexedRule, len(rules))
	var other []indexedRule
	order := 0
	for _, rule := range rules {
		simple := true
		for _, sel := range rule.selectors {
			if len(sel.parts) != 1 || sel.parts[0].tag == "" || sel.parts[0].id != "" ||
				len(sel.parts[0].classes) > 0 || len(sel.parts[0].attrs) > 0 || len(sel.parts[0].pseudos) > 0 {
				simple = false
				break
			}
		}
		if simple {
			for _, sel := range rule.selectors {
				one := cssRule{selectors: []selector{sel}, decls: rule.decls}
				byTag[sel.parts[0].tag] = append(byTag[sel.parts[0].tag], indexedRule{rule: one, order: order})
			}
		} else {
			other = append(other, indexedRule{rule: rule, order: order})
		}
		order += len(rule.decls) + 1
	}
	return byTag, other
}

// root returns the style of the document root. The root carries the base font
// size, so a fragment without <body> still resolves em lengths.
func (c *cascade) root() computed {
	cs := computed{fontSize: c.base, display: displayBlock, listStyle: listDecimal}
	cs.style.FontSize = c.base
	return cs
}

// newCascade builds one chapter's cascade: the user-agent sheet for this base
// size and host colours, then the author rules in document order.
func newCascade(base float32, o Options, rules []cssRule) *cascade {
	if base <= 0 {
		base = 18
	}
	return newCascadeWithUA(base, o, uaRules(base, o.linkColor(), o.ruleColor()), rules)
}

// newCascadeWithUA is newCascade for a Build that parsed the user-agent sheet
// once and shares it across chapters.
func newCascadeWithUA(base float32, o Options, ua []cssRule, rules []cssRule) *cascade {
	if base <= 0 {
		base = 18
	}
	byTag, other := indexUA(ua)
	decls := make(map[string][]declaration, len(byTag))
	for tag, byTagRules := range byTag {
		for _, ir := range byTagRules {
			decls[tag] = append(decls[tag], ir.rule.decls...)
		}
	}
	return &cascade{
		base:        base,
		width:       o.ContainerWidth,
		ua:          ua,
		uaByTag:     byTag,
		uaDecls:     decls,
		uaOther:     other,
		rules:       rules,
		ruleColor:   o.ruleColor(),
		ignoreColor: o.IgnoreColor,
	}
}

// compute cascades one element on top of its parent's computed style. path is
// the ancestor chain ending at n, used by selector matching.
func (c *cascade) compute(n *hnode, parent computed, path matchPath) computed {
	out := inherit(parent)
	out.display = defaultDisplay(n.tag)
	if n.tag == "ol" {
		out.listStyle = listDecimal
	} else if n.tag == "ul" {
		out.listStyle = listDisc
	}

	// A book with no author CSS and no inline style is the common case: each
	// element's style is its tag's UA declarations over what it inherited, so
	// matching and sorting can be skipped entirely.
	if len(c.rules) == 0 && len(c.uaOther) == 0 && attr(n, "style") == "" {
		decls := c.uaDecls[n.tag]
		if len(decls) == 0 {
			return out
		}
		for _, d := range decls {
			if d.prop == "font-size" {
				c.apply(&out, parent, d, false)
			}
		}
		for _, d := range decls {
			if d.prop != "font-size" {
				c.apply(&out, parent, d, false)
			}
		}
		out.changed = true
		return out
	}

	base := len(c.cands)
	addIndexed := func(rules []indexedRule, origin uint8) {
		for _, ir := range rules {
			for _, sel := range ir.rule.selectors {
				if !sel.matches(path) {
					continue
				}
				ids, classes, tags := sel.specificity()
				for di, d := range ir.rule.decls {
					c.cands = append(c.cands, cssCandidate{
						prio: cssPriority{
							important: d.important,
							origin:    origin,
							ids:       ids,
							classes:   classes,
							tags:      tags,
							order:     ir.order + di,
						},
						decl: d,
					})
				}
			}
		}
	}
	addIndexed(c.uaByTag[n.tag], 0)
	addIndexed(c.uaOther, 0)
	order := 0
	addIndexedAuthor := func() {
		for _, rule := range c.rules {
			for _, sel := range rule.selectors {
				if !sel.matches(path) {
					continue
				}
				ids, classes, tags := sel.specificity()
				for di, d := range rule.decls {
					c.cands = append(c.cands, cssCandidate{
						prio: cssPriority{
							important: d.important,
							origin:    1,
							ids:       ids,
							classes:   classes,
							tags:      tags,
							order:     order + di,
						},
						decl: d,
					})
				}
			}
			order += len(rule.decls) + 1
		}
	}
	addIndexedAuthor()
	if inline := attr(n, "style"); inline != "" {
		for di, d := range parseDeclarations(inline) {
			c.cands = append(c.cands, cssCandidate{
				prio: cssPriority{important: d.important, origin: 2, order: di},
				decl: d,
			})
		}
	}
	cands := c.cands[base:]
	if len(cands) == 0 {
		return out
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].prio.less(cands[j].prio) })
	// font-size goes first: every other em length resolves against the
	// element's own final size, not against whatever the parent had.
	for _, cand := range cands {
		if cand.decl.prop == "font-size" {
			c.apply(&out, parent, cand.decl, cand.prio.origin != 0)
			out.changed = true
		}
	}
	for _, cand := range cands {
		if cand.decl.prop != "font-size" {
			c.apply(&out, parent, cand.decl, cand.prio.origin != 0)
			out.changed = true
		}
	}
	c.cands = c.cands[:base]
	return out
}

// renderStyle combines margins and padding and resolves currentColor borders,
// producing the entry that goes into the document style table.
func (c *cascade) renderStyle(cs computed) render.Style {
	s := cs.style
	s.MarginTop += cs.padTop
	s.MarginRight += cs.padRight
	s.MarginBottom += cs.padBottom
	s.MarginLeft += cs.padLeft
	fix := func(b *render.Border, current bool) {
		if !current || b.Width <= 0 {
			return
		}
		if !s.Color.Transparent() {
			b.Color = s.Color
		} else {
			b.Color = c.ruleColor
		}
	}
	fix(&s.BorderTop, cs.borderCurrent[0])
	fix(&s.BorderRight, cs.borderCurrent[1])
	fix(&s.BorderBottom, cs.borderCurrent[2])
	fix(&s.BorderLeft, cs.borderCurrent[3])
	return s
}

func (c *cascade) apply(out *computed, parent computed, d declaration, author bool) {
	v := strings.TrimSpace(d.value)
	low := strings.ToLower(v)
	if low == "inherit" {
		return
	}
	switch d.prop {
	case "font-size":
		if size, ok := c.fontSize(low, parent.fontSize); ok {
			out.fontSize = size
			out.style.FontSize = size
		}
	case "font-weight":
		if weight, ok := fontWeight(low, parent.style.FontWeight); ok {
			out.style.FontWeight = weight
		}
	case "font-style":
		switch low {
		case "italic", "oblique":
			out.style.FontSlant = render.SlantItalic
		case "normal", "initial":
			out.style.FontSlant = render.SlantNone
		}
	case "font-family":
		if fam := firstFontFamily(v); fam != "" {
			out.style.FontFamily = fam
		}
	case "line-height":
		if lh, ok := c.lineHeight(low, out.fontSize); ok {
			out.style.LineHeight = lh
		}
	case "letter-spacing":
		if low == "normal" || low == "initial" {
			out.style.LetterSpacing = 0
		} else if n, ok := c.length(low, out.fontSize); ok {
			out.style.LetterSpacing = n
		}
	case "word-spacing":
		if low == "normal" || low == "initial" {
			out.style.WordSpacing = 0
		} else if n, ok := c.length(low, out.fontSize); ok {
			out.style.WordSpacing = n
		}
	case "color":
		// The user-agent link colour is the host's choice; IgnoreColor only
		// silences ink the book itself declares.
		if c.ignoreColor && author {
			return
		}
		if low == "initial" {
			out.style.Color = paint.Color{}
			return
		}
		if col, ok := parseColor(low); ok {
			out.style.Color = col
		}
	case "text-align":
		switch low {
		case "left", "start":
			out.style.TextAlign = render.AlignLeft
		case "right", "end":
			out.style.TextAlign = render.AlignRight
		case "center":
			out.style.TextAlign = render.AlignCenter
		case "justify":
			out.style.TextAlign = render.AlignJustify
		case "initial", "inherit":
			out.style.TextAlign = render.AlignDefault
		}
	case "text-decoration", "text-decoration-line":
		switch {
		case strings.Contains(low, "underline"):
			out.style.TextDecoration = render.TextDecorationUnderline
		case strings.Contains(low, "line-through"):
			out.style.TextDecoration = render.TextDecorationLineThrough
		case low == "none" || low == "initial":
			out.style.TextDecoration = render.TextDecorationNone
		}
		if col, ok := parseColor(strings.TrimSpace(strings.TrimPrefix(low, "underline"))); ok {
			out.style.TextDecorationColor = col
		}
	case "text-indent":
		if n, ok := c.length(low, out.fontSize); ok {
			out.style.FirstLineIndent = n
		}
	case "margin":
		out.style.MarginTop, out.style.MarginRight, out.style.MarginBottom, out.style.MarginLeft = c.boxShorthand(low, out.fontSize)
	case "margin-top":
		if n, ok := c.length(low, out.fontSize); ok {
			out.style.MarginTop = n
		}
	case "margin-right":
		if n, ok := c.length(low, out.fontSize); ok {
			out.style.MarginRight = n
		}
	case "margin-bottom":
		if n, ok := c.length(low, out.fontSize); ok {
			out.style.MarginBottom = n
		}
	case "margin-left":
		if n, ok := c.length(low, out.fontSize); ok {
			out.style.MarginLeft = n
		}
	case "padding":
		out.padTop, out.padRight, out.padBottom, out.padLeft = c.boxShorthand(low, out.fontSize)
	case "padding-top":
		if n, ok := c.length(low, out.fontSize); ok {
			out.padTop = n
		}
	case "padding-right":
		if n, ok := c.length(low, out.fontSize); ok {
			out.padRight = n
		}
	case "padding-bottom":
		if n, ok := c.length(low, out.fontSize); ok {
			out.padBottom = n
		}
	case "padding-left":
		if n, ok := c.length(low, out.fontSize); ok {
			out.padLeft = n
		}
	case "border":
		c.applyBorder(out, v, [4]bool{true, true, true, true})
	case "border-top":
		c.applyBorder(out, v, [4]bool{true, false, false, false})
	case "border-right":
		c.applyBorder(out, v, [4]bool{false, true, false, false})
	case "border-bottom":
		c.applyBorder(out, v, [4]bool{false, false, true, false})
	case "border-left":
		c.applyBorder(out, v, [4]bool{false, false, false, true})
	case "page-break-before", "break-before":
		out.style.BreakBefore = forcedBreak(low)
	case "page-break-after", "break-after":
		out.style.BreakAfter = forcedBreak(low)
	case "page-break-inside", "break-inside":
		out.style.KeepTogether = low == "avoid"
	case "widows":
		if n, err := strconv.Atoi(low); err == nil && n > 0 {
			out.style.Widows = n
		}
	case "orphans":
		if n, err := strconv.Atoi(low); err == nil && n > 0 {
			out.style.Orphans = n
		}
	case "display":
		if d, ok := parseDisplay(low); ok {
			out.display = d
		}
	case "list-style-type":
		if ls, ok := parseListStyleType(low); ok {
			out.listStyle = ls
		}
	case "list-style":
		if ls, ok := parseListStyleShorthand(low); ok {
			out.listStyle = ls
		}
	case "white-space":
		switch low {
		case "pre", "pre-wrap", "pre-line", "break-spaces":
			out.pre = true
		case "normal", "nowrap", "initial":
			out.pre = false
		}
	case "hyphens":
		switch low {
		case "auto":
			out.style.Hyphenate = true
		case "none", "manual", "initial":
			out.style.Hyphenate = false
		}
	}
}

func forcedBreak(v string) bool {
	switch v {
	case "always", "page", "left", "right", "recto", "verso":
		return true
	}
	return false
}

func (c *cascade) applyBorder(out *computed, value string, sides [4]bool) {
	width, color, hasColor, ok := c.parseBorder(value)
	if !ok {
		return
	}
	borders := [4]*render.Border{&out.style.BorderTop, &out.style.BorderRight, &out.style.BorderBottom, &out.style.BorderLeft}
	for i, on := range sides {
		if on {
			*borders[i] = render.Border{Width: width, Color: color}
			out.borderCurrent[i] = !hasColor
		}
	}
}

// parseBorder reads a border shorthand: width, style and colour in any order.
// Styles beyond solid are accepted but drawn solid, which is all the IR has.
func (c *cascade) parseBorder(value string) (width float32, color paint.Color, hasColor, ok bool) {
	low := strings.ToLower(strings.TrimSpace(value))
	if low == "none" || low == "initial" || low == "0" || low == "hidden" || strings.HasPrefix(low, "0 ") {
		return 0, paint.Color{}, false, true
	}
	width = 3 // CSS border-width: medium
	for _, tok := range strings.Fields(low) {
		switch tok {
		case "thin":
			width = 1
			continue
		case "medium":
			width = 3
			continue
		case "thick":
			width = 5
			continue
		case "none", "hidden":
			return 0, paint.Color{}, false, true
		case "solid", "dashed", "dotted", "double", "groove", "ridge", "inset", "outset":
			continue
		}
		if col, ok := parseColor(tok); ok {
			color, hasColor = col, true
			continue
		}
		if n, ok := c.length(tok, c.base); ok {
			width = n
		}
	}
	return width, color, hasColor, true
}

// boxShorthand resolves the 1-to-4 value margin/padding form.
func (c *cascade) boxShorthand(v string, fontSize float32) (top, right, bottom, left float32) {
	parts := strings.Fields(v)
	vals := make([]float32, 0, 4)
	for _, p := range parts {
		if n, ok := c.length(p, fontSize); ok {
			vals = append(vals, n)
		}
	}
	switch len(vals) {
	case 1:
		return vals[0], vals[0], vals[0], vals[0]
	case 2:
		return vals[0], vals[1], vals[0], vals[1]
	case 3:
		return vals[0], vals[1], vals[2], vals[1]
	case 4:
		return vals[0], vals[1], vals[2], vals[3]
	}
	return 0, 0, 0, 0
}

// length reads a CSS length. Percentages resolve against the containing block
// width; when the host does not supply one they are zero rather than a guess.
func (c *cascade) length(v string, fontSize float32) (float32, bool) {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	switch low {
	case "auto", "initial":
		return 0, true
	case "normal":
		return 0, false
	}
	units := []string{"rem", "px", "em", "%", "pt", "pc", "in", "cm", "mm", "vw", "vh", "vmin", "vmax", "q"}
	unit := ""
	num := low
	for _, u := range units {
		if strings.HasSuffix(low, u) {
			unit, num = u, strings.TrimSpace(strings.TrimSuffix(low, u))
			break
		}
	}
	f, err := strconv.ParseFloat(num, 32)
	if err != nil {
		return 0, false
	}
	switch unit {
	case "", "px":
		return float32(f), true
	case "em":
		return float32(f) * fontSize, true
	case "rem":
		return float32(f) * c.base, true
	case "%":
		if c.width > 0 {
			return float32(f) * c.width / 100, true
		}
		return 0, true
	case "pt":
		return float32(f) * 96 / 72, true
	case "pc":
		return float32(f) * 16, true
	case "in":
		return float32(f) * 96, true
	case "cm":
		return float32(f) * 96 / 2.54, true
	case "mm":
		return float32(f) * 96 / 25.4, true
	case "q":
		return float32(f) * 96 / 25.4 / 4, true
	case "vw", "vh", "vmin", "vmax":
		return 0, true
	}
	return 0, false
}

// fontSize resolves font-size, where em and % are relative to the parent size.
func (c *cascade) fontSize(v string, parent float32) (float32, bool) {
	switch v {
	case "initial", "medium":
		return c.base, true
	case "xx-small":
		return c.base * 0.6, true
	case "x-small":
		return c.base * 0.7, true
	case "small":
		return c.base * 0.85, true
	case "large":
		return c.base * 1.2, true
	case "x-large":
		return c.base * 1.5, true
	case "xx-large":
		return c.base * 2, true
	case "smaller":
		return parent / 1.2, true
	case "larger":
		return parent * 1.2, true
	}
	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 32)
		if err != nil {
			return 0, false
		}
		return parent * float32(f) / 100, true
	}
	return c.length(v, parent)
}

// lineHeight turns line-height into the multiplier render.Style wants.
func (c *cascade) lineHeight(v string, fontSize float32) (float32, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "normal" || v == "initial" {
		return 0, false
	}
	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 32)
		if err != nil {
			return 0, false
		}
		return float32(f) / 100, true
	}
	if f, rest, ok := parseFloatPrefix(v); ok && rest == "" {
		return f, true // unitless multiplier
	}
	if n, ok := c.length(v, fontSize); ok {
		if fontSize <= 0 {
			return 0, false
		}
		return n / fontSize, true
	}
	return 0, false
}

func fontWeight(v string, parent int) (int, bool) {
	switch v {
	case "normal", "initial":
		return 400, true
	case "bold":
		return 700, true
	case "bolder":
		switch {
		case parent < 400:
			return 400, true
		case parent < 600:
			return 700, true
		default:
			return 900, true
		}
	case "lighter":
		switch {
		case parent <= 100:
			return 100, true
		case parent <= 400:
			return 100, true
		case parent <= 600:
			return 400, true
		default:
			return 700, true
		}
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 1000 {
		return n, true
	}
	return 0, false
}

func firstFontFamily(v string) string {
	for _, part := range splitTopLevel(v, ',') {
		fam := unquoteCSS(strings.TrimSpace(part))
		if fam != "" && !strings.EqualFold(fam, "inherit") && !strings.EqualFold(fam, "initial") {
			return fam
		}
	}
	return ""
}

func parseDisplay(v string) (display, bool) {
	switch v {
	case "none":
		return displayNone, true
	case "block", "flex", "grid", "flow-root", "table-row", "table-row-group",
		"table-header-group", "table-footer-group", "table-caption", "table-column",
		"table-column-group":
		return displayBlock, true
	case "list-item":
		return displayListItem, true
	case "table", "inline-table":
		return displayTable, true
	case "inline", "inline-block", "table-cell", "contents", "initial":
		return displayInline, true
	}
	return 0, false
}

func defaultDisplay(tag string) display {
	switch tag {
	case "li":
		return displayListItem
	case "table":
		return displayTable
	case "":
		return displayInline
	}
	if blockTags[tag] {
		return displayBlock
	}
	return displayInline
}

func parseListStyleType(v string) (listStyle, bool) {
	switch v {
	case "decimal":
		return listDecimal, true
	case "disc":
		return listDisc, true
	case "circle":
		return listCircle, true
	case "square":
		return listSquare, true
	case "lower-alpha", "lower-latin":
		return listLowerAlpha, true
	case "upper-alpha", "upper-latin":
		return listUpperAlpha, true
	case "lower-roman":
		return listLowerRoman, true
	case "upper-roman":
		return listUpperRoman, true
	case "none":
		return listNone, true
	}
	return 0, false
}

func parseListStyleShorthand(v string) (listStyle, bool) {
	for _, tok := range strings.Fields(v) {
		if ls, ok := parseListStyleType(tok); ok {
			return ls, true
		}
	}
	return 0, false
}

// styleRegistry dedupes the cascaded styles of a document. The zero style is
// entry 0, so a text node with Style 0 inherits its block's style untouched.
type styleRegistry struct {
	styles []render.Style
	index  map[render.Style]render.StyleID
	last   render.Style
	lastID render.StyleID
}

func newStyleRegistry() *styleRegistry {
	zero := render.Style{}
	return &styleRegistry{
		styles: []render.Style{zero},
		index:  map[render.Style]render.StyleID{zero: 0},
	}
}

func (r *styleRegistry) add(s render.Style) render.StyleID {
	// Adjacent text and sibling blocks usually share one style.
	if s == r.last {
		return r.lastID
	}
	if id, ok := r.index[s]; ok {
		r.last, r.lastID = s, id
		return id
	}
	id := render.StyleID(len(r.styles))
	r.styles = append(r.styles, s)
	r.index[s] = id
	r.last, r.lastID = s, id
	return id
}

func (r *styleRegistry) list() []render.Style {
	return r.styles
}

// uaCSS is the reader's user-agent stylesheet: the defaults that used to be
// hard-coded style IDs. Author CSS cascades on top of it, so any of these can
// be overridden by the book.
const uaCSS = `
html, body { margin: 0 }
body { font-size: %gpx }
p { text-indent: 2em; margin-bottom: 0.6em }
h1, h2, h3, h4, h5, h6 { font-weight: bold; margin-top: 0.6em; margin-bottom: 0.6em; break-inside: avoid }
h1 { font-size: 1.6em }
h2 { font-size: 1.4em }
h3 { font-size: 1.25em }
h4 { font-size: 1.1em }
h5 { font-size: 1em }
h6 { font-size: 0.95em }
blockquote { margin-left: 2em; margin-bottom: 0.6em }
ul, ol { margin-bottom: 0.6em }
li { margin-left: 1.5em; margin-bottom: 0.3em; display: list-item }
em, i { font-style: italic }
strong, b, th { font-weight: bold }
table { margin-top: 0.3em; margin-bottom: 0.6em }
caption { font-size: 0.9em; font-style: italic; margin-bottom: 0.3em }
tr { margin-bottom: 0.3em; break-inside: avoid }
dt { font-weight: bold }
dd { margin-left: 2em; margin-bottom: 0.3em }
pre { white-space: pre }
%s
hr { margin: 0.8em 0; border: 0; border-bottom: 1px solid %s }
`

// uaRules parses the user-agent sheet for one build. Parsing per Build keeps
// the package free of init-time state and lets the base size and host colours
// in.
func uaRules(base float32, link, rule paint.Color) []cssRule {
	// A transparent link colour is the reader's: the sheet still underlines
	// links but names no colour, which render.Typography.LinkColor supplies.
	// That keeps the reader's link colour out of the document, so switching
	// between a light and a dark theme repaints links instead of rebuilding
	// the book.
	linkRule := "a { text-decoration: underline }"
	if !link.Transparent() {
		linkRule = fmt.Sprintf("a { color: %s; text-decoration: underline }", cssColor(link))
	}
	sheet := parseCSS(fmt.Sprintf(uaCSS, base, linkRule, cssColor(rule)))
	rules := make([]cssRule, 0, len(sheet.parts))
	for _, part := range sheet.parts {
		if part.importHref == "" {
			rules = append(rules, part.rule)
		}
	}
	return rules
}

func cssColor(c paint.Color) string {
	if c.Transparent() {
		return "transparent"
	}
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

var namedColors = map[string]paint.Color{
	"black":       {R: 0x00, G: 0x00, B: 0x00, A: 0xff},
	"white":       {R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	"red":         {R: 0xff, G: 0x00, B: 0x00, A: 0xff},
	"lime":        {R: 0x00, G: 0xff, B: 0x00, A: 0xff},
	"green":       {R: 0x00, G: 0x80, B: 0x00, A: 0xff},
	"blue":        {R: 0x00, G: 0x00, B: 0xff, A: 0xff},
	"navy":        {R: 0x00, G: 0x00, B: 0x80, A: 0xff},
	"yellow":      {R: 0xff, G: 0xff, B: 0x00, A: 0xff},
	"aqua":        {R: 0x00, G: 0xff, B: 0xff, A: 0xff},
	"cyan":        {R: 0x00, G: 0xff, B: 0xff, A: 0xff},
	"fuchsia":     {R: 0xff, G: 0x00, B: 0xff, A: 0xff},
	"magenta":     {R: 0xff, G: 0x00, B: 0xff, A: 0xff},
	"silver":      {R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff},
	"gray":        {R: 0x80, G: 0x80, B: 0x80, A: 0xff},
	"grey":        {R: 0x80, G: 0x80, B: 0x80, A: 0xff},
	"maroon":      {R: 0x80, G: 0x00, B: 0x00, A: 0xff},
	"olive":       {R: 0x80, G: 0x80, B: 0x00, A: 0xff},
	"purple":      {R: 0x80, G: 0x00, B: 0x80, A: 0xff},
	"teal":        {R: 0x00, G: 0x80, B: 0x80, A: 0xff},
	"orange":      {R: 0xff, G: 0xa5, B: 0x00, A: 0xff},
	"brown":       {R: 0xa5, G: 0x2a, B: 0x2a, A: 0xff},
	"pink":        {R: 0xff, G: 0xc0, B: 0xcb, A: 0xff},
	"gold":        {R: 0xff, G: 0xd7, B: 0x00, A: 0xff},
	"darkred":     {R: 0x8b, G: 0x00, B: 0x00, A: 0xff},
	"darkblue":    {R: 0x00, G: 0x00, B: 0x8b, A: 0xff},
	"darkgreen":   {R: 0x00, G: 0x64, B: 0x00, A: 0xff},
	"darkgray":    {R: 0xa9, G: 0xa9, B: 0xa9, A: 0xff},
	"darkgrey":    {R: 0xa9, G: 0xa9, B: 0xa9, A: 0xff},
	"lightgray":   {R: 0xd3, G: 0xd3, B: 0xd3, A: 0xff},
	"lightgrey":   {R: 0xd3, G: 0xd3, B: 0xd3, A: 0xff},
	"dimgray":     {R: 0x69, G: 0x69, B: 0x69, A: 0xff},
	"dimgrey":     {R: 0x69, G: 0x69, B: 0x69, A: 0xff},
	"lightblue":   {R: 0xad, G: 0xd8, B: 0xe6, A: 0xff},
	"lightgreen":  {R: 0x90, G: 0xee, B: 0x90, A: 0xff},
	"whitesmoke":  {R: 0xf5, G: 0xf5, B: 0xf5, A: 0xff},
	"gainsboro":   {R: 0xdc, G: 0xdc, B: 0xdc, A: 0xff},
	"beige":       {R: 0xf5, G: 0xf5, B: 0xdc, A: 0xff},
	"ivory":       {R: 0xff, G: 0xff, B: 0xf0, A: 0xff},
	"transparent": {R: 0, G: 0, B: 0, A: 0},
}

// parseColor reads a CSS colour. `currentcolor` and `initial` are zero, which
// lets the stylesheet inherit the host's ink.
func parseColor(v string) (paint.Color, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return paint.Color{}, false
	}
	if c, ok := namedColors[v]; ok {
		return c, true
	}
	if v == "currentcolor" || v == "initial" || v == "inherit" {
		return paint.Color{}, true
	}
	if strings.HasPrefix(v, "#") {
		return parseHexColor(v[1:])
	}
	if strings.HasPrefix(v, "rgb") {
		open := strings.IndexByte(v, '(')
		close := strings.LastIndexByte(v, ')')
		if open < 0 || close <= open {
			return paint.Color{}, false
		}
		fields := strings.FieldsFunc(v[open+1:close], func(r rune) bool {
			return r == ',' || r == ' ' || r == '/'
		})
		if len(fields) < 3 {
			return paint.Color{}, false
		}
		c := paint.Color{A: 0xff}
		comp := []*uint8{&c.R, &c.G, &c.B}
		for i := 0; i < 3; i++ {
			n, ok := colorComponent(fields[i])
			if !ok {
				return paint.Color{}, false
			}
			*comp[i] = n
		}
		if len(fields) >= 4 {
			alpha, ok := colorAlpha(fields[3])
			if !ok {
				return paint.Color{}, false
			}
			c.A = alpha
		}
		return c, true
	}
	return paint.Color{}, false
}

func parseHexColor(h string) (paint.Color, bool) {
	switch len(h) {
	case 3:
		r, ok1 := hexByte(string([]byte{h[0], h[0]}))
		g, ok2 := hexByte(string([]byte{h[1], h[1]}))
		b, ok3 := hexByte(string([]byte{h[2], h[2]}))
		return paint.Color{R: r, G: g, B: b, A: 0xff}, ok1 && ok2 && ok3
	case 6:
		r, ok1 := hexByte(h[0:2])
		g, ok2 := hexByte(h[2:4])
		b, ok3 := hexByte(h[4:6])
		return paint.Color{R: r, G: g, B: b, A: 0xff}, ok1 && ok2 && ok3
	case 8:
		r, ok1 := hexByte(h[0:2])
		g, ok2 := hexByte(h[2:4])
		b, ok3 := hexByte(h[4:6])
		a, ok4 := hexByte(h[6:8])
		return paint.Color{R: r, G: g, B: b, A: a}, ok1 && ok2 && ok3 && ok4
	}
	return paint.Color{}, false
}

func hexByte(s string) (uint8, bool) {
	n, err := strconv.ParseUint(s, 16, 8)
	return uint8(n), err == nil
}

func colorComponent(s string) (uint8, bool) {
	if strings.HasSuffix(s, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 32)
		if err != nil {
			return 0, false
		}
		return uint8(math.Round(float64(clamp01(float32(f/100))) * 255)), true
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, false
	}
	return uint8(math.Round(float64(clamp01(float32(f/255))) * 255)), true
}

func colorAlpha(s string) (uint8, bool) {
	if strings.HasSuffix(s, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 32)
		if err != nil {
			return 0, false
		}
		return uint8(math.Round(float64(clamp01(float32(f/100))) * 255)), true
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, false
	}
	return uint8(math.Round(float64(clamp01(float32(f))) * 255)), true
}

func clamp01(f float32) float32 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
