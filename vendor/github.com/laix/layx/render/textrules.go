package render

// TextRules collects the document-wide policies that change line breaking or
// pagination. It is deliberately a plain struct of switches instead of a set of
// scattered fields: everything in here changes break or page results, so the
// layout cache hashes the whole value at once (see chapterBreakKey). A policy
// added here can therefore never be forgotten in a key.
//
// The zero value is the spec default: optional typographic policies are off.
// Nothing in this struct is correct by accident; hosts opt in.
type TextRules struct {
	// Autospacing inserts a small gap between an ideograph and a Latin letter
	// or digit, following CLREQ's recommendation. It is not a CSS initial
	// value, so it is off by default.
	Autospacing bool
	// PunctuationCompression trims half an em from the blank side of adjacent
	// fullwidth punctuation, following CSS Text L4 text-spacing-trim: normal.
	// It changes line breaking.
	PunctuationCompression bool
	// HangingPunctuation lets a closing punctuation mark that falls at the end
	// of a line hang into the right margin by up to half its advance, following
	// CSS Text L4 hanging-punctuation: allow-end. It changes line breaking.
	HangingPunctuation bool
	// Widows and Orphans are the minimum number of lines of a paragraph left
	// at the top and bottom of a page. Their zero value means "unset", not
	// "off": the CSS initial value is 2, so an unset field pads up to 2 in
	// withDefaults. CSS requires a positive integer, so 1 is how a host asks
	// for pages to be filled without the protection.
	Widows  int
	Orphans int
}

// withDefaults lifts unset widows and orphans to the CSS initial value. Zero
// is not a legal CSS value, so a zero field cannot mean "no protection": use 1
// for that. Keeping the padding here rather than in each caller means the
// paginator, the cache key and the tests all see the same numbers.
func (r TextRules) withDefaults() TextRules {
	if r.Widows <= 0 {
		r.Widows = 2
	}
	if r.Orphans <= 0 {
		r.Orphans = 2
	}
	return r
}
