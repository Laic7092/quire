package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/icons"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/locales"
)

// settingsWidth is how wide the display settings panel is, in DIPs, measured
// off Books' own "Themes & Settings" popover: 219 wide, 10 of padding, three
// theme cards a row.
const settingsWidth = 219

// settingsPanel is Apple Books' Aa: the size in a segmented row, the six papers
// as cards with their names inside them, and the rest of the settings behind
// "Customize". Every change is applied as it is made; the page follows a moment
// later, once the reader stops moving a slider.
func (a *app) settingsPanel(c *ui.Context, anchor ui.Element) {
	if !a.aaOpen {
		return
	}
	pal := a.chrome
	ui.PopoverBase(c, anchor, &a.aaOpen, func(panel ui.Element) {
		// The panel hangs under the Aa, 9 DIPs below the bar, at its centre:
		// the way Books anchors its own popover.
		panel.Width(settingsWidth).Padding(0, 10, 19, 10).Radius(18).Gap(0).
			Background(reader.UIColor(pal.Popover)).
			Border(1, reader.UIColor(pal.Line)).
			Shadow(0, 12, 34, 0, ui.RGBA(0, 0, 0, 0.36)).
			AttachTo(anchor, ui.AnchorBottom, ui.AnchorTop).Margin(9, 0, 0, 0).
			Children(func() {
				ui.Row(c).FillWidth().Height(24).Center().Children(func() {
					ui.Text(c, locales.ThemesSettings()).FontSize(11).FontWeight(600).
						TextColor(a.settingsMuted())
				})
				a.settingsDivider(c)
				ui.Box(c).Height(9)
				a.sizeRow(c)
				ui.Box(c).Height(10)
				a.themeGrid(c)
				ui.Box(c).Height(12)
				a.customRow(c)
				if a.aaMore {
					ui.Box(c).Height(10)
					a.settingsDetail(c)
				}
			})
		// The pointer under the Aa, drawn over the bar so the panel and its
		// anchor read as one control, as Books' own popover does.
		if b := anchor.Bounds(); b.W > 0 {
			x := b.X + b.W/2 - 8
			ui.Overlay(c, func() {
				ui.Icon(c, icons.TriangleUp).Size(16, 8).
					Absolute().Left(x).Top(b.Y + b.H + 2).
					TextColor(reader.UIColor(pal.Popover)).PassThrough()
			})
		}
	})
}

// settingsMuted is the caption colour on the panel, which sits on the popover
// colour rather than on the page.
func (a *app) settingsMuted() ui.Color {
	return reader.UIColor(a.chrome.Muted)
}

// settingsInk is the ink on the popover's raised controls: white-ish on the
// dark papers, nearly black on the light ones.
func (a *app) settingsInk() ui.Color {
	return reader.UIColor(a.chrome.OnToolbar)
}

// sizeRow is the segmented control at the top of the panel: a smaller size and
// a larger one in one capsule, then the appearance control in its own, as
// Books draws it.
func (a *app) sizeRow(c *ui.Context) {
	pal := a.chrome
	ui.Row(c).FillWidth().Height(38).Gap(6).Children(func() {
		group := ui.Row(c).Width(130).Height(38).Radius(19).
			Background(reader.UIColor(pal.Raised)).AlignItems(ui.Center)
		group.Children(func() {
			a.sizeCell(c, 13, locales.SettingsSmallerText(), func() { a.changeFont(-1) })
			divider := ui.Box(c).Width(1).Height(20).Background(reader.UIColor(pal.OnToolbar).Alpha(0.30))
			divider.Margin(0, 0)
			a.sizeCell(c, 21, locales.SettingsLargerText(), func() { a.changeFont(1) })
		})
		appearance := ui.Box(c).Width(63).Height(38).Radius(19).Center().
			Background(reader.UIColor(pal.Raised)).Cursor(ui.CursorPointer).
			Label(locales.SettingsAppearance())
		appearance.OnClick(func() { a.aaMore = !a.aaMore })
		appearance.Children(func() {
			ui.Icon(c, icons.SunHorizon).Size(17, 17).TextColor(a.settingsInk())
		})
	})
}

// sizeCell is one half of the size control: a letter at the size it stands for.
func (a *app) sizeCell(c *ui.Context, size float32, label string, onClick func()) {
	pal := a.chrome
	cell := ui.Box(c).Grow(1).FillHeight().Center().Cursor(ui.CursorPointer).Label(label)
	if cell.Hovered() {
		cell.Background(reader.UIColor(pal.Hover))
	}
	cell.OnClick(onClick)
	cell.Children(func() {
		ui.Text(c, "A").FontSize(size).TextColor(a.settingsInk())
	})
}

