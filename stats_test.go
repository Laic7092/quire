package main

import (
	"testing"
	"time"

	"github.com/laix/layx-reader/internal/store"
)

// The library's measures come from the window, not from one screenshot: the
// prototype's 818 DIP window is the reference, and wider or narrower windows
// keep the proportions within the clamps.
func TestHomeMetricsFollowTheWindow(t *testing.T) {
	near := func(got, want float32) bool {
		d := got - want
		return d < 1 && d > -1
	}

	ref := homeMetricsFor(818, true)
	if !near(ref.side, 160) {
		t.Errorf("818 DIP window: sidebar %g, want about 160", ref.side)
	}
	if !near(ref.padding, 32) {
		t.Errorf("818 DIP window: margin %g, want about 32", ref.padding)
	}
	if !near(ref.coverW, 80) {
		t.Errorf("818 DIP window: cover %g wide, want about 80", ref.coverW)
	}
	if ratio := ref.coverH / ref.coverW; ratio != coverAspect {
		t.Errorf("cover ratio %g, want %g", ratio, coverAspect)
	}
	if ref.ring <= ref.coverH*0.5 || ref.ring >= ref.coverH*1.5 {
		t.Errorf("ring %g does not sit between half and one and a half covers (%g)", ref.ring, ref.coverH)
	}

	// A wider window gets a wider source list and larger covers, up to the
	// clamps that keep them from taking over the page.
	wide := homeMetricsFor(1600, true)
	if wide.side != 224 {
		t.Errorf("1600 DIP window: sidebar %g, want the 224 cap", wide.side)
	}
	if wide.coverW != 140 {
		t.Errorf("1600 DIP window: cover %g wide, want the 140 cap", wide.coverW)
	}
	if wide.padding != 40 {
		t.Errorf("1600 DIP window: margin %g, want the 40 cap", wide.padding)
	}

	// A narrow window keeps a readable source list and covers rather than
	// shrinking them away.
	narrow := homeMetricsFor(640, true)
	if narrow.side != 160 {
		t.Errorf("640 DIP window: sidebar %g, want the 160 floor", narrow.side)
	}
	if narrow.coverW != 76 {
		t.Errorf("640 DIP window: cover %g wide, want the 76 floor", narrow.coverW)
	}

	// With the source list hidden the page takes the whole window: no sidebar
	// and covers measured on the wider content.
	full := homeMetricsFor(818, false)
	if full.side != 0 {
		t.Errorf("hidden sidebar: width %g, want 0", full.side)
	}
	if full.coverW <= ref.coverW {
		t.Errorf("hidden sidebar: covers %g wide, want more than %g", full.coverW, ref.coverW)
	}
}

// The goals ring and the foot of the home page count what the store really
// holds: a day counts once however many readings it has, a revised book counts
// once, and only this year's finishes count towards the year.
func TestReadingStatsCountTheStore(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 5, 20, 15, 0, 0, 0, time.Local) // a Wednesday
	sunday := now.AddDate(0, 0, -3)
	monday := now.AddDate(0, 0, -2)
	tuesday := now.AddDate(0, 0, -1)
	key := func(name string) string { return name + "-key" }

	st.Update(func(s *store.State) {
		s.Books = []store.Book{
			{Key: key("a"), Path: "/books/a.epub", Title: "A", LastOpened: monday},
			{Key: key("b"), Path: "/books/b.epub", Title: "B", LastOpened: now},
			// The same book after a new revision: both entries must count once.
			{Key: key("a2"), Path: "/books/a.epub", Title: "A", LastOpened: monday},
			{Key: key("c"), Path: "/books/c.epub", Title: "C", LastOpened: sunday},
		}
		s.Positions = map[string]store.Position{
			key("a"):  {Progress: 1, Updated: tuesday},
			key("a2"): {Progress: 1, Updated: tuesday},
			key("b"):  {Progress: 0.4, Updated: now},
			key("c"):  {Progress: 1, Updated: time.Date(2025, 12, 30, 9, 0, 0, 0, time.Local)},
		}
	})

	a := &app{st: st, goalBooks: 12, goalDays: 5}
	got := a.readingStats(now)
	if got.DaysThisWeek != 3 {
		t.Errorf("days this week = %d, want 3 (Mon, Tue, Wed)", got.DaysThisWeek)
	}
	if got.WeekGoal != 5 || got.BooksGoal != 12 {
		t.Errorf("goals = %d days, %d books; want 5 and 12", got.WeekGoal, got.BooksGoal)
	}
	if got.Days != 5 {
		t.Errorf("days read = %d, want 5 (including the day the old book was finished)", got.Days)
	}
	if got.Streak != 4 {
		t.Errorf("streak = %d, want 4 (Sun through Wed)", got.Streak)
	}
	if got.Finished != 2 {
		t.Errorf("finished = %d, want 2 (the revisions of A count once)", got.Finished)
	}
	if got.BooksThisYear != 1 {
		t.Errorf("books this year = %d, want 1", got.BooksThisYear)
	}
	if p := got.weekProgress(); p != 3.0/5.0 {
		t.Errorf("week progress = %g, want 0.6", p)
	}
	if p := got.yearProgress(); p != 1.0/12.0 {
		t.Errorf("year progress = %g, want 1/12", p)
	}
}

// Before the reader read today the streak still counts the run that ended
// yesterday, and a book finished exactly at the goal fills the ring rather than
// overflowing it.
func TestReadingStatsStreakAndGoalClamp(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local)
	st.Update(func(s *store.State) {
		s.Books = []store.Book{{Key: "k", Path: "/books/a.epub", LastOpened: now.AddDate(0, 0, -1)}}
		s.Positions = map[string]store.Position{
			"k": {Progress: 1, Updated: now.AddDate(0, 0, -1)},
			"x": {Progress: 1, Updated: now.AddDate(0, 0, -1)},
			"y": {Progress: 1, Updated: now.AddDate(0, 0, -1)},
		}
	})

	a := &app{st: st, goalBooks: 1, goalDays: 5}
	got := a.readingStats(now)
	if got.Streak != 1 {
		t.Errorf("streak = %d, want 1 from yesterday, with today unread", got.Streak)
	}
	if got.DaysThisWeek != 1 {
		t.Errorf("days this week = %d, want 1", got.DaysThisWeek)
	}
	if got.BooksThisYear < 1 {
		t.Errorf("books this year = %d, want at least 1", got.BooksThisYear)
	}
	if p := got.yearProgress(); p != 1 {
		t.Errorf("year progress = %g, want it clamped at 1", p)
	}
}

// A goal is stored with the preferences and comes back on the next launch.
func TestGoalsPersist(t *testing.T) {
	path := t.TempDir() + "/state.json"
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, goalBooks: defaultGoalBooks, goalDays: defaultGoalDays}
	a.setGoals(24, 7)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	again, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	prefs := again.State().Prefs
	if prefs.GoalBooks != 24 || prefs.GoalDays != 7 {
		t.Errorf("stored goals = %d books, %d days; want 24 and 7", prefs.GoalBooks, prefs.GoalDays)
	}
}
