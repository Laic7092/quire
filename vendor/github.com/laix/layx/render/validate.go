package render

import "math"

// This file holds the input-contract checks shared by LayoutOptions and
// Document. Every float that reaches layout arithmetic is validated here before
// any zero-value defaulting can hide it.

// isFinite reports whether f is neither NaN nor an infinity.
//
// Layout accepts only finite floats. NaN compares false to every range check
// (x <= 0, x < min, x > max), so a validation written only in those terms lets
// it through; it then flows into page geometry and reachability tests, where it
// turns "no page" into "one page of lost content" instead of an error.
// Infinities overflow the same arithmetic in the other direction. Both are
// rejected at the API boundary with ErrInvalidOptions or ErrInvalidDocument.
func isFinite(f float32) bool {
	// Converting float32 to float64 is exact for NaN and infinities, so this
	// catches float32 overflow too (a computation can yield +Inf even though
	// no literal is infinite).
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}

// finiteOption returns a descriptive error when v is not finite. name is the
// host-facing field name; it becomes DocumentError.Detail so a host can point
// at the UI control or JSON key that produced the value.
func finiteOption(name string, v float32) error {
	if isFinite(v) {
		return nil
	}
	return docErrorf(ErrInvalidOptions, "%s must be finite, got %v", name, v)
}

// validateLayoutOptionsFinite rejects non-finite floats in o.
//
// It runs before LayoutOptions.validate's range checks and before
// withDefaults, because withDefaults treats every non-positive value as
// "unset": NaN is not positive, so defaulting would silently substitute a sane
// value (or, for the fields with no default, leave NaN in place).
func validateLayoutOptionsFinite(o LayoutOptions) error {
	checks := [...]struct {
		name string
		v    float32
	}{
		// Viewport and margins define the content box.
		{"viewport width", o.Viewport.Width},
		{"viewport height", o.Viewport.Height},
		{"margin top", o.Margins.Top},
		{"margin right", o.Margins.Right},
		{"margin bottom", o.Margins.Bottom},
		{"margin left", o.Margins.Left},
		// TileHeight slices the scroll flow; a non-finite value makes the
		// tile count unreachable.
		{"tile height", o.TileHeight},
		// Document-wide typography defaults inherited by every style.
		{"typography font size", o.Typography.FontSize},
		{"typography line height", o.Typography.LineHeight},
		{"typography letter spacing", o.Typography.LetterSpacing},
		{"typography word spacing", o.Typography.WordSpacing},
		{"typography paragraph gap", o.Typography.ParagraphGap},
	}
	for _, c := range checks {
		if err := finiteOption(c.name, c.v); err != nil {
			return err
		}
	}
	return nil
}

// validateStyle rejects non-finite floats in one style table entry.
//
// id is reported as DocumentError.Style; the detail names the field. Style is
// entered only through DocumentBuilder, so this is the only place a host can
// hand the layout a non-finite style.
//
// paint.Color channels are uint8 and therefore cannot be NaN or infinite, so
// they need no check.
func validateStyle(id StyleID, st Style) error {
	checks := [...]struct {
		name string
		v    float32
	}{
		{"font size", st.FontSize},
		{"line height", st.LineHeight},
		{"letter spacing", st.LetterSpacing},
		{"word spacing", st.WordSpacing},
		{"first line indent", st.FirstLineIndent},
		{"margin top", st.MarginTop},
		{"margin bottom", st.MarginBottom},
		{"margin left", st.MarginLeft},
		{"margin right", st.MarginRight},
		{"corner radius", st.CornerRadius},
		{"border top width", st.BorderTop.Width},
		{"border right width", st.BorderRight.Width},
		{"border bottom width", st.BorderBottom.Width},
		{"border left width", st.BorderLeft.Width},
	}
	for _, c := range checks {
		if !isFinite(c.v) {
			return styleErrorf(ErrInvalidDocument, "", InvalidNode, id, "%s must be finite, got %v", c.name, c.v)
		}
	}
	return nil
}

// validateStyles checks every entry in a document style table. Style 0 always
// exists (the builder installs a zero style), so an empty table is valid.
func validateStyles(styles []Style) error {
	for i := range styles {
		if err := validateStyle(StyleID(i), styles[i]); err != nil {
			return err
		}
	}
	return nil
}
