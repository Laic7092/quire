package reader

import (
	"context"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// selectionBook is a two-chapter book whose text wraps a few times on a narrow
// page, so a drag crosses lines and a cross-chapter selection has both ends.
func selectionBook(t *testing.T) (*render.Document, *Fonts) {
	t.Helper()
	b := &book.Book{
		Title: "selection",
		Chapters: []book.Chapter{
			{Title: "一", XHTML: []byte("<html><body><p>The quick brown fox jumps over " +
				"the lazy dog, again and again, until the line wraps several " +
				"times on a narrow page.</p></body></html>")},
			{Title: "二", XHTML: []byte("<html><body><p>Second chapter text, for " +
				"cross chapter selections.</p></body></html>")},
		},
	}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc, fonts
}

// firstText returns the first non-empty text node of a chapter and its text.
func firstText(t *testing.T, ch render.Chapter) (render.NodeID, string) {
	t.Helper()
	for i := range ch.Nodes {
		if ch.Nodes[i].Kind == render.NodeText && ch.Nodes[i].Text != "" {
			return render.NodeID(i), ch.Nodes[i].Text
		}
	}
	t.Fatal("chapter has no text node")
	return 0, ""
}

// A drag across the page covers the lines it went over, as page-local
// rectangles, and reads back the text under them.
func TestSelectionCoversADrag(t *testing.T) {
	doc, fonts := selectionBook(t)
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}
	from, ok := page.HitTest(paint.Point{X: Margin + 2, Y: Margin + 6})
	if !ok {
		t.Fatal("hit test found nothing at the start of page 0")
	}
	to, ok := page.HitTest(paint.Point{X: 300, Y: Margin + 120})
	if !ok {
		t.Fatal("hit test found nothing lower on page 0")
	}

	var sel Selection
	sel.Start(from)
	sel.Extend(to)
	quads := sel.Quads(layout)
	if len(quads) == 0 {
		t.Fatal("the drag selected no rectangles")
	}
	for _, q := range quads {
		if q.Page != 0 {
			t.Errorf("quad on page %d, want page 0", q.Page)
		}
		if q.Rect.Width <= 0 || q.Rect.Height <= 0 {
			t.Errorf("empty quad: %+v", q.Rect)
		}
	}

	text := sel.Text(doc)
	if strings.TrimSpace(text) == "" {
		t.Fatal("the selection holds no text")
	}
	if strings.Contains(text, "\n\n") {
		t.Errorf("a one-chapter selection was joined as chapters: %q", text)
	}
	if !strings.Contains(docChapterText(t, doc, 0), strings.TrimSpace(text)) {
		t.Errorf("selected %q, which is not in chapter 0", text)
	}

	// A backwards drag selects the same text.
	var back Selection
	back.Start(to)
	back.Extend(from)
	if got := back.Text(doc); got != text {
		t.Errorf("backwards drag reads %q, want %q", got, text)
	}
}

// The endpoints may be in different chapters; the copied text then has a blank
// line between them, the way paragraphs are separated.
func TestSelectionTextCrossesChapters(t *testing.T) {
	doc, fonts := selectionBook(t)
	if _, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18}); err != nil {
		t.Fatal(err)
	}
	ch0, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("no chapter 0")
	}
	ch1, ok := doc.Chapter(1)
	if !ok {
		t.Fatal("no chapter 1")
	}
	first, _ := firstText(t, ch0)
	second, secondText := firstText(t, ch1)

	var sel Selection
	sel.Start(render.Location{Chapter: 0, Node: first})
	sel.Extend(render.Location{Chapter: 1, Node: second, Offset: 6})

	text := sel.Text(doc)
	parts := strings.Split(text, "\n\n")
	if len(parts) != 2 {
		t.Fatalf("selection reads %q, want two chapters", text)
	}
	if !strings.HasSuffix(parts[0], docChapterText(t, doc, 0)) {
		t.Errorf("first part %q does not end chapter 0", parts[0])
	}
	if want := string([]rune(secondText)[:6]); parts[1] != want {
		t.Errorf("second part %q, want %q", parts[1], want)
	}
}

// A press without a drag selects nothing, and a location past the end of a node
// clamps to it rather than reading into the next one.
func TestSelectionClampsToItsNodes(t *testing.T) {
	doc, fonts := selectionBook(t)
	if _, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18}); err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("no chapter 0")
	}
	node, text := firstText(t, ch)

	var click Selection
	click.Start(render.Location{Chapter: 0, Node: node, Offset: 5})
	if quads := click.Quads(nil); quads != nil {
		t.Errorf("a click produced %d quads without a layout", len(quads))
	}
	if got := click.Text(doc); got != "" {
		t.Errorf("a click reads %q", got)
	}

	var sel Selection
	sel.Start(render.Location{Chapter: 0, Node: node})
	sel.Extend(render.Location{Chapter: 0, Node: node, Offset: len([]rune(text)) + 50})
	if got := sel.Text(doc); got != text {
		t.Errorf("selection past the node end reads %q, want the whole node %q", got, text)
	}

	sel.Clear()
	if !sel.Empty() {
		t.Error("the selection survived Clear")
	}
	if got := sel.Text(doc); got != "" {
		t.Errorf("a cleared selection reads %q", got)
	}
}

// docChapterText is the chapter's text as the reader copies it: its text nodes
// in reading order.
func docChapterText(t *testing.T, doc *render.Document, i int) string {
	t.Helper()
	ch, ok := doc.Chapter(i)
	if !ok {
		t.Fatalf("no chapter %d", i)
	}
	var b strings.Builder
	stack := []render.NodeID{ch.Root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		n := &ch.Nodes[id]
		if n.Kind == render.NodeText {
			b.WriteString(n.Text)
			continue
		}
		for j := len(n.Children) - 1; j >= 0; j-- {
			stack = append(stack, n.Children[j])
		}
	}
	return b.String()
}
