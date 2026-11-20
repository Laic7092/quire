package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/locales"
)

// A tap in the middle of a phone's page shows and hides the bars; the sides
// still turn the page, and the middle is not a page turn.
func TestPhoneMiddleTapTogglesTheBars(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	if !a.chromeShown() {
		t.Fatal("the bars are away before the reader touched the page")
	}
	tt.ClickAt(195, 400)
	if a.chromeShown() {
		t.Error("a tap in the middle left the bars up")
	}
	tt.ClickAt(195, 400)
	if !a.chromeShown() {
		t.Error("a second tap in the middle did not bring the bars back")
	}

	start := a.page
	tt.ClickAt(40, 400)
	if a.page != start-1 {
		t.Errorf("a tap at the left went to page %d, want %d", a.page, start-1)
	}
	tt.ClickAt(350, 400)
	if a.page != start {
		t.Errorf("a tap at the right went to page %d, want %d", a.page, start)
	}
}

// A finger on a scrolling column drags it, rather than marking text: the
// column follows the finger and keeps its speed for a moment.
func TestPhoneFingerDragsTheScrollingColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	before := a.scrollY
	tt.FingerDown(500, 500)
	tt.FingerMove(500, 420)
	tt.FingerMove(500, 340)
	tt.FingerUp(500, 340)
	if a.scrollY <= before {
		t.Errorf("the column stayed at %.1f after a finger moved 160 DIPs down", a.scrollY)
	}
	if len(a.selQuads) > 0 {
		t.Error("dragging the column left a selection behind")
	}
}

// The phone's library is the tabbed one: the destinations at the foot choose
// the filter, and the Mac source list is not built beside it.
func TestPhoneLibraryUsesTheTabBar(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	a.book = nil
	a.doc = nil
	a.layout = nil
	a.bookWindow = false
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	if a.libTab != mobileTabHome || a.libFilter != 0 {
		t.Fatalf("the library opens on tab %d, filter %d", a.libTab, a.libFilter)
	}
	if err := tt.Click(locales.SidebarAll()); err != nil {
		t.Fatalf("the All destination is not in the bar: %v", err)
	}
	if a.libTab != mobileTabAll || a.libFilter != 3 {
		t.Errorf("the All destination chose tab %d, filter %d", a.libTab, a.libFilter)
	}
	// The shops the Mac source list holds are a screen of their own, and its
	// rows are built without a Mac sidebar behind them.
	if err := tt.Click(locales.SidebarSources()); err != nil {
		t.Fatalf("the Sources destination is not in the bar: %v", err)
	}
	if a.libTab != mobileTabSources {
		t.Errorf("the Sources destination chose tab %d", a.libTab)
	}
	tt.Frame()
	if !tt.HasText(locales.MenuOpen()) {
		t.Error("the Sources screen does not offer the way to open a book")
	}
}

// The phone's grid falls from three covers to two when the screen cannot give
// three a finger's width, and the covers keep their shape either way.
func TestPhoneMetricsFitTheCovers(t *testing.T) {
	m := phoneMetricsFor(390)
	if m.cols != 3 {
		t.Errorf("a 390 DIP phone puts %d covers in a row, want 3", m.cols)
	}
	if want := (float32(390) - 2*m.padding - 2*m.coverGap) / 3; m.coverW != want {
		t.Errorf("a cover is %.1f wide, want %.1f", m.coverW, want)
	}
	if m.coverH != m.coverW*coverAspect {
		t.Errorf("the cover's height %.1f does not keep its %.2f aspect", m.coverH, coverAspect)
	}
	if narrow := phoneMetricsFor(320); narrow.cols != 2 {
		t.Errorf("a 320 DIP phone puts %d covers in a row, want 2", narrow.cols)
	}
}

