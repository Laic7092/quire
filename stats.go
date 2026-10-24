package main

import (
	"fmt"
	"time"

	"github.com/laix/layx-reader/internal/store"
)

// The reading goals the home screen measures against, when the reader has not
// set their own: a book a month, and five days of reading a week.
const (
	defaultGoalBooks = 12
	defaultGoalDays  = 5
)

// readingStats is the home screen's record of the reader: the days read this
// week against the week's goal, the books finished this year against the year's
// goal, and how many days in a row the reader has come back.
type readingStats struct {
	DaysThisWeek  int
	WeekGoal      int
	BooksThisYear int
	BooksGoal     int
	// Days is every day the reader opened a book, and Finished every book read
	// to its last page; Streak is the days in a row up to today (or up to
	// yesterday, while today is still unread).
	Days     int
	Finished int
	Streak   int
}

// weekProgress is how much of the week's goal is done, from 0 to 1.
func (s readingStats) weekProgress() float32 {
	if s.WeekGoal <= 0 {
		return 0
	}
	return min(1, float32(s.DaysThisWeek)/float32(s.WeekGoal))
}

// yearProgress is how much of the year's goal is done, from 0 to 1.
func (s readingStats) yearProgress() float32 {
	if s.BooksGoal <= 0 {
		return 0
	}
	return min(1, float32(s.BooksThisYear)/float32(s.BooksGoal))
}

// readingStats reads the store: a day counts as read when a book was opened or a
// page recorded on it, and a book counts as finished when its last recorded
// position is the end of it. now is the day the week and the streak are
// measured against, so a test can pin it.
func (a *app) readingStats(now time.Time) readingStats {
	s := readingStats{WeekGoal: a.goalDays, BooksGoal: a.goalBooks}
	if s.WeekGoal <= 0 {
		s.WeekGoal = defaultGoalDays
	}
	if s.BooksGoal <= 0 {
		s.BooksGoal = defaultGoalBooks
	}
	if a.st == nil {
		return s
	}
	state := a.st.State()

	days := map[string]bool{}
	for _, b := range state.Books {
		if !b.LastOpened.IsZero() {
			days[dayKey(b.LastOpened)] = true
		}
	}
	finished := map[string]bool{}
	for _, b := range state.Books {
		p, ok := state.Positions[b.Key]
		if !ok || p.Progress < 1 {
			continue
		}
		// A book revised on disk has a new key but the same path: it is still
		// one book on the shelf.
		if finished[b.Path] {
			continue
		}
		finished[b.Path] = true
		s.Finished++
		if p.Updated.Year() == now.Year() {
			s.BooksThisYear++
		}
	}
	for _, p := range state.Positions {
		if !p.Updated.IsZero() {
			days[dayKey(p.Updated)] = true
		}
	}

	s.Days = len(days)
	weekStart := startOfWeek(now)
	for i := 0; i < 7; i++ {
		if days[dayKey(weekStart.AddDate(0, 0, i))] {
			s.DaysThisWeek++
		}
	}
	// A streak counts back from today; before the reader has read today, it
	// still counts the run that ended yesterday.
	day := now
	if !days[dayKey(day)] {
		day = day.AddDate(0, 0, -1)
	}
	for days[dayKey(day)] {
		s.Streak++
		day = day.AddDate(0, 0, -1)
	}
	return s
}

// dayKey names a calendar day, so two readings on the same day count once
// whatever time of day they were.
func dayKey(t time.Time) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}

// startOfWeek is the Monday of t's week, at midnight: the week the home screen's
// ring counts.
func startOfWeek(t time.Time) time.Time {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	back := (int(day.Weekday()) + 6) % 7 // Sunday is 0, Monday 1
	return day.AddDate(0, 0, -back)
}

// setGoals stores the reading goals the reader picked and saves them with the
// rest of the preferences.
func (a *app) setGoals(books, days int) {
	if books <= 0 {
		books = defaultGoalBooks
	}
	if days <= 0 {
		days = defaultGoalDays
	}
	if books == a.goalBooks && days == a.goalDays {
		return
	}
	a.goalBooks, a.goalDays = books, days
	if a.st == nil {
		return
	}
	a.st.Update(func(s *store.State) {
		s.Prefs.GoalBooks = books
		s.Prefs.GoalDays = days
	})
	a.st.SaveSoon(0)
}