// themeLabel is the name of a paper in the language the app shows. The papers
// are the reader's own, so the reader names them; the catalogs say the names.
func themeLabel(t reader.Theme) string {
	switch t {
	case reader.ThemeQuiet:
		return locales.ThemeQuiet()
	case reader.ThemePaper:
		return locales.ThemePaper()
	case reader.ThemeBold:
		return locales.ThemeBold()
	case reader.ThemeCalm:
		return locales.ThemeCalm()
	case reader.ThemeFocus:
		return locales.ThemeFocus()
	}
	return locales.ThemeOriginal()
}

// themeGrid is the papers: three cards a row, each printed in its own colours,
// with its "Aa" and its name inside, and a ring around the one being read.
func (a *app) themeGrid(c *ui.Context) {
	ui.Grid(c).Columns(3).Gap(7).Children(func() {
		for _, theme := range reader.ThemeOrder {
			a.themeCard(c, theme)
		}
	})
}

func (a *app) themeCard(c *ui.Context, theme reader.Theme) {
	pal := reader.ForTheme(theme)
	chosen := a.paperTheme(c.Theme().Dark) == theme
	card := ui.Column(c).Height(55).Radius(12).Background(reader.UIColor(pal.Paper)).
		Center().Cursor(ui.CursorPointer).Label(themeLabel(theme))
	switch {
	case chosen:
		// The ring is the paper's own ink, so it reads on white and on
		// graphite alike, one step thicker than the card's edge.
		card.Border(1, reader.UIColor(pal.Ink))
	case theme.Dark():
		// A dark card needs an edge of its own to sit on the popover.
		card.Border(1, reader.UIColor(pal.OnToolbar).Alpha(0.22))
	}
	card.OnClick(func() { a.setTheme(theme) })
	card.Children(func() {
		ui.Column(c).Gap(0).Center().Children(func() {
			ui.Text(c, "Aa").FontSize(24).TextColor(reader.UIColor(pal.Ink))
			ui.Text(c, themeLabel(theme)).FontSize(10).TextColor(reader.UIColor(pal.Ink))
		})
	})
}

// settingsDivider is the hairline under the panel's title, and between the
// settings "Customize" opens.
func (a *app) settingsDivider(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Background(reader.UIColor(a.chrome.Line))
}

// customRow is the foot of the panel: the way to the sizes, the leading, the
// face and the margins, as Books puts "Customize" under its themes.
func (a *app) customRow(c *ui.Context) {
	pal := a.chrome
	row := ui.Row(c).FillWidth().Height(36).Center().Gap(7).Radius(12).
		Background(reader.UIColor(pal.Raised)).Cursor(ui.CursorPointer).Label(locales.SettingsCustomize())
	row.OnClick(func() { a.aaMore = !a.aaMore })
	row.Children(func() {
		ui.Icon(c, icons.Gear).Size(13, 13).TextColor(a.settingsInk())
		ui.Text(c, locales.SettingsCustomize()).FontSize(12.5).FontWeight(600).TextColor(a.settingsInk())
	})
}

// settingsDetail is what "Customize" opens: the size, the leading and the
// margins as sliders, the face and the alignment as pairs, and two pages or one.
func (a *app) settingsDetail(c *ui.Context) {
	a.sliderRow(c, locales.SettingsSize(), fmt.Sprintf("%.0f", a.opts.FontSize), &a.uiSize,
		reader.MinFontSize, reader.MaxFontSize, func() {
			a.setOptions(withFontSize(a.opts, round(float32(a.uiSize), 1)))
		})
	a.sliderRow(c, locales.SettingsLeading(), fmt.Sprintf("%.2f", a.opts.LineHeight), &a.uiLeading,
		reader.MinLeading, reader.MaxLeading, func() {
			o := a.opts
			o.LineHeight = round(float32(a.uiLeading), 0.05)
			a.setOptions(o)
		})
	a.sliderRow(c, locales.SettingsMargins(), fmt.Sprintf("%.0f", a.opts.Margin), &a.uiMargin,
		reader.MinMargin, reader.MaxMargin, func() {
			o := a.opts
			o.Margin = round(float32(a.uiMargin), 1)
			a.setOptions(o)
		})
	face := 0
	if a.opts.Font == reader.FontSans {
		face = 1
	}
	a.choose(c, locales.SettingsTypeface(), face, func(choice int) {
		o := a.opts
		o.Font = reader.FontSerif
		if choice == 1 {
			o.Font = reader.FontSans
		}
		a.setOptions(o)
	}, locales.SettingsSerif(), locales.SettingsSans())
	align := 0
	if !a.opts.Justify {
		align = 1
	}
	a.choose(c, locales.SettingsAlignment(), align, func(choice int) {
		o := a.opts
		o.Justify = choice == 0
		a.setOptions(o)
	}, locales.SettingsJustified(), locales.SettingsLeft())
	// The switch writes the setting itself, so the comparison setOptions makes
	// would see no change: save it and lay the sheet out again here.
	// The row is the control, as Books lets the whole line be pressed; the
	// pill at its right shows the state.
	a.switchRow(c, locales.SettingsTwoPages(), a.opts.Spread, func(on bool) {
		a.opts.Spread = on
		a.savePrefs()
		a.layW, a.layH = 0, 0
	})
	// The turn is a pair like the face and the alignment: sliding pages, or one
	// page fading into the next. It changes no layout, so it is saved as it is
	// picked and the next turn follows it.
	turn := 0
	if a.opts.Turn == reader.TurnFade {
		turn = 1
	}
	a.choose(c, locales.SettingsTurn(), turn, func(choice int) {
		t := reader.TurnSlide
		if choice == 1 {
			t = reader.TurnFade
		}
		a.setTurn(t)
	}, locales.SettingsTurnSlide(), locales.SettingsTurnFade())
	// Scrolling reads the book as one column. It is a layout mode, so it goes
	// through setOptions like the type settings: the sheet follows once the
	// reader stops pressing.
	a.switchRow(c, locales.SettingsScrolling(), a.opts.Scroll, func(on bool) {
		o := a.opts
		o.Scroll = on
		a.setOptions(o)
	})
	if ui.Button(c, locales.SettingsReset()).Clicked() {
		a.setOptions(reader.DefaultOptions())
		a.syncSettingsFields()
	}
}