// pressPoint finds a point on the page that is over text, in the page box's
// own coordinates (which is what handlePageInput is given).
func pressPoint(t *testing.T, a *app) (float32, float32) {
	t.Helper()
	for y := float32(80); y < 700; y += 6 {
		for x := float32(20); x < a.boxW-20; x += 6 {
			if _, ok := a.pageLocation(x, y); ok {
				return x, y
			}
		}
	}
	t.Fatal("the page has no text to press")
	return 0, 0
}

// countHaptics swaps the one road to the motor for a counter.
func countHaptics(t *testing.T) *int {
	t.Helper()
	n := 0
	old := playHaptic
	playHaptic = func() { n++ }
	t.Cleanup(func() { playHaptic = old })
	return &n
}

// The phone's motor plays for one gesture and no other: a finger that stays
// until the hold becomes a selection, and then moves. Taps, page turns, swipes
// and chrome toggles are silent — a reader turning pages must never feel the
// motor.
//
// The events go straight to handlePageInput: the tester's finger is put through
// the window's transform, and this test wants the page box's coordinates.
func TestPhoneOnlyTheLongPressHaptics(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	plays := countHaptics(t)
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	x, y := pressPoint(t, a)
	down := func(x, y float32) {
		if !a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y}) {
			t.Fatalf("the press at %.0f,%.0f was not taken", x, y)
		}
	}
	move := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x, Y: y})
	}
	up := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
	}

	// A tap on the right edge turns the page without buzzing.
	start := a.page
	down(0.9*a.boxW, y)
	up(0.9*a.boxW, y)
	if a.page == start {
		t.Fatal("a tap at the right did not turn the page")
	}
	if *plays != 0 {
		t.Fatalf("a page turn played the motor %d times", *plays)
	}

	// A quick sideways drag is a swipe, not a selection: no buzz either.
	start = a.page
	down(0.8*a.boxW, y)
	move(0.4*a.boxW, y)
	up(0.4*a.boxW, y)
	if a.page == start {
		t.Fatal("the swipe did not turn the page")
	}
	if *plays != 0 {
		t.Fatalf("a page swipe played the motor %d times", *plays)
	}

	// A finger that outlasts the hold, then moves, starts selecting: one tick,
	// and one only, however far it then drags.
	down(x, y)
	at = at.Add(touchHold)
	move(x+40, y+40)
	if *plays != 1 {
		t.Fatalf("the long press played the motor %d times, want 1", *plays)
	}
	at = at.Add(time.Second)
	move(x+90, y+90)
	up(x+90, y+90)
	if *plays != 1 {
		t.Errorf("the rest of the drag played the motor again: %d plays", *plays)
	}
}

// A mouse drag on a desktop selects text with the same handler; the motor is
// not asked at all, however long the button is held.
func TestDesktopDragDoesNotHaptic(t *testing.T) {
	a := newWindow(t)
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	plays := countHaptics(t)
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	x, y := pressPoint(t, a)
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y})
	at = at.Add(2 * touchHold)
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x + 60, Y: y + 60})
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x + 60, Y: y + 60})
	if *plays != 0 {
		t.Errorf("a desktop drag played the motor %d times", *plays)
	}
}

// A phone's long press marks text, and the commands come after the selection is
// made: pressed, held, dragged, released — and only then the card with copy, add
// note, select page and clear selection. A press with no drag, or the secondary
// button a desktop has, opens nothing over an empty page.
func TestPhoneSelectionAsksForTheMenu(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	x, y := pressPoint(t, a)
	down := func(x, y float32) {
		if !a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y}) {
			t.Fatalf("the press at %.0f,%.0f was not taken", x, y)
		}
	}
	move := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x, Y: y})
	}
	up := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
	}

	// A tap is a page turn or the bars: no selection, no card.
	down(0.9*a.boxW, y)
	up(0.9*a.boxW, y)
	if a.selMenu || len(a.selQuads) != 0 {
		t.Fatal("a tap left a selection or its card behind")
	}

	// Holding still and then dragging selects; the card waits for the release.
	down(x, y)
	at = at.Add(touchHold)
	move(x+40, y+40)
	if len(a.selQuads) == 0 {
		t.Fatal("the long press selected no text")
	}
	if a.selMenu {
		t.Error("the card opened while the finger was still selecting")
	}
	up(x+40, y+40)
	if !a.selMenu {
		t.Fatal("the finished selection did not ask for the commands")
	}

	tt.Frame()
	for _, label := range []string{
		locales.MenuCopy(), locales.AddNote(), locales.MenuSelectPage(), locales.MenuClearSelection(),
	} {
		if !tt.HasText(label) {
			t.Errorf("the card has no %q", label)
		}
	}
}

