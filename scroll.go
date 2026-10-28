package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/locales"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// The reading page can be turned or scrolled. Scrolling builds the layout with
// render.ModeScroll: the chapters stack into one flow with no page slots, and
// the engine slices that flow into tiles one viewport tall. The reader keeps a
// content offset — scrollY is the flow's y at the top of the column — and draws
// the tiles the viewport covers. Positions, selection rectangles and search
// hits then live in the same content coordinates in both modes, which is why
// the panels, the store and the search index need no second code path.

// scrollInset is the room the column keeps between its text and the bars above
// and below it, in DIPs: the same head the paged sheet gives its first line.
const scrollInset = reader.PageTop

// scrollGlideBase and scrollGlideEvery are the length of a glide, in
// milliseconds of base plus milliseconds a DIP of distance. Both are measured
// from a browser on this machine (Edge, 150% scaling, 144 Hz): one notch of the
// wheel — 300 CSS px at this machine's setting — takes 104 ms, three notches that
// coalesce into one glide take 202 ms, and the shape of it is one bell, up over
// the first half and down over the second. A shorter distance does not shrink
// below scrollGlideMin: a browser's arrow key, 40 px, still takes a tenth of a
// second, and a step shorter than that would read as a jump.
const (
	scrollGlideBase  = 55 * time.Millisecond
	scrollGlideEvery = 163 * time.Microsecond
	scrollGlideMin   = 100 * time.Millisecond
)

// scrollFollow is how long the column would take to cover what a scroll owes at
// the rate that debt implies: distance over time, which is the middle of a
// glide. It shapes a stream of asks — a held key, a wheel spun hard — which glides
// for as long as it is asked to and so cannot be a bell of a fixed length.
const scrollFollow = 60 * time.Millisecond

// scrollRamp is how long the column takes to come up to a new rate. A notch
// arrives as a distance all at once, and a column that takes it at once starts a
// glide with a lurch — half a line in one frame at 150%, which is what a reader
// feels as the text jumping and then catching up. Coming up to the rate over
// three frames or so removes the lurch without making the glide feel late, and a
// stream of asks — a held key, a wheel spun hard — joins a rate already there
// instead of restarting from nothing.
const scrollRamp = 30 * time.Millisecond

// scrollLand is how close, in device pixels, the column has to be to the place it
// was sent to before it is there: two, so that a glide ends while it is still
// covering a pixel a frame. Finer than that and the last frames each cover a
// share of a pixel, which the screen draws as one pixel at a time — a crawl that
// reads as the scroll going soft at the end.
const scrollLand = 2

// scrollStarved is the gap past which a glide is taken to have been starved of
// frames rather than to be running: the app was busy, or the window was in the
// background, and the column arrives at the place it was sent to instead of
// resuming a crawl nobody was watching.
const scrollStarved = 100 * time.Millisecond

// platformScrollLine is how many DIPs the toolkit gives one of the platform's
// scroll lines: a notch of a wheel arrives as its line count times this, which
// is three lines by default and nine on some machines.
const platformScrollLine = 33

// scrollWheelLinesMax caps what one notch may ask for, in lines of the book: a
// nine-line platform setting would otherwise send the page fifteen lines at a
// time, and no one reads at that.
const scrollWheelLinesMax = 4

// wheelLineOverride, set by LAYX_WHEEL_LINES, is how many lines of the book a
// notch moves whatever the platform says: the dial for a wheel that reads too
// fast or too slow.
var wheelLineOverride, wheelLineSet = envNumber("LAYX_WHEEL_LINES")

// wheelLines is how many lines of the book a scroll event of dy DIPs asks for.
func (a *app) wheelLines(dy float32) float32 {
	lines := absf(dy) / platformScrollLine
	if wheelLineSet {
		lines = float32(wheelLineOverride)
	}
	return max(1, min(lines, scrollWheelLinesMax))
}

// wheelDistance is how far a scroll event of dy DIPs asks the column to move.
// The count of lines is the platform's; the size of a line is the book's. A
// notch is three lines of the platform and three lines here — 60 DIPs of the
// page's type, not the 99 the platform's own 33-DIP line would cover of it,
// which is five lines of a book. That is what makes a wheel read as one notch
// of a page rather than as a leap past the paragraph the reader was on.
func (a *app) wheelDistance(dy float32) float32 {
	line := a.opts.FontSize * a.opts.LineHeight
	return float32(math.Copysign(float64(a.wheelLines(dy)*line), float64(dy)))
}

