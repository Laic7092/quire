// Package widgets holds the small pieces of chrome the reader's panels share:
// icon buttons, section headers and the empty message every list falls back to.
//
// They are thin wrappers over package ui, so the app keeps one look: a borderless
// button that shows only an icon, a muted line where a list has no rows, and a
// row that reacts to hover and to being chosen.
package widgets

import (
	"github.com/egoist/mygo/ui"
)

// IconButton is a button showing only an icon. label names it for assistive
// technology and for tests; tip is its tooltip.
func IconButton(c *ui.Context, icon *ui.SVG, label, tip string) ui.Element {
	b := ui.Button(c, "").Label(label).Padding(6).Radius(6).KeepFocus()
	if tip != "" {
		b = b.Tooltip(tip)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(18, 18)
	})
	return b
}

// GlyphButton is a toolbar glyph: no button surface, only the icon, with a
// soft wash under the pointer — the way a Mac toolbar behaves. label names it
// for assistive technology and for tests; tip is its tooltip.
func GlyphButton(c *ui.Context, icon *ui.SVG, label, tip string) ui.Element {
	return glyph(c, label, tip, func() {
		ui.Icon(c, icon).Size(17, 17)
	})
}

// TextGlyph is GlyphButton for a toolbar control that is its own label, as
// Books writes the display settings as "Aa".
func TextGlyph(c *ui.Context, text, label, tip string) ui.Element {
	return glyph(c, label, tip, func() {
		ui.Text(c, text).FontSize(13).FontWeight(600)
	})
}

// glyph builds the hoverable, borderless control both of the above share.
func glyph(c *ui.Context, label, tip string, fn func()) ui.Element {
	t := c.Theme()
	b := ui.Box(c).Padding(5, 7).Radius(6).Center().Cursor(ui.CursorPointer).
		Label(label).KeepFocus()
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if tip != "" {
		b.Tooltip(tip)
	}
	b.Children(fn)
	return b
}

// SmallButton is a compact text button for a toolbar: the room of a text button
// without the padding of a dialog's.
func SmallButton(c *ui.Context, label, tip string) ui.Element {
	b := ui.Button(c, label).Padding(5, 10).Radius(6).KeepFocus()
	if tip != "" {
		b = b.Tooltip(tip)
	}
	return b
}

// Empty is the message a panel shows where it has no rows: centered, muted, and
// a little below the middle so it does not move as rows arrive.
func Empty(c *ui.Context, message, hint string) ui.Element {
	t := c.Theme()
	return ui.Column(c).Fill().Center().Padding(24).Gap(6).Children(func() {
		ui.Text(c, message).TextColor(t.TextMuted).TextAlign(ui.Center)
		if hint != "" {
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(240)
		}
	})
}

// SectionTitle is the small uppercase-looking label above a list of rows.
func SectionTitle(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	return ui.Text(c, text).FontSize(11).Bold().TextColor(t.TextMuted).Padding(4, 2)
}

// RowBackground is the fill of a row that the pointer is over or that is
// chosen: the theme's hover colour, or a tint of the accent for the choice.
func RowBackground(c *ui.Context, hovered, chosen bool) ui.Color {
	t := c.Theme()
	switch {
	case chosen:
		return t.Accent.Alpha(0.16)
	case hovered:
		return t.SurfaceHover
	}
	return ui.Transparent
}