// A finger that moves before the hold selects nothing: a quick drag is a
// scroll or a page swipe, or on a paged book a gesture the page does not use.
// Only the hold turns a press into a selection, as the system's own text does.
func TestPhoneQuickDragSelectsNothing(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	x, y := pressPoint(t, a)
	down := func(x, y float32) {
		if !a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y}) {
			t.Fatalf("the press at %.0f,%.0f was not taken", x, y)
		}
	}
	move := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x, Y: y})
	}
	up := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
	}

	// A quick vertical drag on a paged book: no selection, no card, no turn.
	page := a.page
	down(x, y)
	move(x+20, y+60)
	move(x+30, y+120)
	if len(a.selQuads) != 0 {
		t.Fatal("a quick drag marked text without the hold")
	}
	up(x+30, y+120)
	if a.selMenu {
		t.Error("a quick drag asked for the commands")
	}
	if a.page != page {
		t.Errorf("a quick drag turned the page to %d", a.page)
	}

	// A quick sideways drag on a paged book is the swipe: also no selection.
	down(0.8*a.boxW, y)
	move(0.4*a.boxW, y)
	up(0.4*a.boxW, y)
	if len(a.selQuads) != 0 {
		t.Fatal("a swipe marked text")
	}

	// A hold that lifts without moving marks nothing, and does not turn the
	// page it was pressed on either.
	page = a.page
	down(x, y)
	at = at.Add(touchHold + 100*time.Millisecond)
	up(x, y)
	if a.selMenu || len(a.selQuads) != 0 {
		t.Error("a hold alone marked text")
	}
	if a.page != page {
		t.Errorf("a long press without a drag turned the page to %d", a.page)
	}
}

// A quick sideways drag on a phone's scrolling column is not a selection either:
// the column only marks text once the hold is granted.
func TestPhoneQuickDragOnColumnSelectsNothing(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	tt.Frame()

	x, y := pressPoint(t, a)
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y})
	at = at.Add(120 * time.Millisecond)
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x + 60, Y: y + 6})
	if len(a.selQuads) != 0 {
		t.Fatal("a quick sideways drag marked text without the hold")
	}
	a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x + 60, Y: y + 6})
	if a.selMenu {
		t.Error("a quick drag asked for the commands")
	}
	if len(a.selQuads) != 0 {
		t.Errorf("the selection survived the release: %d quads", len(a.selQuads))
	}
}

