package search

import (
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/locales"
	"github.com/laic7092/layx/render"
)

func testPanel() *Panel {
	return &Panel{
		Query:  "ray",
		Status: locales.SearchResults(3),
		Hits: []Hit{
			{Chapter: 0, Title: "第一章", Snippet: "hello 「ray」 of light", Offset: 2,
				Loc: render.Location{Chapter: 0, Node: 2, Offset: 2}},
			{Chapter: 1, Title: "第二章", Snippet: "another 「ray」", Offset: 9,
				Loc: render.Location{Chapter: 1, Node: 3, Offset: 9}},
		},
		PageOf: func(h Hit) (int, bool) { return h.Chapter + 1, true },
	}
}

func TestPanelShowsHitsAndJumps(t *testing.T) {
	p := testPanel()
	var jumped render.Location
	p.OnJump = func(loc render.Location) { jumped = loc }

	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 520)
	tt.Frame()

	if !tt.HasText(locales.SearchResults(3)) {
		t.Errorf("status is not shown; texts = %q", tt.Texts())
	}
	// A hit is two lines: its chapter and page, then the snippet.
	if !tt.HasText("第一章") || !tt.HasText(locales.SearchPage(2)) {
		t.Errorf("first hit is not shown; texts = %q", tt.Texts())
	}
	if !tt.HasText("hello 「ray」 of light") {
		t.Errorf("snippet is not shown; texts = %q", tt.Texts())
	}
	if err := tt.Click("hello 「ray」 of light"); err != nil {
		t.Fatal(err)
	}
	if jumped.Chapter != 0 || jumped.Node != 2 || jumped.Offset != 2 {
		t.Errorf("jumped to %+v, want the first hit's location", jumped)
	}
	if p.Selected != 0 {
		t.Errorf("selected = %d, want 0", p.Selected)
	}
}

func TestPanelEmptyState(t *testing.T) {
	p := &Panel{Query: "nothing", Status: locales.SearchNotFoundShort()}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 320)
	tt.Frame()
	if !tt.HasText(locales.SearchNotFoundShort()) || !tt.HasText(locales.SearchNoHitsHint()) {
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
	if err := tt.Click(locales.SearchFieldLabel()); err != nil {
		t.Fatal(err)
	}
	tt.Type("風")
	tt.Frame()
	if searched != "風" {
		t.Errorf("OnSearch(%q), want 風", searched)
	}
}

// longPanel is a panel with enough hits to scroll.
func longPanel(n int) *Panel {
	hits := make([]Hit, n)
	for i := range hits {
		hits[i] = Hit{
			Chapter: 0,
			Title:   "第一章",
			Snippet: "snippet " + strconv.Itoa(i),
			Offset:  i * 3,
			Loc:     render.Location{Chapter: 0, Node: render.NodeID(i + 1), Offset: i},
		}
	}
	return &Panel{Query: "old", Hits: hits, Selected: -1}
}

// A new query shows its own results from the top: the scroll offset of the
// previous query's rows means nothing in the new ones.
func TestPanelNewQueryStartsAtTop(t *testing.T) {
	p := longPanel(80)
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 320)
	tt.Frame()

	p.list.ScrollTo(40, ui.Start)
	tt.Frame()
	if first, _ := p.list.Visible(); first < 20 {
		t.Fatalf("the list did not scroll: first visible row = %d", first)
	}

	p.Query = "new"
	tt.Frame()
	if first, _ := p.list.Visible(); first != 0 {
		t.Errorf("a new query kept the old scroll position: first visible row = %d", first)
	}
}

// A row's identity is the hit it shows, not its index: two hits of one query
// are different rows even at the same place in the list.
func TestPanelRowsAreKeyedByHit(t *testing.T) {
	p := testPanel()
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 520)
	tt.Frame()
	first, second := p.list.Key(0), p.list.Key(1)
	if first == second {
		t.Fatalf("two hits share the key %v", first)
	}
	if p.list.Key(0) != first {
		t.Error("a row's key changed between frames")
	}
}

// A query with no hits says so once: its status is the empty state's title, not
// a line above it as well.
func TestPanelNoHitsStatusIsShownOnce(t *testing.T) {
	p := &Panel{Query: "nothing", Status: locales.SearchNotFound("nothing"), Selected: -1}
	tt := ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 320)
	tt.Frame()
	count := 0
	for _, text := range tt.Texts() {
		if text == locales.SearchNotFound("nothing") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the no-hits status appears %d times: %v", count, tt.Texts())
	}
	if !tt.HasText(locales.SearchNoHitsHint()) {
		t.Errorf("the no-hits hint is missing: %v", tt.Texts())
	}

	// A running search keeps its line, and the hits stay listed below it.
	p = longPanel(3)
	p.Status = locales.SearchSearching()
	tt = ui.NewTester(func(c *ui.Context) { p.View(c) }, 420, 320)
	tt.Frame()
	if !tt.HasText(locales.SearchSearching()) || !tt.HasText("snippet 0") {
		t.Errorf("a running search lost its status or its hits: %v", tt.Texts())
	}
}
