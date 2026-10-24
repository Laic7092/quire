// Package store persists the state that must survive a restart: the shelf, the
// reading position of every book and the reading preferences.
//
// Everything lives in one JSON file. The package is pure logic: it does not
// import the UI package, does not own a window and does not start goroutines
// other than the single debounce timer behind SaveSoon.
//
// On-disk format: one State value, indented JSON, schema Version 1. Missing
// files are not an error (a fresh install has no state); a corrupt file is
// copied to "<path>.bak" and replaced by an empty State so that a broken write
// never costs the user the whole shelf on the next launch.
//
// Concurrency: every method is safe for concurrent use. Update, SetPosition
// and TouchBook mutate the in-memory state under a lock; Save, Flush and the
// debounce timer serialize their writes through a second lock, so a snapshot
// is always written whole.
package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Version is the current on-disk schema version.
const Version = 1

const (
	// appDir is the per-user directory name under the config root.
	appDir = "layx-reader"
	// fileName is the state file name inside appDir.
	fileName = "state.json"
)

// Book is one shelf / recently-opened entry.
type Book struct {
	// Key is the stable identity of this revision of the file; see Key.
	Key        string    `json:"key"`
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	Format     string    `json:"format"` // "epub" or "txt"
	Size       int64     `json:"size"`
	Modified   time.Time `json:"modified"`
	Added      time.Time `json:"added"`
	LastOpened time.Time `json:"lastOpened"`
}

// Position is a reading position that has to survive a reflow. Node and Offset
// are exact within one built Document, Anchor is stable across rebuilds (when
// the builder provides one) and Progress is the last-resort fallback.
type Position struct {
	Chapter  int       `json:"chapter"`
	Node     uint32    `json:"node"`   // render.NodeID; 0 means "not recorded"
	Anchor   uint32    `json:"anchor"` // render.AnchorID; 0 means NoAnchor
	Offset   int       `json:"offset"` // rune offset inside Node
	Progress float32   `json:"progress"`
	Updated  time.Time `json:"updated"`
}

// Bookmark is a place in a book that the reader kept. Position carries the
// reflow-stable place; Label is the reader's own name for it, empty when the
// reader never renamed it and the host should show the chapter instead.
//
// Created is when the bookmark was made; Position.Updated is not meaningful
// here and is written as the same instant.
type Bookmark struct {
	ID       string    `json:"id"`
	Position Position  `json:"position"`
	Label    string    `json:"label"`
	Created  time.Time `json:"created"`
}