// A drag that goes one way and comes back leaves the column where it began, and
// the lift has nothing to throw: the page must not spring off on its own. A
// one-way flick still flies, as a throw should.
func TestPhoneBackAndForthDragDoesNotSpring(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	a.instant = false
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	tt.Frame()

	down := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y})
	}
	move := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerMove, X: x, Y: y})
	}
	up := func(x, y float32) {
		a.handlePageInput(ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
	}

	// Down the column and back, lifting where the finger landed.
	start := a.scrollY
	down(500, 500)
	for i := 1; i <= 10; i++ {
		at = at.Add(16 * time.Millisecond)
		move(500, 500-float32(i)*10)
	}
	for i := 9; i >= 0; i-- {
		at = at.Add(16 * time.Millisecond)
		move(500, 500-float32(i)*10)
	}
	up(500, 500)
	if a.scrollY != start {
		t.Fatalf("the back-and-forth drag left the column at %.1f, from %.1f", a.scrollY, start)
	}
	if a.flingV != 0 {
		t.Errorf("the lift flung the column at %.0f DIPs a second after a back-and-forth drag", a.flingV)
	}
	for i := 0; i < 40; i++ {
		at = at.Add(16 * time.Millisecond)
		a.stepColumn()
	}
	if a.scrollY != start {
		t.Errorf("the page sprung %.1f DIPs after the finger lifted", a.scrollY-start)
	}

	// One stroke the whole way: that is a flick, and it carries on.
	down(500, 500)
	for i := 1; i <= 6; i++ {
		at = at.Add(16 * time.Millisecond)
		move(500, 500-float32(i)*20)
	}
	up(500, 380)
	if a.flingV == 0 {
		t.Fatal("a one-way flick was not carried on after the lift")
	}
	lifted := a.scrollY
	for i := 0; i < 40; i++ {
		at = at.Add(16 * time.Millisecond)
		a.stepColumn()
	}
	if a.scrollY <= lifted {
		t.Errorf("the flick stopped at %.1f, from %.1f", a.scrollY, lifted)
	}
}

// A phone has no secondary button, so the page does not register a system
// context menu: nothing pops up over an empty page but the selection's own card.
func TestPhonePageHasNoSystemContextMenu(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	x, y := pressPoint(t, a)
	tt.RightClickAt(x+10, y+10)
	if m := tt.Menu(); m != nil {
		t.Errorf("a long press on the page opened the system menu %q", m)
	}
	if a.selMenu {
		t.Error("the system's press opened the commands' card")
	}
}

// A finger scrolls the column as soon as it lifts: two drags in a row both move
// it. The page takes the finger's moves itself, so nothing of the toolkit's
// fling is left to swallow the next touch.
func TestPhoneScrollsTwiceInARow(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	a.now = tt.Now
	tt.Frame()

	drag := func() {
		tt.FingerDown(500, 500)
		tt.FingerMove(500, 470)
		if !a.dragScroll {
			t.Error("the page did not take the finger's moves itself")
		}
		tt.FingerMove(500, 440)
		tt.FingerUp(500, 440)
	}
	first := a.scrollY
	drag()
	one := a.scrollY
	if one <= first {
		t.Fatalf("the first drag left the column at %.1f, from %.1f", one, first)
	}
	drag()
	if a.scrollY <= one {
		t.Fatalf("the second drag left the column at %.1f, from %.1f", a.scrollY, one)
	}
}

// A phone's scrolling column takes the whole screen: the bars are a layer over
// it, not a band the text keeps clear of, so the flow runs under them and there
// is no blank at either end — whether they are shown or away.
func TestPhoneScrollingColumnFillsTheScreen(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	a.opts.Scroll = true
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	if got := a.boxH; got != 844 {
		t.Fatalf("a phone's column is %.0f tall in an 844 DIP screen", got)
	}
	away := a.boxH
	a.chromeAway = true
	tt.Frame()
	if a.boxH != away {
		t.Errorf("hiding the bars re-measured the column: %.0f, was %.0f", a.boxH, away)
	}
	a.chromeAway = false

	// A page still keeps the bars' room: it cannot slide under them.
	a.opts.Scroll = false
	tt.Frame()
	tt.Frame()
	if got, want := a.boxH, float32(844-mobileHeaderHeight-mobileFooterHeight); got != want {
		t.Errorf("a phone's page is %.0f tall, want %.0f between the bars", got, want)
	}
}

