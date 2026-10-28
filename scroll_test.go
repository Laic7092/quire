package main

import (
	"context"
	"fmt"
	"image"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
)

// newScrollWindow is newWindow with a scrolling column: the sample book laid out
// with render.ModeScroll at the sheet the reader measures, and the app told the
// column is what is on screen.
func newScrollWindow(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	return newScrollWindowView(t, nil)
}

// newScrollWindowView is newScrollWindow whose view wrap wraps, for a test that
// counts the passes the window builds or looks at what each one draws.
func newScrollWindowView(t *testing.T, wrap func(*ui.Context, func(*ui.Context))) (*app, *ui.Tester) {
	t.Helper()
	a := newWindow(t)
	a.opts.Scroll = true
	view := a.view
	if wrap != nil {
		view = func(c *ui.Context) { wrap(c, a.view) }
	}
	tt := ui.NewTester(view, 1000, 760)
	tt.Frame()

	// A scrolling book has one column, so the sheet is one maximum page wide
	// and as tall as the area the frame measured between the bars.
	layout, err := reader.BuildLayout(context.Background(), a.doc, a.fonts,
		pageMaxWidth, a.boxH, reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	if layout.ContentHeight() <= a.boxH {
		t.Fatalf("the sample book is only %.0f tall; it does not scroll", layout.ContentHeight())
	}
	a.layout = layout
	a.layScroll = true
	a.tileH = a.boxH
	a.page = 0
	a.scrollY = 0
	a.scrollChapter = -1
	a.clearSelection()
	if a.pan != nil {
		if loc, ok := a.currentLoc(); ok {
			a.pan.RefreshAt(a.book, layout, loc)
		}
	}
	tt.Frame()
	return a, tt
}

// sheetRect is the page box the reader draws into, as the frame laid it out.
func sheetRect(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	r, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("no sheet in the window: %q", tt.Texts())
	}
	return r
}

// The scrolling column draws, moves with the wheel and the keys, and reports
// its place as a percentage instead of a page number.
func TestScrollModeScrollsTheColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	if !tt.HasText("0%") {
		t.Fatalf("the folio starts at %q", tt.Texts())
	}
	if tt.HasText("1 of ") {
		t.Errorf("a scroll layout printed a page number: %q", tt.Texts())
	}

	r := sheetRect(t, tt)
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Wheel(x, y, 0, 240)
	tt.Frame()
	if a.scrollY <= 0 {
		t.Fatalf("the wheel did not move the column: scrollY = %.0f", a.scrollY)
	}
	if got, want := a.pageLabel(), tt.Texts(); !contains(want, got) {
		t.Errorf("the folio does not say %q: %q", got, want)
	}

	// A click on the text is not a page turn, and does not move the column: a
	// scrolling reader clicks to place the caret, not to skip a screen.
	before := a.scrollY
	tt.ClickAt(x, y)
	tt.Frame()
	if a.scrollY != before {
		t.Errorf("a click scrolled from %.0f to %.0f", before, a.scrollY)
	}

	// Page keys move a screenful, the arrows a few lines, Home and End go to
	// the ends.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.scrollY <= before {
		t.Errorf("the right arrow did not scroll: %.0f", a.scrollY)
	}
	step := a.scrollY
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.scrollY <= step {
		t.Errorf("the down arrow did not scroll: %.0f", a.scrollY)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.scrollY != a.maxScroll() {
		t.Errorf("End left the column at %.0f of %.0f", a.scrollY, a.maxScroll())
	}
	if !tt.HasText("100%") {
		t.Errorf("the end of the book is not 100%%: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if a.scrollY != 0 {
		t.Errorf("Home left the column at %.0f", a.scrollY)
	}
	if !tt.HasText("0%") {
		t.Errorf("the top of the book is not 0%%: %q", tt.Texts())
	}
}

// Dragging in the column selects the lines it crosses, and the rectangles are
// content rectangles with no page of their own.
func TestScrollModeSelectsText(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)
	x := r.X + r.W/2

	tt.Press(x, r.Y+80)
	tt.Move(x, r.Y+220)
	tt.Release(x, r.Y+220)
	tt.Frame()

	if len(a.selQuads) == 0 {
		t.Fatalf("the drag selected nothing: %q", tt.Texts())
	}
	for _, q := range a.selQuads {
		if q.Page != -1 {
			t.Errorf("a quad carries page %d, want -1 in scroll mode", q.Page)
		}
		if q.Content.Empty() {
			t.Error("a quad has no content rectangle")
		}
	}
	if text := a.selectedText(); text == "" {
		t.Error("nothing was selected")
	}
	if !tt.HasText(locales.SelectionSelected()) {
		t.Errorf("the folio does not report the selection: %q", tt.Texts())
	}

	// Scrolling keeps the selection: its rectangles are content coordinates,
	// so they move with the text rather than being dropped.
	quads := len(a.selQuads)
	before := a.scrollY
	tt.Wheel(x, r.Y+r.H/2, 0, 120)
	tt.Frame()
	if a.scrollY == before {
		t.Fatal("the wheel did not scroll")
	}
	if len(a.selQuads) != quads {
		t.Errorf("scrolling dropped the selection: %d quads, had %d", len(a.selQuads), quads)
	}

	// A jump from the table of contents moves the column to that chapter.
	if a.pan == nil || len(a.pan.Toc.Entries) < 2 {
		t.Fatalf("no second chapter to jump to")
	}
	e := a.pan.Toc.Entries[1]
	if !e.HasLoc {
		t.Fatal("the second chapter has no location")
	}
	top, ok := a.layout.OffsetOf(e.Loc)
	if !ok {
		t.Fatal("the chapter's location does not resolve")
	}
	a.gotoLoc(e.Loc)
	tt.Frame()
	if a.scrollY != clampf(top, 0, a.maxScroll()) {
		t.Errorf("jumped to %.0f, want the chapter top %.0f", a.scrollY, top)
	}
	if a.chapterNumber() != e.Chapter+1 {
		t.Errorf("chapter number %d after jumping to %d", a.chapterNumber(), e.Chapter+1)
	}
}