// Note is a passage the reader marked, with the comment they wrote about it.
// From and To are the two ends of the passage as it was selected (either order
// works); Text is the passage itself, kept so the note can be recognised after
// a relayout or a new revision of the book.
//
// Comment may be empty: a bare highlight is a note without one.
type Note struct {
	ID       string    `json:"id"`
	From     Position  `json:"from"`
	To       Position  `json:"to"`
	Text     string    `json:"text"`
	Comment  string    `json:"comment"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
}

// Prefs are the reading preferences worth restoring on launch.
type Prefs struct {
	FontSize float32 `json:"fontSize"`
	// LineHeight is the leading, as a multiple of the size.
	LineHeight float32 `json:"lineHeight"`
	// Font is the face: "serif" or "sans".
	Font string `json:"font"`
	// Justify stretches lines to the measure.
	Justify bool `json:"justify"`
	// Margin is the page inset in DIPs.
	Margin float32 `json:"margin"`
	// Spread shows two pages side by side.
	Spread bool `json:"spread"`
	// Scroll reads the book as one continuous column instead of pages.
	Scroll bool `json:"scroll"`
	// Theme is the paper: "white", "sepia", "gray" or "night", empty while the
	// reader follows the system's appearance.
	Theme string `json:"theme"`
	// Sidebar is the panel that was open last: "shelf", "toc", "search" or "".
	Sidebar string `json:"sidebar"`
	// SidebarOpen remembers whether that panel was showing when the reader
	// closed, so the next launch looks the same.
	SidebarOpen bool `json:"sidebarOpen"`
	// SidebarWidth is the width the user dragged the panel to, in DIPs; zero
	// means the window's default.
	SidebarWidth float32 `json:"sidebarWidth"`
	// GoalBooks is the home screen's yearly reading goal, in books; GoalDays
	// is how many days a week the reader means to read. Zero means the
	// default, so a state file written before the goals existed still opens.
	GoalBooks int `json:"goalBooks"`
	GoalDays  int `json:"goalDays"`
}

// State is the whole persisted document.
type State struct {
	Version   int                   `json:"version"`
	Books     []Book                `json:"books"`
	Positions map[string]Position   `json:"positions"` // Book.Key -> Position
	Bookmarks map[string][]Bookmark `json:"bookmarks"` // Book.Key -> bookmarks
	Notes     map[string][]Note     `json:"notes"`     // Book.Key -> notes
	Prefs     Prefs                 `json:"prefs"`
	LastOpen  string                `json:"lastOpen"` // Book.Key
}

// ErrClosed is returned by Save and Flush after Close.
var ErrClosed = errors.New("store: closed")

// DefaultPath returns the state file path:
// $XDG_CONFIG_HOME/layx-reader/state.json, else ~/.config/layx-reader/state.json.
// The directory is not created; Save does that.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, appDir, fileName), nil
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, appDir, fileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: config dir: %w", err)
	}
	return filepath.Join(home, ".config", appDir, fileName), nil
}

// Store owns the state of one file. Create it with Open.
type Store struct {
	path string

	// writeMu serializes disk writes; mu guards the in-memory state.
	writeMu sync.Mutex
	mu      sync.Mutex

	state   State
	gen     uint64 // bumped by every mutation; used to clear dirty safely
	dirty   bool
	closed  bool
	timer   *time.Timer
	lastErr error

	// writes counts completed saves. Tests use it to prove that SaveSoon
	// coalesces; it is meaningless outside tests.
	writes int64
}

// Open reads path. A missing or empty file yields an empty State; a file that
// is not valid JSON is copied to path+".bak" and replaced by an empty State.
// Both are reported as success: a fresh install and a damaged file are states
// the reader must start in, not errors.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty path")
	}
	s := &Store{path: path, state: emptyState()}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		if berr := backup(path, data); berr != nil {
			return nil, fmt.Errorf("store: %s is not valid JSON (%v) and the backup failed: %w", path, err, berr)
		}
		return s, nil
	}
	normalize(&st)
	s.state = st
	return s, nil
}

// Path returns the file this store writes.
func (s *Store) Path() string { return s.path }

// State returns a deep copy of the current state; the caller may mutate it
// freely. Callers that want their changes persisted use Update instead.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

// Update mutates the state under the store lock and marks it dirty. fn must not
// retain the pointer it receives. Updates after Close are dropped.
func (s *Store) Update(fn func(*State)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	fn(&s.state)
	if s.state.Positions == nil {
		s.state.Positions = map[string]Position{}
	}
	s.gen++
	s.dirty = true
}

// Save writes the current state atomically: a temp file in the same directory,
// fsync, then rename. It does not clear pending debounce state.
func (s *Store) Save() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return s.save(true)
}

// SaveSoon schedules a write after d. Repeated calls replace the pending
// deadline, so a burst of page turns costs one write. d <= 0 writes through
// the same timer immediately.
func (s *Store) SaveSoon(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(d, s.fire)
		return
	}
	s.timer.Reset(d)
}

// Flush writes pending changes immediately and cancels the debounce timer. It
// is a no-op when nothing is dirty.
func (s *Store) Flush() error {
	s.stopTimer()
	s.mu.Lock()
	closed, dirty := s.closed, s.dirty
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if !dirty {
		return nil
	}
	return s.save(false)
}

// Close flushes pending changes and releases the timer. It is idempotent; Save
// and Flush return ErrClosed afterwards and Update becomes a no-op.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	timer, dirty := s.timer, s.dirty
	s.timer = nil
	s.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	if !dirty {
		return nil
	}
	return s.save(false)
}

// LastError returns the error of the most recent save, or nil. Background
// saves (SaveSoon, Close) have no caller to report to, so they leave it here.
func (s *Store) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Key returns a stable identity for one revision of a file: the first 16 hex
// digits of sha256(absolute path | size | mtime in unix nanoseconds). Touching
// the file (including copying it) changes the key, which is what lets the shelf
// notice that a book was re-downloaded or edited.
func Key(path string, size int64, modified time.Time) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%d", filepath.Clean(abs), size, modified.UnixNano())
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// TouchBook upserts b by Key and stamps LastOpened (and Added on first insert).
// It also makes b the LastOpen book. A book whose file was edited or
// re-downloaded has a new Key but the same path, and keeps its shelf row: the
// old revision is replaced, its Added time kept, and its position, bookmarks
// and notes move to the new key rather than being stranded under the old one.
func (s *Store) TouchBook(b Book) {
	now := time.Now()
	s.Update(func(st *State) {
		if b.Added.IsZero() {
			b.Added = now
		}
		b.LastOpened = now
		for i := range st.Books {
			if st.Books[i].Key != b.Key {
				continue
			}
			if !st.Books[i].Added.IsZero() {
				b.Added = st.Books[i].Added
			}
			st.Books[i] = b
			st.LastOpen = b.Key
			return
		}
		if i := samePathBook(st.Books, b.Path); i >= 0 {
			old := st.Books[i]
			if !old.Added.IsZero() {
				b.Added = old.Added
			}
			st.Books[i] = b
			moveReading(st, old.Key, b.Key)
			st.LastOpen = b.Key
			return
		}
		st.Books = append(st.Books, b)
		st.LastOpen = b.Key
	})
}

// samePathBook returns the index of the book that names path, or -1. Paths are
// compared cleaned and absolute, so the same file opened through two spellings
// is one book.
func samePathBook(books []Book, path string) int {
	want := cleanPath(path)
	for i := range books {
		if cleanPath(books[i].Path) == want {
			return i
		}
	}
	return -1
}

func cleanPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// moveReading carries what the reader recorded about one revision of a file to
// the next one. It runs inside Update, with the store lock already held.
func moveReading(st *State, from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	if p, ok := st.Positions[from]; ok {
		st.Positions[to] = p
		delete(st.Positions, from)
	}
	if rows, ok := st.Bookmarks[from]; ok {
		st.Bookmarks[to] = rows
		delete(st.Bookmarks, from)
	}
	if rows, ok := st.Notes[from]; ok {
		st.Notes[to] = rows
		delete(st.Notes, from)
	}
}

// Recent returns the shelf ordered by LastOpened, newest first. n <= 0 returns
// every book.
func (s *Store) Recent(n int) []Book {
	books := s.State().Books
	sort.SliceStable(books, func(i, j int) bool {
		return books[i].LastOpened.After(books[j].LastOpened)
	})
	if n > 0 && len(books) > n {
		books = books[:n]
	}
	return books
}

// Position returns the saved position for key.
func (s *Store) Position(key string) (Position, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Positions[key]
	return p, ok
}

// BookmarksOf returns key's bookmarks in the order the reader made them.
func (s *Store) BookmarksOf(key string) []Bookmark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Bookmark(nil), s.state.Bookmarks[key]...)
}

// AddBookmark stores b under key and returns it with its ID and Created
// stamped. A bookmark at the very place b names is not stored twice: the
// existing one is returned instead, so pressing add twice cannot pile rows on
// one page.
func (s *Store) AddBookmark(key string, b Bookmark) Bookmark {
	now := time.Now()
	if b.ID == "" {
		b.ID = newID()
	}
	if b.Created.IsZero() {
		b.Created = now
	}
	b.Position.Updated = b.Created
	var out Bookmark
	s.Update(func(st *State) {
		for _, old := range st.Bookmarks[key] {
			if samePlace(old.Position, b.Position) {
				out = old
				return
			}
		}
		st.Bookmarks[key] = append(st.Bookmarks[key], b)
		out = b
	})
	return out
}

// UpdateBookmark replaces the bookmark with b's ID under key. It reports
// whether such a bookmark existed; Position is kept as stored when b leaves it
// zero, so renaming a row does not lose where it points.
func (s *Store) UpdateBookmark(key string, b Bookmark) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	for i := range s.state.Bookmarks[key] {
		if s.state.Bookmarks[key][i].ID != b.ID {
			continue
		}
		old := s.state.Bookmarks[key][i]
		if b.Position == (Position{}) {
			b.Position = old.Position
		}
		if b.Created.IsZero() {
			b.Created = old.Created
		}
		s.state.Bookmarks[key][i] = b
		s.gen++
		s.dirty = true
		return true
	}
	return false
}

// RemoveBookmark drops the bookmark with id under key, reporting whether it
// was there.
func (s *Store) RemoveBookmark(key, id string) bool {
	removed := false
	s.Update(func(st *State) {
		rows := st.Bookmarks[key]
		for i := range rows {
			if rows[i].ID != id {
				continue
			}
			rows = append(rows[:i], rows[i+1:]...)
			removed = true
			break
		}
		if !removed {
			return
		}
		if len(rows) == 0 {
			delete(st.Bookmarks, key)
			return
		}
		st.Bookmarks[key] = rows
	})
	return removed
}

// NotesOf returns key's notes in the order the reader made them.
func (s *Store) NotesOf(key string) []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Note(nil), s.state.Notes[key]...)
}

// AddNote stores n under key and returns it with its ID, Created and Modified
// stamped. A note covering the same passage is not stored twice.
func (s *Store) AddNote(key string, n Note) Note {
	now := time.Now()
	if n.ID == "" {
		n.ID = newID()
	}
	if n.Created.IsZero() {
		n.Created = now
	}
	n.Modified = n.Created
	var out Note
	s.Update(func(st *State) {
		for _, old := range st.Notes[key] {
			if samePlace(old.From, n.From) && samePlace(old.To, n.To) {
				out = old
				return
			}
		}
		st.Notes[key] = append(st.Notes[key], n)
		out = n
	})
	return out
}

// UpdateNote replaces the note with n's ID under key, stamping Modified. It
// reports whether such a note existed.
func (s *Store) UpdateNote(key string, n Note) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	for i := range s.state.Notes[key] {
		if s.state.Notes[key][i].ID != n.ID {
			continue
		}
		n.Created = s.state.Notes[key][i].Created
		n.Modified = time.Now()
		s.state.Notes[key][i] = n
		s.gen++
		s.dirty = true
		return true
	}
	return false
}

// RemoveNote drops the note with id under key, reporting whether it was there.
func (s *Store) RemoveNote(key, id string) bool {
	removed := false
	s.Update(func(st *State) {
		rows := st.Notes[key]
		for i := range rows {
			if rows[i].ID != id {
				continue
			}
			rows = append(rows[:i], rows[i+1:]...)
			removed = true
			break
		}
		if !removed {
			return
		}
		if len(rows) == 0 {
			delete(st.Notes, key)
			return
		}
		st.Notes[key] = rows
	})
	return removed
}

// samePlace reports whether two positions name the same point of a book. The
// node and offset are exact and the anchor survives a rebuild, so either one
// matching means the same place.
func samePlace(a, b Position) bool {
	if a.Chapter != b.Chapter {
		return false
	}
	if a.Node != 0 && a.Node == b.Node && a.Offset == b.Offset {
		return true
	}
	return a.Anchor != 0 && a.Anchor == b.Anchor && a.Offset == b.Offset
}

// newID returns a random bookmark or note identity: stable in the file, never
// reused, and short enough to read in a bug report.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The reader is lost without its shelf anyway; fall back to the clock so
		// a broken entropy source cannot stop a bookmark from being saved.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// SetPosition stores p for key, stamping Updated when it is zero.
func (s *Store) SetPosition(key string, p Position) {
	if key == "" {
		return
	}
	if p.Updated.IsZero() {
		p.Updated = time.Now()
	}
	s.Update(func(st *State) {
		st.Positions[key] = p
	})
}

// save writes the state. force = false skips the write when nothing is dirty.
func (s *Store) save(force bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	if !force && !s.dirty {
		s.mu.Unlock()
		return nil
	}
	snap, gen := cloneState(s.state), s.gen
	s.mu.Unlock()

	data, err := json.MarshalIndent(&snap, "", "  ")
	if err == nil {
		data = append(data, '\n')
		err = writeAtomic(s.path, data)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.dirty = true
		s.lastErr = err
		return err
	}
	s.lastErr = nil
	// A mutation that landed while this snapshot was being written keeps the
	// state dirty so the next timer or Flush picks it up.
	if s.gen == gen {
		s.dirty = false
	}
	s.writes++
	return nil
}

// fire is the debounce timer callback.
func (s *Store) fire() {
	s.mu.Lock()
	s.timer = nil
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	_ = s.save(false)
}

func (s *Store) stopTimer() {
	s.mu.Lock()
	timer := s.timer
	s.timer = nil
	s.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

// writeCount reports completed saves; the tests use it to prove that SaveSoon
// coalesces a burst into one write.
func (s *Store) writeCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func emptyState() State {
	return State{
		Version:   Version,
		Positions: map[string]Position{},
		Bookmarks: map[string][]Bookmark{},
		Notes:     map[string][]Note{},
	}
}

func normalize(st *State) {
	if st.Version < Version {
		st.Version = Version
	}
	if st.Positions == nil {
		st.Positions = map[string]Position{}
	}
	if st.Bookmarks == nil {
		st.Bookmarks = map[string][]Bookmark{}
	}
	if st.Notes == nil {
		st.Notes = map[string][]Note{}
	}
}

func cloneState(st State) State {
	out := State{
		Version:   st.Version,
		Prefs:     st.Prefs,
		LastOpen:  st.LastOpen,
		Positions: make(map[string]Position, len(st.Positions)),
		Bookmarks: make(map[string][]Bookmark, len(st.Bookmarks)),
		Notes:     make(map[string][]Note, len(st.Notes)),
	}
	if len(st.Books) > 0 {
		out.Books = append([]Book(nil), st.Books...)
	}
	for k, v := range st.Positions {
		out.Positions[k] = v
	}
	for k, rows := range st.Bookmarks {
		out.Bookmarks[k] = append([]Bookmark(nil), rows...)
	}
	for k, rows := range st.Notes {
		out.Notes[k] = append([]Note(nil), rows...)
	}
	return out
}

// backup copies a corrupt file next to itself so the user can attach it to a
// bug report. The original is left in place and is overwritten by the next
// successful save.
func backup(path string, data []byte) error {
	return writeAtomic(path+".bak", data)
}

// writeAtomic writes data to path via a temp file in the same directory. A
// crash or a full disk leaves either the old file or the new one, never a
// truncated mix.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	tmp = ""
	// The rename is durable once the directory entry is synced; failure here
	// means the file is on disk but the directory entry may not be.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