// A flick carries the column on at the speed the finger had, decaying over
// flingTau: a hard flick travels as far as it was hard, not a fixed share of it,
// and it stops by itself.
func TestFlingCarriesTheFingerSpeed(t *testing.T) {
	a, tt := newScrollWindow(t)
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	a.instant = false // the test builds at rest, as a reader with no motion
	_ = tt
	a.scrollY, a.scrollGoal = 500, 500

	a.startFling(1000)
	if !a.gliding() {
		t.Fatal("a flick of 1000 DIPs a second started nothing")
	}
	// The first frame covers the integral of the decaying speed.
	at = at.Add(100 * time.Millisecond)
	a.stepColumn()
	tau := float32(flingTau.Seconds())
	want := 1000 * tau * (1 - float32(math.Exp(-0.1/0.4)))
	if got := a.scrollY - 500; math.Abs(float64(got-want)) > 0.5 {
		t.Errorf("the first frame moved %.2f DIPs, want %.2f", got, want)
	}
	// It slows down, and the whole flick is about flingTau of the speed it left.
	slow := a.flingV
	at = at.Add(100 * time.Millisecond)
	a.stepColumn()
	if a.flingV >= slow {
		t.Errorf("the flick did not slow down: %.0f, was %.0f", a.flingV, slow)
	}
	for i := 0; i < 100 && a.gliding(); i++ {
		at = at.Add(16 * time.Millisecond)
		a.stepColumn()
	}
	if a.gliding() {
		t.Fatal("the flick never stopped")
	}
	// It stops once what is left is below flingMin, so the whole flick is the
	// speed above that floor, times flingTau.
	if got, want := a.scrollY-500, (1000-float32(flingMin))*tau; math.Abs(float64(got-want)) > 2 {
		t.Errorf("a 1000 DIP/s flick travelled %.0f DIPs, want about %.0f", got, want)
	}
	// A press on the column drops the flick where it is drawn.
	a.scrollY = 600
	a.startFling(2000)
	a.stopScroll()
	if a.gliding() {
		t.Error("a press did not stop the flick")
	}
	if a.scrollY != 600 {
		t.Errorf("stopping the flick moved the column to %.0f", a.scrollY)
	}
}

// A thumb that rests on the page for a moment and then drags scrolls the column:
// only the system's own half second marks text. A shorter hold once stole the
// gesture, and a reader who paused to think got a selection instead of scrolling.
func TestPhoneHesitantFingerStillScrolls(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	tt.Frame()

	tt.FingerDown(500, 500)
	at = at.Add(touchHold - 100*time.Millisecond) // a pause, not a long press
	tt.FingerMove(500, 470)
	if !a.dragScroll {
		t.Fatal("a 400 ms hesitation was taken for a long press, not a scroll")
	}
	if len(a.selQuads) != 0 {
		t.Error("the hesitation selected text instead of scrolling")
	}
	tt.FingerUp(500, 470)

	// The hold itself still selects: that is what it is for.
	tt.FingerDown(500, 600)
	at = at.Add(touchHold + 100*time.Millisecond)
	tt.FingerMove(500, 570)
	if a.dragScroll {
		t.Error("a hold past the threshold scrolled instead of selecting")
	}
	if len(a.selQuads) == 0 {
		t.Error("the hold selected no text")
	}
	tt.FingerUp(500, 570)
}

// A wheel's notch during a flick is the ask: the flick is dropped and the column
// goes where the notch said, rather than racing it.
func TestWheelAskDropsTheFlick(t *testing.T) {
	a, tt := newScrollWindow(t)
	at := time.Unix(0, 0)
	a.now = func() time.Time { return at }
	a.instant = false
	_ = tt
	a.scrollY, a.scrollGoal = 500, 500
	a.startFling(1000)
	a.scrollBy(300)
	if a.flingV != 0 {
		t.Fatalf("the ask left the flick running at %.0f DIPs a second", a.flingV)
	}
	for i := 0; i < 60 && a.gliding(); i++ {
		at = at.Add(16 * time.Millisecond)
		a.stepColumn()
	}
	if want := float32(800); math.Abs(float64(a.scrollY-want)) > 2 {
		t.Errorf("the column stands at %.0f, want %.0f", a.scrollY, want)
	}
}