// envNumber reads a number from the environment, reporting whether it was one.
func envNumber(name string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// scrollGapMin is the distance under which the column is at the place it was
// sent to when the screen cannot say otherwise: half a device pixel, which no
// reader can see it settle. See stepScroll, which asks the pixels rather than
// this number when it can.
const scrollGapMin = 0.5

// scrollUniform, cleared by LAYX_SCROLL_UNIFORM=0, is how a key that keeps
// asking moves the column: at one rate, held for as long as the asks come,
// rather than as one glide per ask. A key that repeats is not a series of
// asks to arrive at places — it is the reader walking down the column — so
// what it sets is a speed, and the frames hold it.
var scrollUniform = os.Getenv("LAYX_SCROLL_UNIFORM") != "0"

// scrollDebug, set by LAYX_SCROLL_DEBUG=1, prints how far the column moved on
// each frame of a glide: the sequence a reader feels as smooth or as not.
var scrollDebug = os.Getenv("LAYX_SCROLL_DEBUG") != ""

const (
	// scrollKeyLines and scrollKeyOver set the rate a key moves the column at:
	// three lines of type in a tenth of a second, which is what reading a column
	// wants and what a browser's smooth scrolling gives.
	scrollKeyLines = 3.0
	scrollKeyOver  = 100 * time.Millisecond
	// scrollKeyRamp is how long the column takes to come up to that rate, and
	// scrollKeyTail the distance over which it gives it back when the asks stop:
	// the debt a key leaves is at most one step, so the place it stops at is the
	// place of its last press.
	scrollKeyRamp = 80 * time.Millisecond
	scrollKeyTail = 80 * time.Millisecond
)

// scrolling reports whether the layout on screen is a scrolling column. It
// follows the published layout, not the setting, so the frame between changing
// the setting and the new layout keeps drawing what it has.
func (a *app) scrolling() bool { return a.layScroll }

// scrollViewport is how much of the flow the column shows, in DIPs.
func (a *app) scrollViewport() float32 {
	if a.boxH <= 2*scrollInset {
		return 1
	}
	return a.boxH - 2*scrollInset
}

// maxScroll is the largest content offset the viewport can take: 0 when the
// whole book already fits in the column.
func (a *app) maxScroll() float32 {
	if a.layout == nil {
		return 0
	}
	return max(0, a.layout.ContentHeight()-a.scrollViewport())
}

// scrollTo sends the column to content y, clamped to the book: a place, as the
// table of contents, a search hit, Home or the store names one. A place is
// arrived at rather than travelled: the column lands in the frame the ask
// arrived in, however far away it is. It is the asks for a distance — a wheel's
// notch, a key's step, a screenful — that glide.
func (a *app) scrollTo(y float32) {
	if a.layout == nil {
		return
	}
	// A glide already on its way ends here rather than being followed, and it
	// drops a place a pending mode switch meant to restore: the reader named
	// where to go, and that place is on the sheet now.
	a.scrollNow(clampf(y, 0, a.maxScroll()))
}

// scrollNow puts the column at content y in the frame the ask arrived in.
func (a *app) scrollNow(y float32) {
	if a.layout == nil {
		return
	}
	a.dismissCover()
	a.jumpAt = false
	// A glide still on its way is cut short here, and the log counts it: the
	// column was moved by something other than the ask it was following.
	if a.gliding() {
		perf.glide()
	}
	y = clampf(y, 0, a.maxScroll())
	a.scrollAt = time.Time{}
	a.scrollPlan = false
	a.scrollRate = false
	a.scrollGoal, a.scrollY = y, y
	a.scrollVel = 0
	a.scrollAhead()
	a.afterScroll()
}

// scrollBy moves the column by dy DIPs, as every scroll asks: a wheel's notch, a
// key's step, a screenful. The distance is the ask, whatever asked for it: the
// asks add up in the goal, and the frames that follow cover it at one rate — so
// one ask glides to its place while a burst of them is paid at the rate it comes
// at, and nothing is dropped or restarted on the way.
//
// Nothing is dropped: a burst used to be cut back to one ask's worth of debt,
// which did keep a held key from sprinting, but it also threw away distance a
// wheel had asked for — a spin of ten notches delivered three or four, and the
// column could not be made to move faster however hard the wheel was turned. The
// follow shapes the motion now, and it is above what a hand asks for, so what is
// asked for is what is delivered.
func (a *app) scrollBy(dy float32) {
	if a.layout == nil || dy == 0 {
		return
	}
	// The bars follow the reader down the column and come back when the reader
	// does: the chrome is the window's, not the page's.
	a.chromeFollow(dy)
	// Any scrolling is a turn of the page as far as the cover is concerned.
	a.dismissCover()
	a.jumpAt = false
	if a.instant {
		a.scrollNow(a.scrollY + dy)
		return
	}
	// Every ask is owed in full and joins the same debt, so a wheel spun hard
	// travels as far as it was spun instead of as far as the follow kept up.
	y := clampf(a.scrollGoal+dy, 0, a.maxScroll())
	resting := !a.gliding()
	a.follow(y)
	if resting && a.gliding() && !a.scrollPlan {
		// One ask, from a column standing still: a glide of its own, and a glide of
		// its own is a bell — up over the first half, down over the second, and a
		// stop at the end. That is what a browser gives a notch of a wheel, and it
		// is why one notch does not share the shape of a held key.
		a.plan(y)
	}
}

// scrollByNow moves the column by dy DIPs at once, from where it is drawn: a
// continuous device's deltas are the movement itself, so they land in the frame
// they arrive in and stack from the position on screen.
func (a *app) scrollByNow(dy float32) {
	a.chromeFollow(dy)
	a.scrollNow(a.scrollY + dy)
}

// follow sets the place the column owes, without touching where it is drawn: the
// next frames cover the distance. It is the one way an ask becomes motion, so a
// notch, a step and a screenful differ in their distance and in nothing else.
func (a *app) follow(y float32) {
	if y == a.scrollY {
		a.scrollAt = time.Time{}
		a.scrollGoal = y
		a.scrollVel = 0
		return
	}
	if a.scrollPlan {
		// The reader asked again while the bell was still on its way: what was one
		// ask's bell becomes a stream, which the frames pay at the rate the asking
		// comes at. The hand-over keeps the speed the bell had reached, so a held
		// key accelerates out of its first step instead of starting over.
		a.scrollPlan = false
		a.scrollVel = a.planSpeed()
		a.scrollAt = a.clock()
		a.scrollGoal = y
		a.scrollAhead()
		return
	}
	a.scrollGoal = y
	if a.scrollAt.IsZero() {
		// A column standing still starts from no speed: the ramp is what keeps a
		// notch from lurching on its first frame.
		a.scrollAt = a.clock()
		a.scrollVel = 0
		a.scrollAhead()
	}
}

// plan starts a glide of its own: the column covers the distance to y over a
// length the distance sets, following a bell. Nothing is integrated — the place
// is a function of the clock — so a late frame or a stutter does not stretch the
// glide: it arrives when it said it would, and stops dead there, which is what
// makes one notch feel like a notch rather than like a slide.
func (a *app) plan(y float32) {
	if y == a.scrollY {
		return
	}
	a.scrollFrom = a.scrollY
	// The bell's clock starts on the frame that first draws it, not on the ask:
	// the app is idle when an ask arrives and its next frame is a tick of the idle
	// cadence away, which on this machine is 17 ms — a sixth of the whole glide
	// spent before the reader sees anything. Played in the frames it gets, the bell
	// always shows its own start: a share of a pixel, then half a line, then the
	// middle of the glide.
	a.scrollStart = time.Time{}
	a.scrollDur = glideLength(y - a.scrollY)
	a.scrollPlan = true
	a.scrollRate = false
	a.scrollAt = a.clock()
	a.scrollVel = 0
	a.scrollAhead()
}

// planSpeed is how fast the planned glide is moving now, in DIPs a second: the
// bell's slope at the phase it has reached.
func (a *app) planSpeed() float32 {
	if !a.scrollPlan || a.scrollDur <= 0 || a.scrollStart.IsZero() {
		return a.scrollVel
	}
	u := float32(a.clock().Sub(a.scrollStart)) / float32(a.scrollDur)
	return (a.scrollGoal - a.scrollFrom) / float32(a.scrollDur.Seconds()) * bellSlope(u)
}

// stepPlan draws the column where the bell says it is, and reports whether the
// bell has finished.
func (a *app) stepPlan(now time.Time) bool {
	if a.scrollStart.IsZero() {
		a.scrollStart = now
	}
	u := float32(now.Sub(a.scrollStart)) / float32(a.scrollDur)
	if u >= 1 {
		a.scrollY = a.scrollGoal
		a.scrollPlan = false
		a.scrollVel = 0
		return true
	}
	a.scrollY = a.scrollFrom + (a.scrollGoal-a.scrollFrom)*bell(u)
	return false
}

// glideLength is how long a glide over d DIPs takes: measured from a browser, a
// base length that grows with the distance and a floor under it.
func glideLength(d float32) time.Duration {
	n := scrollGlideBase + time.Duration(absf(d)*float32(scrollGlideEvery))
	if n < scrollGlideMin {
		return scrollGlideMin
	}
	return n
}

// bell is the shape of a glide: nothing, everything, and a stop at each end,
// 6u⁵-15u⁴+10u³. Its slope peaks at 1.875 in the middle, so the column is
// fastest halfway and comes to rest at the place it was sent to.
func bell(u float32) float32 {
	if u <= 0 {
		return 0
	}
	if u >= 1 {
		return 1
	}
	return u * u * u * (u*(u*6-15) + 10)
}

// bellSlope is the slope of bell at u, in units of the bell's average rate.
func bellSlope(u float32) float32 {
	if u <= 0 || u >= 1 {
		return 0
	}
	return 30 * u * u * (1 - u) * (1 - u)
}

// gliding reports whether the column is on its way to a place it was sent to.
func (a *app) gliding() bool { return !a.scrollAt.IsZero() }

// stopScroll lands the column where it is drawn, as a press on the text does:
// the lines must not slide under a pointer that is selecting them.
func (a *app) stopScroll() {
	if a.gliding() {
		a.scrollAt = time.Time{}
		a.scrollPlan = false
		a.scrollRate = false
		a.scrollGoal = a.scrollY
		a.scrollVel = 0
	}
}

// stepScroll advances the column toward what a scroll asked for, by the time
// since the last frame. It runs in the build pass, so the draw callback still
// only replays the tiles the viewport now covers.
//
// The column covers a share of what it owes, set by scrollFollow: a single ask
// therefore eases into its place and stops, while asks that keep coming are
// followed at the rate they come at, with one ask's worth of lag. That is the
// same motion for a wheel, a key and a screenful, and it needs no knowledge of
// which of them asked.
//
// A glide is counted in device pixels, because that is what a screen has: a
// share of the distance smaller than a pixel is drawn as the picture the frame
// before it drew, and the reader sees the type stop dead and then hop. So the
// last frame of a glide takes the last pixel whole, and no frame that is still
// on its way takes less than one.
//
// It runs in the build pass, which is where the place is published: a frame that
// only paints moves the column with stepColumn and leaves the publishing to the
// build that lands (see drawSheet).
func (a *app) stepScroll() {
	if a.stepColumn() {
		a.afterScroll()
	}
}

// stepColumn is the motion itself: where the column stands after the time since
// the last frame, whether it landed there, and the tiles around it. It reports
// whether the column was moving at all.
//
// It is the part of a scroll that the moving frames need, and the part they may
// do while painting: it moves the picture and builds the display lists the
// picture is made of, and touches nothing the view shows. What the reader's
// place publishes — the panels' chapter, the store's reading position — is
// afterScroll's, and stays in the build pass.
func (a *app) stepColumn() bool {
	if a.scrollAt.IsZero() {
		return false
	}
	now := a.clock()
	from := a.scrollY
	raw := now.Sub(a.scrollAt)
	a.scrollAt = now
	if raw > scrollStarved {
		// The glide got no frames for longer than a stutter: the app was busy or
		// the window was not on screen. It arrives rather than resuming a crawl
		// the reader has long stopped watching.
		a.scrollY = a.scrollGoal
		a.scrollVel = 0
		a.scrollAt = time.Time{}
		a.scrollAhead()
		return true
	}
	if a.scrollPlan {
		// A planned glide is a place on a clock, not a debt paid down: it needs no
		// share of a frame and no pixels counted, and it arrives on time however
		// the frames fell.
		a.stepPlan(now)
		if a.landed() {
			// The last share of the bell is under a device pixel: the picture is
			// the one the place asked for draws, so the column stands there — and
			// the reading position is that place, not a share short of it.
			a.scrollY = a.scrollGoal
			a.scrollPlan = false
			a.scrollVel = 0
			a.scrollAt = time.Time{}
		}
		a.scrollAhead()
		return true
	}
	// The frame's time is delivered whole, so the motion is time-correct: a late
	// frame covers a late frame's distance rather than carrying two frames' worth
	// into the next one. The only gap that is cut is one past scrollStarved, and
	// that lands above.
	dt := raw
	gap := a.scrollGoal - a.scrollY
	pixel := a.pixel() // 0 where the column is not drawn on the grid
	if dt > 0 && gap != 0 {
		// Under a couple of pixels to go: the place asked for is on the screen
		// already, so the glide takes it now rather than spending frames on a
		// share of it that the screen would draw as one pixel at a time. A glide
		// always ends: every frame below moves the picture or lands.
		if pixel > 0 && absf(gap)*pixel < scrollLand {
			a.scrollY = a.scrollGoal
			a.scrollVel = 0
		} else if a.scrollRate {
			// A key is asking: one rate for the whole hold (see scrollKey).
			a.scrollY += a.stepRate(a.scrollGoal-a.scrollY, dt, pixel)
		} else {
			a.scrollY += a.step(a.scrollGoal-a.scrollY, dt, pixel)
		}
	}
	if a.landed() {
		// The column stands where it was sent to as far as the screen is
		// concerned: the frames left would draw the same pixels again.
		a.scrollY = a.scrollGoal
		a.scrollAt = time.Time{}
		a.scrollRate = false
	}
	if scrollDebug {
		fmt.Printf("[scroll] dt=%5.1fms  dy=%+7.2f  y=%8.1f  gap=%+7.2f  rate=%v  vel=%6.0f\n",
			float64(raw.Microseconds())/1000, a.scrollY-from, a.scrollY,
			a.scrollGoal-a.scrollY, a.scrollRate, a.scrollVel)
	}
	a.scrollAhead()
	return true
}

// pace is how one kind of ask moves the column: the time its debt is covered
// over, how long the speed takes to come up to it, and the ceiling on that
// speed, 0 when the debt is the only limit.
type pace struct {
	over time.Duration
	ramp time.Duration
	top  float32
}

// step is how far the column moves this frame, in DIPs, toward the gap it owes,
// when the asks come as a stream: a wheel spun hard, a trackpad. The speed is the
// one the gap implies — the debt covered over scrollFollow — and the debt is the
// only ceiling, so a place far away is travelled rather than whipped. That speed
// is not taken up at once: it is come up to over scrollRamp, which is what a
// notch needs, because one arrives as a whole distance in a single frame and a
// column that takes it at once covers a share of it in the frame, which is the
// lurch a reader sees before the glide settles. A stream of asks keeps the speed
// it had, so a wheel joins whatever it had.
//
// Every frame moves the picture: a share of the debt smaller than a pixel is
// drawn as the frame before it, so the step is at least one pixel while the debt
// is at least one pixel, and the caller lands the column rather than crawl.
func (a *app) step(gap float32, dt time.Duration, pixel float32) float32 {
	return a.stepToward(gap, dt, pixel, pace{over: scrollFollow, ramp: scrollRamp})
}

// stepToward is the motion both shapes of an ask are made of: the speed the debt
// implies, come up to over p.ramp and held under p.top, never further than the
// debt itself — and at least one device pixel while there is a pixel of it left,
// so that every frame of a glide moves the picture.
func (a *app) stepToward(gap float32, dt time.Duration, pixel float32, p pace) float32 {
	secs := dt.Seconds()
	want := float64(gap) / p.over.Seconds()
	if top := float64(p.top); top > 0 && math.Abs(want) > top {
		want = math.Copysign(top, want)
	}
	a.scrollVel += float32((want - float64(a.scrollVel)) * math.Min(1, secs/p.ramp.Seconds()))
	move := float64(a.scrollVel) * secs
	if math.Abs(move) > math.Abs(float64(gap)) {
		move = float64(gap)
	}
	if pixel > 0 && math.Abs(move)*float64(pixel) < 1 {
		move = math.Copysign(1/float64(pixel), float64(gap))
	}
	if math.Abs(move) > math.Abs(float64(gap)) {
		move = float64(gap)
	}
	return float32(move)
}

// pixel is how many device pixels a DIP is where the column is drawn, or 0 when
// that is not known or the snapping is off — the two cases in which a glide has
// no pixels to count.
func (a *app) pixel() float32 {
	if !snapScroll || !(a.devScale > 0) {
		return 0
	}
	return a.devScale
}

// landed reports whether the column, drawn where it is, already stands at the
// place it was sent to: the picture of the two is the same, so the frames left
// would draw the same pixels again.
func (a *app) landed() bool {
	pixel := a.pixel()
	if pixel <= 0 {
		return absf(a.scrollGoal-a.scrollY) < scrollGapMin
	}
	return snapDevices(a.viewTop-a.scrollGoal, pixel) == snapDevices(a.viewTop-a.scrollY, pixel)
}

// scrollLines moves the column by lines lines of type, as one press of an arrow
// key asks for them. Under scrollUniform the press sets a rate rather than a
// glide of its own: see scrollKey.
func (a *app) scrollLines(lines float32) {
	line := a.opts.FontSize * a.opts.LineHeight
	if scrollUniform {
		a.scrollKey(lines * line)
		return
	}
	a.scrollBy(lines * line)
}

// scrollKey moves the column by d DIPs, as one arrow key press asks for them.
//
// A key that repeats is the reader walking down the column and not a series of
// asks to arrive at places, so what it sets here is a rate: the frames hold it
// for as long as the presses keep coming, and one press is still a step of d.
// The debt is kept at one step and no more, so a tap lands where it asked and a
// key held down cannot run away from the last of its repeats.
func (a *app) scrollKey(d float32) {
	if a.layout == nil || d == 0 {
		return
	}
	a.chromeFollow(d)
	a.dismissCover()
	a.jumpAt = false
	if a.instant {
		a.scrollNow(a.scrollY + d)
		return
	}
	step := absf(d)
	goal := a.scrollGoal + d
	if d > 0 {
		goal = min(goal, a.scrollY+step)
	} else {
		goal = max(goal, a.scrollY-step)
	}
	a.scrollGoal = clampf(goal, 0, a.maxScroll())
	a.scrollRate = true
	a.scrollPlan = false
	if a.scrollAt.IsZero() {
		a.scrollAt = a.clock()
		a.scrollVel = 0
	}
	a.scrollAhead()
}

// keyRate is the speed a key moves the column at, in DIPs a second: the same
// three lines a tenth of a second whatever the reading size.
func (a *app) keyRate() float32 {
	line := a.opts.FontSize * a.opts.LineHeight
	return float32(scrollKeyLines) * line / float32(scrollKeyOver.Seconds())
}

// stepRate is the same integrator while a key is asking: one rate for every
// frame of the hold, come up to over scrollKeyRamp and held under the rate the
// key sets. There is no bell and no controller here — the reader walking down a
// column holds one speed — and the debt left when the presses stop is given back
// over scrollKeyTail, so the stop is a settle rather than a cut.
func (a *app) stepRate(gap float32, dt time.Duration, pixel float32) float32 {
	return a.stepToward(gap, dt, pixel, pace{
		over: scrollKeyTail, ramp: scrollKeyRamp, top: a.keyRate(),
	})
}

// scrollScreens moves the column by screens screens, keeping a slice of the
// last one in view so the reader can see the two meet.
func (a *app) scrollScreens(screens int) {
	a.scrollBy(float32(screens) * a.scrollViewport() * 0.9)
}

// scrollAhead builds the tiles around the viewport. It runs between frames, so
// the draw callback only replays display lists that already exist.
func (a *app) scrollAhead() {
	if a.layout == nil || a.tileH <= 0 {
		return
	}
	first := int(a.scrollY / a.tileH)
	last := int((a.scrollY + a.scrollViewport()) / a.tileH)
	a.layout.Tiles(first-1, last+2)
}

// afterScroll publishes the new place to the panels and the store. The panels
// only care about the chapter, so their rows are rebuilt when it changes rather
// than on every wheel event; the store's write is debounced on its own.
func (a *app) afterScroll() {
	if a.pan == nil || a.layout == nil {
		return
	}
	// Writing the place down happens on the UI thread inside the build pass, so
	// the log counts it apart from the rest of the frame.
	if perf != nil {
		start := time.Now()
		defer func() { perf.record(time.Since(start)) }()
	}
	loc, ok := a.currentLoc()
	if !ok {
		return
	}
	if loc.Chapter != a.scrollChapter {
		a.scrollChapter = loc.Chapter
		a.pan.RefreshAt(a.book, a.layout, loc)
	}
	a.recordScroll(a.layout, loc)
}

// atScrollEnd reports whether the column shows the end of the flow. maxScroll
// is the largest offset the column can stand at; a flow shorter than the
// viewport is at its end from the start.
func (a *app) atScrollEnd() bool {
	max := a.maxScroll()
	return max <= 0.5 || a.scrollY >= max-0.5
}

// recordScroll writes the current place down, marking the book finished only
// when the column actually reached the end. Scrolling back clears the mark.
func (a *app) recordScroll(layout *render.Layout, loc render.Location) {
	if a.pan == nil {
		return
	}
	if a.atScrollEnd() {
		a.pan.RecordAtEnd(layout, loc)
		return
	}
	a.pan.RecordAt(layout, loc)
}

// currentLoc is the reading position in the layout's own coordinates: the top
// of the scrolling column, or the first line of the page shown.
func (a *app) currentLoc() (render.Location, bool) {
	if a.layout == nil {
		return render.Location{}, false
	}
	if a.scrolling() {
		return a.layout.LocationAt(clampf(a.scrollY, 0, a.layout.ContentHeight()))
	}
	if loc, ok := a.layout.LocationOfPage(a.page); ok {
		return loc, true
	}
	return render.Location{}, false
}

// currentChapter is the chapter the reader is in, numbered from zero.
func (a *app) currentChapter() int {
	if loc, ok := a.currentLoc(); ok {
		return max(loc.Chapter, 0)
	}
	return 0
}

// scrollProgress is how far down the column the reader is, from 0 to 1: the
// position of the viewport in the whole travel, so it starts at 0 and ends at 1.
func (a *app) scrollProgress() float64 {
	travel := a.maxScroll()
	if travel <= 0 {
		return 0
	}
	return float64(clampf(a.scrollY, 0, travel) / travel)
}

// drawScroll paints the tiles of the scrolling column that the viewport shows.
// It runs inside the box's Draw callback, so it only replays display lists it
// already has.
func (a *app) drawScroll(p *ui.Painter, r ui.Rect) {
	column, y, tileH := a.columnPlan()
	if column == nil || a.paint == nil || tileH <= 0 {
		return
	}
	view := ui.Rect{X: r.X, Y: r.Y + scrollInset, W: r.W, H: a.scrollViewport()}
	if view.H < 1 {
		return
	}
	// The column is placed on whole device pixels. A glyph is rasterized once
	// per subpixel phase, and its quad is snapped to the pixel grid anyway, so an
	// offset in between two pixels swaps every glyph in the column for a near
	// copy of itself and moves each of them by up to half a pixel — every frame,
	// for as long as the gesture lasts. That is the breathing this used to do:
	// crisp in any one frame, unreadable as motion. Snapping holds one phase for
	// the whole gesture, the way a compositor moves a layer it painted once.
	//
	// Every tile has to stand at that same phase, so what is snapped is the
	// screen position of the flow's top edge and not the content offset: tile
	// tops are whole pixels apart (layoutSheet), so they only share one phase if
	// the edge they hang from does.
	y = clampf(y, 0, max(column.ContentHeight()-view.H, 0))
	top := snapDevices(view.Y-y, p.Scale())
	// Where the column is drawn, and at what ratio, is what says whether a glide
	// has anything left to show (see landed). The viewport itself does not move,
	// so this is a property of the window and of the screen.
	a.viewTop, a.devScale = view.Y, p.Scale()
	first := max(int(y/tileH)-1, 0)
	last := int((y + view.H) / tileH)
	if n := column.TileCount(); last >= n {
		last = n - 1
	}
	// Paint from the bottom up. Every tile starts with its own opaque sheet, and
	// a line belongs to the tile that holds its top edge, so a line near a tile
	// boundary overhangs into the next tile's area: drawing the lower tile
	// first lets that overhang land on top of its paper instead of being
	// covered by it.
	for i := last; i >= first; i-- {
		tile := column.Tile(i)
		if tile == nil {
			continue
		}
		box := ui.Rect{X: view.X, Y: top + tile.Top(), W: view.W, H: tile.Height()}
		// The performance log keeps how long each tile took: a tile built on the
		// frame it is shown is what a scroll stutter looks like.
		perf.tileAt(i, func() { a.paint.DrawTile(p, tile, box, view) })
	}
	// The marks are placed from the same top edge as the tiles, so the veils lie
	// on the glyphs they belong to in the frame they are drawn.
	if a.marksOn == column {
		a.drawScrollMarks(p, view, view.Y-top)
	}
}

// snapScroll, cleared by LAYX_NO_SNAP, is the switch a reader flips to feel what
// the snapping is worth: off, the column moves in subpixel steps again.
var snapScroll = os.Getenv("LAYX_NO_SNAP") == ""

// absf is how far v is from zero.
func absf(v float32) float32 { return float32(math.Abs(float64(v))) }

// snapDevices rounds a screen position or a length to the pixels of the screen,
// as a compositor moves a layer it painted: the column travels in steps of one
// device pixel. scale is the device ratio, so a Retina column moves in half DIPs
// and a plain one in whole DIPs.
func snapDevices(v, scale float32) float32 {
	if !snapScroll {
		return v
	}
	return roundDevices(v, scale)
}

// roundDevices rounds v to the pixels of the screen, whatever snapScroll says:
// the grid the column is placed on and the one its tiles are cut on have to be
// the same grid, or the tiles stand at phases of their own.
func roundDevices(v, scale float32) float32 {
	if !(scale > 0) {
		return v
	}
	return float32(math.Round(float64(v)*float64(scale))) / scale
}

// drawScrollMarks paints the notes, the search hits and the selection over the
// column. It only fills rectangles it already has: the veils are rebuilt when
// the layout or the results change, never while drawing.
func (a *app) drawScrollMarks(p *ui.Painter, view ui.Rect, y float32) {
	pal := reader.Current()
	// The notes sit under what the reader is acting on now, as they do on a
	// page: the hits and the selection are painted over them.
	a.drawScrollNoteMarks(p, view, y)
	on := -1
	if a.pan != nil {
		on = a.pan.Search.Selected
	}
	for i, quads := range a.hitQuads {
		colour := pal.Match
		if i == on {
			colour = pal.MatchOn
		}
		a.paint.FillScrollQuads(p, view, quads, colour, y)
	}
	a.paint.FillScrollQuads(p, view, a.selQuads, pal.Select, y)
}

// scrollLocation maps a point in the column's own coordinates — as the draw
// callback and the pointer events receive them — to a document location. The
// x is measured from the element's left edge, so the page margin comes off; the
// y is below the head inset and the flow has already scrolled by scrollY.
func (a *app) scrollLocation(x, y float32) (render.Location, bool) {
	column, y0, _ := a.columnPlan()
	if column == nil {
		return render.Location{}, false
	}
	m := column.Margins()
	return column.HitTest(paint.Point{X: x - m.Left, Y: y0 + (y - scrollInset)})
}

// scrollSelectAll selects the lines the column is showing, the scroll-mode
// counterpart of selecting the page.
func (a *app) scrollSelectAll() {
	column, y, _ := a.columnPlan()
	if column == nil {
		return
	}
	v := column.Viewport()
	m := column.Margins()
	from, ok1 := column.HitTest(paint.Point{X: 1, Y: y + 1})
	to, ok2 := column.HitTest(paint.Point{
		X: v.Width - m.Left - m.Right - 1,
		Y: y + a.scrollViewport() - 1,
	})
	if !ok1 || !ok2 {
		return
	}
	a.sel.Start(from)
	a.sel.Extend(to)
	a.selQuads = a.sel.Quads(column)
	a.selCopied = 0
}

// scrollLabel is the folio's name for a scroll position: how far through the
// book the column is, as a percentage.
func (a *app) scrollLabel() string {
	if a.layout == nil {
		return "—"
	}
	return locales.ScrollPercent(int(a.scrollProgress()*100 + 0.5))
}

// chapterScreensLeft is how much of the chapter is below the column, counted in
// viewports: the scroll-mode reading of "pages left in chapter".
func (a *app) chapterScreensLeft() string {
	if a.layout == nil {
		return ""
	}
	chapter := a.currentChapter()
	end := a.layout.ContentHeight()
	if top, ok := a.layout.ChapterContentTop(chapter + 1); ok {
		end = top
	}
	view := a.scrollViewport()
	left := end - (a.scrollY + view)
	if left <= 0 {
		return locales.ChapterLastPage()
	}
	screens := int((left + view - 1) / view)
	if screens == 1 {
		return locales.ChapterOnePageLeft()
	}
	return locales.ChapterPagesLeft(screens)
}

// resumeScrollNote is the status line a scroll-mode book opens with: how far
// into the book the saved position is, and which store path found it.
func resumeScrollNote(l *render.Layout, loc render.Location, how string) string {
	if p, ok := l.Progress(loc); ok {
		return locales.ResumeScroll(int(p*100+0.5), resumeHow(how))
	}
	return locales.ResumeScrollHere(resumeHow(how))
}

// resumeHow names the store's degradation path in the language the app shows,
// so the note the reader opens with says where the position came from.
func resumeHow(how string) string {
	switch how {
	case "node":
		return locales.HowNode()
	case "anchor":
		return locales.HowAnchor()
	case "progress":
		return locales.HowProgress()
	}
	return how
}