// A note marks its passage in the scrolling column too. The veil is a content
// rectangle, so it is painted where the text is and travels with the column
// rather than staying on the screen.
func TestScrollModePaintsNoteMarks(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)
	x := r.X + r.W/2

	tt.Press(x, r.Y+80)
	tt.Move(x, r.Y+220)
	tt.Release(x, r.Y+220)
	tt.Frame()
	if len(a.selQuads) == 0 {
		t.Fatal("the drag selected nothing")
	}
	a.addNoteFromSelection()
	if len(a.pan.Notes.Entries) != 1 {
		t.Fatalf("%d notes after adding one", len(a.pan.Notes.Entries))
	}
	a.pan.Show(appstate.SidebarNone)
	tt.Frame()
	if len(a.noteQuads) == 0 {
		t.Fatal("the note has no rectangles")
	}

	// A point inside the first passage rectangle, in the sheet's own terms.
	q := a.noteQuads[0]
	px := r.X + q.Content.X + q.Content.Width/2
	py := r.Y + scrollInset + q.Content.Y - a.scrollY + q.Content.Height/2
	marked := tt.Image().RGBAAt(int(px), int(py))
	// The same frame without the veil: the pixel is the paper, or the ink of the
	// line under it, so the note is what painted there.
	noteQuads := a.noteQuads
	a.noteQuads = nil
	tt.Frame()
	if plain := tt.Image().RGBAAt(int(px), int(py)); plain == marked {
		t.Errorf("the note paints nothing at (%d, %d): %v", int(px), int(py), marked)
	}

	// The veil follows the text: after a scroll it is over the same passage at
	// the passage's new place, with the same colour it had before.
	a.noteQuads = noteQuads
	a.scrollByNow(60)
	tt.Frame()
	py = r.Y + scrollInset + q.Content.Y - a.scrollY + q.Content.Height/2
	if got := tt.Image().RGBAAt(int(px), int(py)); got != marked {
		t.Errorf("the note does not follow the column: %v where it stands, want %v", got, marked)
	}
}

