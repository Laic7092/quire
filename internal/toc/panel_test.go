package toc

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/locales"
)

func panelEntries() []Entry {
	return []Entry{
		{Title: "第一卷 风起", Chapter: 0, Page: 0, Pages: 2, Level: 0},
		{Title: "第一章 初见", Chapter: 1, Page: 2, Pages: 3, Level: 1, Current: true},
		{Title: "第二章 再见", Chapter: 2, Page: 5, Pages: 1, Level: 1},
		{Title: "没有页码的一章", Chapter: 3, Page: -1, Level: 1},
	}
}

func TestPanelDrawsRows(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: 1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 400)
	tt.Frame()
	for _, e := range p.Entries {
		if !tt.HasText(e.Title) {
			t.Errorf("row %q is missing", e.Title)
		}
	}
	if !tt.HasText("3–5") {
		t.Errorf("page range of the 3-page chapter is missing: %v", tt.Texts())
	}
	// The last row has no page, so it shows a title only.
	if tt.HasText("-1/0") {
		t.Error("a row without pages printed a page label")
	}
}

func TestPanelJumpOnClick(t *testing.T) {
	var jumped []Entry
	p := &Panel{Entries: panelEntries(), Selected: 0, OnJump: func(e Entry) { jumped = append(jumped, e) }}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 400)
	tt.Frame()

	if err := tt.Click("第二章 再见"); err != nil {
		t.Fatal(err)
	}
	if p.Selected != 2 {
		t.Errorf("Selected = %d after a click on row 2, want 2", p.Selected)
	}
	if len(jumped) != 1 || jumped[0].Chapter != 2 {
		t.Fatalf("jumped to rows %v, want [chapter 2]", jumped)
	}

	// A row without a page is disabled: it may be chosen, but does not jump.
	if err := tt.Click("没有页码的一章"); err != nil {
		t.Fatal(err)
	}
	if len(jumped) != 1 {
		t.Errorf("a disabled row jumped: %v", jumped)
	}
}

func TestPanelEmpty(t *testing.T) {
	p := &Panel{}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 200)
	tt.Frame()
	if !tt.HasText(locales.TocEmpty()) {
		t.Error("an empty panel drew no placeholder")
	}
	// Out-of-range selections are dropped, not kept.
	p = &Panel{Entries: panelEntries(), Selected: 99}
	tt = ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 400)
	tt.Frame()
	if p.Selected != -1 {
		t.Errorf("Selected = %d, want -1", p.Selected)
	}
}

func TestPanelSelectCurrent(t *testing.T) {
	p := &Panel{Entries: panelEntries(), Selected: -1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 320, 400)
	tt.Frame()
	if !p.SelectCurrent() {
		t.Fatal("SelectCurrent found nothing")
	}
	tt.Frame()
	if !tt.HasText("第一章 初见") {
		t.Error("the current row is not drawn")
	}
}
