package search

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func testPanel() *Panel {
	return &Panel{
		Query:  "ray",
		Status: "3 条结果",
		Hits: []Hit{
			{Chapter: 0, Title: "第一章", Snippet: "hello 「ray」 of light", Offset: 2},
			{Chapter: 1, Title: "第二章", Snippet: "another 「ray」", Offset: 9},
		},
		PageOf: func(h Hit) (int, bool) { return h.Chapter + 1, true },
	}
}

func TestPanelShowsHitsAndJumps(t *testing.T) {
	p := testPanel()
	jumped := -1
	p.OnJump = func(page int) { jumped = page }

	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 520)
	tt.Frame()

	if !tt.HasText("3 条结果") {
		t.Errorf("status is not shown; texts = %q", tt.Texts())
	}
	// A hit is two lines: its chapter and page, then the snippet.
	if !tt.HasText("第一章") || !tt.HasText("第 2 页") {
		t.Errorf("first hit is not shown; texts = %q", tt.Texts())
	}
	if !tt.HasText("hello 「ray」 of light") {
		t.Errorf("snippet is not shown; texts = %q", tt.Texts())
	}
	if err := tt.Click("hello 「ray」 of light"); err != nil {
		t.Fatal(err)
	}
	if jumped != 1 {
		t.Errorf("jumped to %d, want 1", jumped)
	}
	if p.Selected != 0 {
		t.Errorf("selected = %d, want 0", p.Selected)
	}
}

func TestPanelEmptyState(t *testing.T) {
	p := &Panel{Query: "nothing", Status: "未找到"}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 320)
	tt.Frame()
	if !tt.HasText("未找到") || !tt.HasText("换个词试试，或先用空格分开更短的词") {
		t.Errorf("empty status is not shown; texts = %q", tt.Texts())
	}
}

func TestPanelSearchCallback(t *testing.T) {
	p := testPanel()
	p.Query = ""
	searched := ""
	p.OnSearch = func(q string) { searched = q }

	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 520)
	tt.Frame()
	if err := tt.Click("全文搜索"); err != nil {
		t.Fatal(err)
	}
	tt.Type("風")
	tt.Frame()
	if searched != "風" {
		t.Errorf("OnSearch(%q), want 風", searched)
	}
}