// Switching between pages and scrolling keeps the place: the option change
// remembers where the reader was for the layout that replaces this one.
func TestScrollModeKeepsThePlaceAcrossModes(t *testing.T) {
	a, _ := newScrollWindow(t)
	a.scrollTo(a.maxScroll() / 3)
	loc, ok := a.currentLoc()
	if !ok {
		t.Fatal("no reading location")
	}
	before, ok := a.layout.OffsetOf(loc)
	if !ok {
		t.Fatal("the reading location does not resolve")
	}

	o := a.opts
	o.Scroll = false
	a.setOptions(o)
	if !a.jumpAt {
		t.Fatal("the mode change forgot the reading position")
	}
	if a.jumpLoc.Chapter != loc.Chapter {
		t.Errorf("remembered chapter %d, was %d", a.jumpLoc.Chapter, loc.Chapter)
	}
	if after, ok := a.layout.OffsetOf(a.jumpLoc); !ok || after != before {
		t.Errorf("remembered offset %.0f, was at %.0f", after, before)
	}
	if a.opts.Scroll {
		t.Error("the scroll option is still on after switching to pages")
	}
}

// contains reports whether any of texts has want.
func contains(texts []string, want string) bool {
	for _, s := range texts {
		if s == want {
			return true
		}
	}
	return false
}

// The scroll layout's tiles start where the flow does: the first one is at
// content 0 and the last one covers the end of the book.
func TestScrollTilesCoverTheFlow(t *testing.T) {
	a, _ := newScrollWindow(t)
	layout := a.layout
	if n := layout.TileCount(); n < 2 {
		t.Fatalf("tile count = %d", n)
	}
	if first := layout.Tile(0); first == nil || first.Top() != 0 {
		t.Fatalf("first tile top is not 0")
	}
	last := layout.Tile(layout.TileCount() - 1)
	if last == nil {
		t.Fatal("the last tile is nil")
	}
	if last.Top()+last.Height() < layout.ContentHeight()-1 {
		t.Errorf("the last tile ends at %.0f, the book at %.0f",
			last.Top()+last.Height(), layout.ContentHeight())
	}
	// The renderer numbers pages in a paged layout only.
	if p := layout.Page(0); p != nil {
		t.Error("a scroll layout handed out a page")
	}
}