// switchRow is a setting that is on or off, drawn as the line Books has: the name
// at the left and a pill at the right, the whole row pressable.
func (a *app) switchRow(c *ui.Context, label string, on bool, onSet func(bool)) {
	pal := a.chrome
	row := ui.Row(c).FillWidth().Height(24).AlignItems(ui.Center).Gap(6).
		Radius(6).Cursor(ui.CursorPointer).Label(label)
	if row.Hovered() {
		row.Background(reader.UIColor(pal.Hover))
	}
	row.OnClick(func() { onSet(!on) })
	row.Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(a.settingsMuted())
		ui.Spacer(c)
		track := ui.Box(c).Width(32).Height(18).Radius(9).Center()
		track.Background(reader.UIColor(pal.Line))
		if on {
			track.Background(reader.UIColor(pal.Accent))
		}
		track.Children(func() {
			knob := ui.Box(c).Width(14).Height(14).Radius(7)
			knob.Background(reader.UIColor(pal.Popover))
			if on {
				knob.MarginStart(14)
			} else {
				knob.MarginEnd(14)
			}
		})
	})
}

// choose is a pair of settings that exclude each other, drawn as the panel's own
// segmented control rather than a button: the chosen half is filled, as Books
// fills the size row at the top. It takes *choice, and reports whether it moved.
func (a *app) choose(c *ui.Context, label string, chosen int, onPick func(int), labels ...string) {
	pal := a.chrome
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(a.settingsMuted())
		ui.Row(c).FillWidth().Height(25).Radius(7).Padding(2).Gap(2).
			Background(reader.UIColor(pal.Raised)).Label(label).Children(func() {
			for i, name := range labels {
				i, name := i, name
				cell := ui.Box(c).Grow(1).FillHeight().Radius(6).Center().
					Cursor(ui.CursorPointer).Label(name)
				ink := a.settingsMuted()
				if i == chosen {
					cell.Background(reader.UIColor(pal.Hover))
					ink = reader.UIColor(pal.Text)
				}
				cell.Children(func() {
					ui.Text(c, name).FontSize(11).TextColor(ink)
				})
				cell.OnClick(func() {
					if chosen != i {
						onPick(i)
					}
				})
			}
		})
	})
}

// round is v snapped to the nearest multiple of step, so a slider that reports
// tenths does not leave a size of 18.000001 behind.
func round(v, step float32) float32 {
	if step <= 0 {
		return v
	}
	return float32(int(v/step+0.5)) * step
}

// withFontSize is o with another size, for the size control and the sliders.
func withFontSize(o reader.Options, size float32) reader.Options {
	o.FontSize = size
	return o
}

// syncSettingsFields copies the settings into the sliders' own values. It runs
// when the panel opens and after a change made elsewhere, never every frame: the
// sliders write their value as the reader drags them.
func (a *app) syncSettingsFields() {
	a.uiSize = float64(a.opts.FontSize)
	a.uiLeading = float64(a.opts.LineHeight)
	a.uiMargin = float64(a.opts.Margin)
}

// sliderRow is one labelled slider of the panel: its name, its value, and the
// slider itself, which calls changed when the reader moves it.
func (a *app) sliderRow(c *ui.Context, label, value string, v *float64, lo, hi float64, changed func()) {
	muted := a.settingsMuted()
	ui.Column(c).Gap(1).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(11).TextColor(muted).Grow(1)
			ui.Text(c, value).FontSize(11).TextColor(muted)
		})
		slider := ui.Slider(c, v, lo, hi).Label(label)
		if slider.Changed() {
			changed()
		}
	})
}