// The display settings carry the mode switch: the switch turns scrolling off
// again, and the setting is what the next layout is built from.
func TestScrollSwitchInTheDisplaySettings(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.aaOpen, a.aaMore = true, true
	a.syncSettingsFields()
	tt.Frame()
	if !tt.HasText(locales.SettingsScrolling()) {
		t.Fatalf("the display settings have no scrolling switch: %q", tt.Texts())
	}
	if err := tt.Click(locales.SettingsScrolling()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.opts.Scroll {
		t.Error("the scrolling switch did not turn scrolling off")
	}
	if !a.jumpAt {
		t.Error("turning scrolling off forgot the reading position")
	}
}

// A wheel's notches glide the column to their target instead of jumping: the
// distance is stepped from the clock, the notches on the way add to it, and the
// column lands where they asked. Every notch is owed in full, however many
// arrive in one frame, so a spin of the wheel travels as far as it was spun.
func TestScrollGlidesToTheWheelTarget(t *testing.T) {
	a, _ := newScrollWindow(t)
	if a.maxScroll() < 700 {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	a.scrollBy(100)
	a.scrollBy(100)
	if a.scrollGoal != 200 {
		t.Fatalf("two notches in one frame sent the column to %.0f, want both of their 200", a.scrollGoal)
	}
	if !a.gliding() {
		t.Fatal("the column did not start gliding")
	}
	if a.scrollY != 0 {
		t.Errorf("an ask moved the column before a frame did: %.0f", a.scrollY)
	}

	// The frames cover the distance, a share of what is left each one, come up to
	// the speed the debt implies and then ease into the place. The first frame is
	// the longest, the last the shortest, and it lands.
	const frame = 7 * time.Millisecond
	var moves []float32
	for i := 0; i < 80 && a.gliding(); i++ {
		at = at.Add(frame)
		before := a.scrollY
		a.stepScroll()
		moves = append(moves, a.scrollY-before)
	}
	if a.gliding() {
		t.Fatalf("the column never landed: it owes %.1f", a.scrollGoal-a.scrollY)
	}
	if a.scrollY != 200 {
		t.Errorf("landed at %.1f, want both notches' 200", a.scrollY)
	}
	if len(moves) < 3 {
		t.Fatalf("the column covered 200 DIPs in %d frames", len(moves))
	}
	if moves[0] <= moves[len(moves)-1] {
		t.Errorf("the column does not ease in: it moved %.1f then %.1f", moves[0], moves[len(moves)-1])
	}
	if len(moves) > 60 {
		t.Errorf("the column took %d frames (%s) to cover 100 DIPs", len(moves), time.Duration(len(moves))*frame)
	}

	// A place is arrived at, not travelled: a table of contents row, a search hit
	// or Home lands the column in the frame it was asked for, however far away it
	// is, instead of gliding there at the wheel's pace.
	a.scrollTo(a.maxScroll())
	if a.scrollY != a.maxScroll() {
		t.Errorf("a jump left the column at %.1f of %.1f", a.scrollY, a.maxScroll())
	}
	if a.gliding() {
		t.Errorf("a jump glided: it owes %.1f after the frame it was asked in", a.scrollGoal-a.scrollY)
	}
}

// A touchpad's deltas are the movement itself: they land in the frame they
// arrive in, and stack from where the column is drawn, so the text never trails
// the hand. A wheel's notch still glides to its target.
func TestScrollPreciseDeltasLandAtOnce(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)
	x, y := r.X+r.W/2, r.Y+r.H/2
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	tt.Scroll(x, y, 0, 40)
	if a.gliding() {
		t.Fatalf("a precise delta started a glide, to %.0f", a.scrollGoal)
	}
	if a.scrollY != 40 {
		t.Fatalf("a precise delta of 40 put the column at %.2f, want 40", a.scrollY)
	}
	tt.Scroll(x, y, 0, 12.5)
	if a.scrollY != 52.5 {
		t.Fatalf("a second delta stacked to %.2f, want 52.5", a.scrollY)
	}
	// Nothing is on its way, so the frames after the gesture do not move it.
	at = at.Add(50 * time.Millisecond)
	tt.Frame()
	if a.scrollY != 52.5 {
		t.Errorf("the column drifted to %.2f after the gesture", a.scrollY)
	}
}

// A wheel's notch is a distance asked for, and a stream of them is followed at
// the rate it comes at: no ask is dropped to slow it down, and none piles up
// into a sprint after the release.
func TestScrollAsksCruiseAtTheirRate(t *testing.T) {
	a, _ := newScrollWindow(t)
	step := 3 * a.opts.FontSize * a.opts.LineHeight
	if a.maxScroll() < 200*step {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	// A hold: asks every 40 ms — a key's step and a wheel's notch by turns, which
	// is the same ask — and frames every 7 ms, as a 144 Hz display draws them.
	const every, frame = 40 * time.Millisecond, 7 * time.Millisecond
	next := at.Add(every)
	asked := 0
	var moves []float32
	for i := 0; i < 120; i++ {
		at = at.Add(frame)
		if !at.Before(next) {
			a.scrollBy(step)
			asked++
			next = next.Add(every)
		}
		before := a.scrollY
		a.stepScroll()
		moves = append(moves, a.scrollY-before)
		// The column tracks the asks instead of trailing them: the debt is what
		// a scrollFollow's worth of time covers, so it settles at the rate times
		// the follow — a step and a half at this ask rate — and stays there.
		if lead := a.scrollGoal - a.scrollY; lead > 2.5*step {
			t.Fatalf("ask %d left the column %.1f behind, want under 2.5 steps (%.1f)", asked, lead, 2.5*step)
		}
	}
	// The asks ask for one step every 40 ms, and the column travels at that rate:
	// what was asked for is what is delivered.
	var sum float32
	for _, d := range moves[len(moves)-40:] {
		sum += d
	}
	got := sum / (40 * float32(frame) / float32(time.Second))
	want := step / float32(every) * float32(time.Second)
	if got > want*1.02 {
		t.Errorf("the column ran ahead of the asks at %.0f DIPs a second, want at most %.0f (one step every %s)", got, want, every)
	}
	if got < want*0.85 {
		t.Errorf("the column fell behind at %.0f DIPs a second, want about %.0f (one step every %s)", got, want, every)
	}

	// The asking stops: what is left is the follow's tail, and it lands.
	at = at.Add(400 * time.Millisecond)
	a.stepScroll()
	if a.gliding() {
		t.Errorf("the column is still gliding %s after the last ask", 400*time.Millisecond)
	}
}

// A notch of the wheel asks in lines of the book, not in the platform's own
// 33-DIP lines: three lines of a page's type, which is what the wheel's setting
// means, rather than the five that three lines of chrome would cover.
func TestWheelNotchAsksInBookLines(t *testing.T) {
	a, _ := newScrollWindow(t)
	line := a.opts.FontSize * a.opts.LineHeight
	for _, c := range []struct {
		dy   float32
		want float32
	}{
		{99, 3 * line},                    // three lines, the platform's default
		{33, line},                        // one
		{-99, -3 * line},                  // and back
		{297, scrollWheelLinesMax * line}, // nine lines is capped
		{3, line},                         // a stray few DIPs still asks a line
	} {
		if got := a.wheelDistance(c.dy); absf(got-c.want) > 0.01 {
			t.Errorf("a notch of %.0f DIPs asked for %.1f, want %.1f", c.dy, got, c.want)
		}
	}

	// And in motion: a notch glides to its place and lands there. The tester's
	// own scroll is a precise delta, which is the touchpad's path, so the notch
	// is handed to the column the way the pointer handler hands it over.
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	start := a.scrollY
	a.scrollBy(a.wheelDistance(99))
	if !a.gliding() {
		t.Fatalf("a notch did not start a glide")
	}
	for i := 0; i < 120 && a.gliding(); i++ {
		at = at.Add(7 * time.Millisecond)
		a.stepScroll()
	}
	if d := a.scrollY - start; absf(d-3*line) > 0.5 {
		t.Errorf("a notch moved the column %.2f DIPs, want %.2f", d, 3*line)
	}
}

// A key that repeats is the reader walking down the column, so what it sets is
// a speed and not a place: every frame of the hold covers what the one before it
// did. That is the difference between one movement and a glide per repeat, which
// the eye reads as a pulse however many frames the display gives it.
func TestScrollKeyHoldsOneRate(t *testing.T) {
	a, _ := newScrollWindow(t)
	step := 3 * a.opts.FontSize * a.opts.LineHeight
	if a.maxScroll() < 200*step {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	// A held key: Windows repeats one about every 31 ms, and a 144 Hz display
	// draws a frame every 7.
	const every, frame = 31 * time.Millisecond, 7 * time.Millisecond
	next := at.Add(every)
	var moves []float32
	for i := 0; i < 240; i++ {
		at = at.Add(frame)
		if !at.Before(next) {
			a.scrollLines(3)
			next = next.Add(every)
		}
		before := a.scrollY
		a.stepScroll()
		moves = append(moves, a.scrollY-before)
	}

	// The frames of the hold, once it has come up to its rate: one number, not a
	// pulse. The median stands in for the rate, and no frame may be far from it
	// either way — a glide per repeat is a factor of six.
	steady := moves[len(moves)-120:]
	sorted := append([]float32(nil), steady...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	median := sorted[len(sorted)/2]
	var sum float32
	for _, d := range steady {
		sum += d
		if d < median*0.85 || d > median*1.15 {
			t.Fatalf("a frame of the hold moved %.2f DIPs, want about %.2f: the column is pulsing, not moving", d, median)
		}
	}
	got := sum / (float32(len(steady)) * float32(frame) / float32(time.Second))
	if want := a.keyRate(); got < want*0.9 || got > want*1.1 {
		t.Errorf("the column held %.0f DIPs a second, want %.0f (three lines a tenth of a second)", got, want)
	}

	// The asking stops: the debt left is one step, and it is given back, not cut.
	at = at.Add(250 * time.Millisecond)
	a.stepScroll()
	if a.gliding() {
		t.Errorf("the column is still gliding %s after the last press", 250*time.Millisecond)
	}

	// One press is still one step, wherever it starts from, and it lands where
	// it asked within a couple of tenths.
	a.scrollNow(0)
	at = at.Add(time.Second)
	a.scrollLines(3)
	start := a.scrollY
	frames := 0
	for i := 0; i < 60 && a.gliding(); i++ {
		at = at.Add(frame)
		a.stepScroll()
		frames++
	}
	if d := a.scrollY - start; d < step-0.5 || d > step+0.5 {
		t.Errorf("one press moved the column %.2f DIPs, want %.2f", d, step)
	}
	if d := time.Duration(frames) * frame; d > 250*time.Millisecond {
		t.Errorf("one press took %s to land, want under 250ms", d)
	}
}

// Whatever a notch asks for, the glide ends: an ask smaller than a pixel is
// taken at once rather than left owing, and one of a few pixels is walked out a
// pixel a frame. A glide that could not pay what it owed would keep asking for
// frames and drawing the same picture forever.
func TestEveryAskLands(t *testing.T) {
	for _, ask := range []float32{0.1, 0.3, 0.66, 0.7, 1.2, 2.4, 12, 49.5, 200} {
		a, tt := newScrollWindow(t)
		a.instant = false
		tt.SetScale(1.5)
		tt.Frame()
		tt.Frame()
		at := time.Now()
		a.now = func() time.Time { return at }
		start := a.scrollY
		a.scrollBy(ask)
		frames := 0
		for frames < 500 && a.gliding() {
			at = at.Add(7 * time.Millisecond)
			tt.Frame()
			frames++
		}
		if a.gliding() {
			t.Errorf("an ask of %.2f DIP never landed: %.3f still owed after %d frames", ask, a.scrollGoal-a.scrollY, frames)
			continue
		}
		if got, want := a.scrollY-start, ask; got != want {
			t.Errorf("an ask of %.2f landed %.3f away from where it started", want, got)
		}
	}
}

// With the snapping off the glide is the same motion without the pixel floor: it
// lands exactly where it was asked to, never passes it, and comes up to its
// speed and leaves it again.
func TestANotchLandWithoutSnapping(t *testing.T) {
	old := snapScroll
	snapScroll = false
	t.Cleanup(func() { snapScroll = old })
	a, _ := newScrollWindow(t)
	if a.maxScroll() < 100 {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	// a display that turns over every 7 ms
	a.scrollBy(49.5)
	prev := a.scrollY
	var deltas []float32
	for i := 0; i < 400 && a.gliding(); i++ {
		at = at.Add(7 * time.Millisecond)
		a.stepScroll()
		if a.scrollY < prev-1e-4 {
			t.Fatalf("the column went back from %.4f to %.4f", prev, a.scrollY)
		}
		if a.scrollY > 49.5+1e-4 {
			t.Fatalf("the column passed the notch: %.4f", a.scrollY)
		}
		deltas = append(deltas, a.scrollY-prev)
		prev = a.scrollY
	}
	if a.gliding() {
		t.Fatalf("the notch never landed: %.4f to go", a.scrollGoal-a.scrollY)
	}
	if a.scrollY != 49.5 {
		t.Errorf("landed at %.4f, want 49.5", a.scrollY)
	}
	// The glide comes up to its speed and then leaves it: one peak, a ramp up to
	// it and a ramp down, so the column neither lurches out of rest nor stops
	// dead.
	peak := 0
	for i, d := range deltas {
		if d > deltas[peak] {
			peak = i
		}
	}
	for i := 1; i <= peak; i++ {
		if deltas[i] < deltas[i-1]-1e-4 {
			t.Fatalf("frame %d slowed to %.4f from %.4f before the peak: the glide does not come up to speed", i, deltas[i], deltas[i-1])
		}
	}
	// The last frame is the landing: what is left is taken whole, so it is
	// allowed to be the bigger of the two.
	for i := peak + 1; i < len(deltas)-1; i++ {
		if deltas[i] > deltas[i-1]+1e-4 {
			t.Fatalf("frame %d sped up to %.4f from %.4f after the peak: the glide does not ease out", i, deltas[i], deltas[i-1])
		}
	}
	if peak == 0 || peak == len(deltas)-1 {
		t.Fatalf("the glide has no ramp: %v", deltas)
	}
}

// A spin of the wheel travels as far as it was spun. Every notch joins the same
// debt, so what a fast wheel asks for is delivered at the speed it asked for
// instead of being cut back to what the follow could carry: ten notches in a
// quarter of a second cover ten notches, not the three or four the column used
// to be allowed to keep.
func TestASpinOfTheWheelTravelsAsFarAsItWasSpun(t *testing.T) {
	a, _ := newScrollWindow(t)
	const notch = 49.5
	if a.maxScroll() < 12*notch {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	const frame = 16667 * time.Microsecond
	for i := 0; i < 10; i++ {
		a.scrollBy(notch)
		// a free wheel's notches arrive faster than the display draws
		for f := 0; f < 2; f++ {
			at = at.Add(frame)
			a.stepScroll()
		}
	}
	for i := 0; i < 200 && a.gliding(); i++ {
		at = at.Add(frame)
		a.stepScroll()
	}
	if a.gliding() {
		t.Fatalf("the column never settled: it owes %.1f", a.scrollGoal-a.scrollY)
	}
	if want := float32(10) * notch; a.scrollY != want {
		t.Errorf("ten notches put the column at %.1f, want all of %.1f", a.scrollY, want)
	}
}

// A glide ends on the frame its picture stops changing: the tail of a follow is
// a share of its distance, and its last frames are under a device pixel, where a
// snapped column draws the same image again and then hops. No frame of a notch
// moves nothing.
func TestANotchNeverFreezesAtTheEnd(t *testing.T) {
	if !snapScroll {
		t.Skip("LAYX_NO_SNAP turned the snapping off")
	}
	// A display of 60 Hz and one of 144 Hz: a faster display draws more of the
	// follow's tail, and the tail is where the frames went by that moved no
	// pixels at all.
	for _, hz := range []time.Duration{60, 144} {
		t.Run(fmt.Sprintf("%dHz", hz), func(t *testing.T) {
			notchNeverFreezes(t, time.Second/hz)
		})
	}
}

// notchNeverFreezes glides one wheel notch a frame at the given frame interval
// and holds every frame of it to moving the picture.
func notchNeverFreezes(t *testing.T, frame time.Duration) {
	t.Helper()
	a, tt := newScrollWindow(t)
	a.instant = false
	tt.SetScale(1.5)
	tt.Frame()
	tt.Frame()
	if !(a.devScale > 0) {
		t.Fatalf("no frame said where the column is drawn (scale %g)", a.devScale)
	}
	at := time.Now()
	a.now = func() time.Time { return at }
	drawn := func() float32 { return snapDevices(a.viewTop-a.scrollY, a.devScale) }
	a.scrollBy(49.5)
	last := drawn()
	var deltas []float32
	for i := 0; i < 400 && a.gliding(); i++ {
		at = at.Add(frame)
		tt.Frame()
		now := drawn()
		deltas = append(deltas, now-last)
		last = now
	}
	if a.gliding() {
		t.Fatalf("the notch never landed: it owes %.1f", a.scrollGoal-a.scrollY)
	}
	if len(deltas) < 3 {
		t.Fatalf("the notch was over in %d frames", len(deltas))
	}
	t.Logf("one notch at 150%%, a frame every %s, in device px: %v", frame, deltas)
	// A glide starts from rest — that is what a bell is, and what a browser's
	// notch is: its first frames cover a share of a pixel and the screen draws
	// the picture it had. What must not happen is a frame that draws the same
	// picture again once the glide is under way, which is the frozen tail a
	// follow used to leave.
	moved := false
	for i, d := range deltas {
		if d != 0 {
			moved = true
			continue
		}
		if moved {
			t.Errorf("frame %d of %d drew the same picture again after the glide had started (%v device px a frame)", i, len(deltas), deltas)
		}
	}
	if !moved {
		t.Fatalf("the notch drew nothing at all: %v", deltas)
	}
	if a.scrollY != 49.5 {
		t.Errorf("the column settled at %.2f, want the notch's 49.5", a.scrollY)
	}
}

// The column is drawn on whole device pixels, so two offsets inside the same
// pixel print the same page: a glyph keeps one subpixel phase while the reader
// is between two of them, instead of being swapped for a near copy of itself —
// and moved by half a pixel — on every frame of the gesture.
func TestScrollDrawsOnWholePixels(t *testing.T) {
	if !snapScroll {
		t.Skip("LAYX_NO_SNAP turned the snapping off")
	}
	a, tt := newScrollWindow(t)
	if a.maxScroll() < 2 {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	r := sheetRect(t, tt)
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	draw := func(y float32) *image.RGBA {
		a.scrollAt = time.Time{}
		a.scrollGoal, a.scrollY = y, y
		tt.Frame()
		return tt.Image()
	}
	mid := a.maxScroll() / 2
	first := draw(mid + 0.2)
	second := draw(mid + 0.35)
	differ := 0
	for py := int(r.Y); py < int(r.Y+r.H); py++ {
		for px := int(r.X); px < int(r.X+r.W); px++ {
			if !image.Pt(px, py).In(first.Bounds()) {
				continue
			}
			if first.RGBAAt(px, py) != second.RGBAAt(px, py) {
				differ++
			}
		}
	}
	if differ != 0 {
		t.Errorf("%d pixels changed between two offsets inside one pixel", differ)
	}
}

// A line belongs to the tile that holds its top edge, so a line near a tile
// boundary overhangs into the next tile's area. Drawing the lower tile first
// keeps that overhang on top of the paper instead of letting the next tile's
// opaque sheet cut it in half mid-screen.
func TestScrollDoesNotCutLinesAtTileBoundaries(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)

	// Find a tile boundary that falls inside a line of text.
	var boundary float32
	for k := 1; k < a.layout.TileCount(); k++ {
		y := float32(k) * a.tileH
		before, ok1 := a.layout.LocationAt(y - 0.5)
		after, ok2 := a.layout.LocationAt(y + 0.5)
		if ok1 && ok2 && before == after {
			boundary = y
			break
		}
	}
	if boundary == 0 {
		t.Skip("no line straddles a tile boundary in this book")
	}

	// Put the boundary in the middle of the column and draw it.
	y := boundary - a.scrollViewport()/2
	a.scrollTo(y)
	tt.Frame()
	img := tt.Image()
	at := r.Y + scrollInset + (boundary - y)

	x0, x1 := int(r.X+40), int(r.W+r.X-40)
	above := inkPixels(img, x0, int(at)-8, x1, int(at)-1)
	below := inkPixels(img, x0, int(at)+1, x1, int(at)+8)
	if above == 0 {
		t.Skipf("the band above the boundary has no ink to compare")
	}
	if below*3 < above {
		t.Errorf("the line is cut at the tile boundary: %d ink pixels above it, %d below", above, below)
	}
}

// inkPixels counts the pixels in a rectangle that are not the paper: text is
// drawn in the palette's ink, which differs from the sheet around it.
func inkPixels(img *image.RGBA, x0, y0, x1, y1 int) int {
	paper := img.RGBAAt(x0, y0-20)
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			p := img.RGBAAt(x, y)
			diff := absInt(int(p.R)-int(paper.R)) + absInt(int(p.G)-int(paper.G)) +
				absInt(int(p.B)-int(paper.B))
			if diff > 120 {
				n++
			}
		}
	}
	return n
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
