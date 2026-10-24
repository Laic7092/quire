package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenMissingFile(t *testing.T) {
	s, path := openTemp(t)
	st := s.State()
	if st.Version != Version {
		t.Errorf("version = %d, want %d", st.Version, Version)
	}
	if len(st.Books) != 0 || len(st.Positions) != 0 {
		t.Errorf("fresh state is not empty: %+v", st)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Open created %s", path)
	}
	if err := s.Flush(); err != nil {
		t.Errorf("Flush on a clean store: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Flush wrote a clean store")
	}
}

func TestOpenEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := s.State().Version; got != Version {
		t.Errorf("version = %d, want %d", got, Version)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("an empty file should not be backed up")
	}
}

func TestOpenCorruptJSONBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	corrupt := []byte(`{"version":1,"books":[{"key":"a",`)
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.State().Books) != 0 {
		t.Errorf("corrupt file produced a non-empty state")
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if string(bak) != string(corrupt) {
		t.Errorf("backup = %q, want %q", bak, corrupt)
	}

	// The next save replaces the corrupt file with valid JSON.
	s.Update(func(st *State) { st.Prefs.Sidebar = "toc" })
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if got := reopened.State().Prefs.Sidebar; got != "toc" {
		t.Errorf("sidebar after reopen = %q", got)
	}
}

func TestStateIsDeepCopy(t *testing.T) {
	s, _ := openTemp(t)
	s.Update(func(st *State) {
		st.Books = append(st.Books, Book{Key: "k1", Title: "one"})
		st.Positions["k1"] = Position{Chapter: 3}
	})
	st := s.State()
	st.Books[0].Title = "mutated"
	st.Positions["k1"] = Position{Chapter: 99}
	st.Positions["k2"] = Position{Chapter: 1}

	fresh := s.State()
	if got := fresh.Books[0].Title; got != "one" {
		t.Errorf("Books shares storage with State(): %q", got)
	}
	if got := fresh.Positions["k1"].Chapter; got != 3 {
		t.Errorf("Positions shares storage with State(): chapter %d", got)
	}
	if _, ok := fresh.Positions["k2"]; ok {
		t.Errorf("Positions map is shared with State()")
	}
}

func bigState(st *State) {
	title := strings.Repeat("long book title ", 40)
	for i := 0; i < 200; i++ {
		st.Books = append(st.Books, Book{
			Key:   fmt.Sprintf("key-%04d", i),
			Path:  fmt.Sprintf("/books/%04d.epub", i),
			Title: title,
		})
	}
}

// TestSaveIsAtomic proves that a reader never observes a torn file: the writer
// rewrites a large state 50 times while another goroutine keeps parsing it.
func TestSaveIsAtomic(t *testing.T) {
	s, path := openTemp(t)
	s.Update(bigState)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var readErr error
	var reads int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				readErr = fmt.Errorf("read: %w", err)
				return
			}
			var st State
			if err := json.Unmarshal(data, &st); err != nil {
				readErr = fmt.Errorf("torn read (%d bytes): %w", len(data), err)
				return
			}
			reads++
		}
	}()
	for i := 0; i < 50; i++ {
		i := i
		s.Update(func(st *State) { st.LastOpen = fmt.Sprintf("key-%04d", i) })
		if err := s.Save(); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if reads == 0 {
		t.Error("the reader never got to run")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %q", e.Name())
		}
	}
}

// TestFailedSaveKeepsOldFile checks that a write error leaves the previous file
// in place instead of truncating it.
func TestFailedSaveKeepsOldFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Update(func(st *State) { st.LastOpen = "good" })
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	s.Update(func(st *State) { st.LastOpen = "lost" })
	if err := s.Save(); err == nil {
		t.Fatal("Save into a read-only directory succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the old file disappeared: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the failed save modified the file")
	}
	if s.LastError() == nil {
		t.Error("LastError is nil after a failed save")
	}

	// The state stays dirty, so a later Flush retries once the disk allows it.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush after the disk recovered: %v", err)
	}
	var st State
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.LastOpen != "lost" {
		t.Errorf("LastOpen = %q, want %q", st.LastOpen, "lost")
	}
}

func TestSaveSoonCoalesces(t *testing.T) {
	s, path := openTemp(t)
	const d = 20 * time.Millisecond
	for i := 0; i < 3; i++ {
		i := i
		s.Update(func(st *State) { st.Prefs.Sidebar = fmt.Sprintf("pane-%d", i) })
		s.SaveSoon(d)
	}
	waitFor(t, time.Second, func() bool { return s.writeCount() == 1 })
	// A second fire would mean the debounce was not merged.
	time.Sleep(4 * d)
	if got := s.writeCount(); got != 1 {
		t.Fatalf("writes after three SaveSoon calls = %d, want 1", got)
	}
	var st State
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.Prefs.Sidebar != "pane-2" {
		t.Errorf("persisted sidebar = %q, want the last mutation %q", st.Prefs.Sidebar, "pane-2")
	}

	// Flush writes pending changes immediately.
	s.Update(func(st *State) { st.LastOpen = "flushed" })
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := s.writeCount(); got != 2 {
		t.Fatalf("writes after Flush = %d, want 2", got)
	}
	if err := json.Unmarshal(mustRead(t, path), &st); err != nil {
		t.Fatal(err)
	}
	if st.LastOpen != "flushed" {
		t.Errorf("LastOpen = %q after Flush", st.LastOpen)
	}

	// A merged burst after the flush lands once more with the newest value.
	for i := 0; i < 3; i++ {
		i := i
		s.Update(func(st *State) { st.Prefs.FontSize = float32(18 + i) })
		s.SaveSoon(d)
	}
	waitFor(t, time.Second, func() bool { return s.writeCount() == 3 })
	time.Sleep(4 * d)
	if got := s.writeCount(); got != 3 {
		t.Fatalf("writes = %d, want 3", got)
	}
	if err := json.Unmarshal(mustRead(t, path), &st); err != nil {
		t.Fatal(err)
	}
	if st.Prefs.FontSize != 20 {
		t.Errorf("FontSize = %v, want 20", st.Prefs.FontSize)
	}
}

func TestCloseFlushesAndStopsTimer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Update(func(st *State) { st.LastOpen = "closing" })
	s.SaveSoon(time.Hour)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var st State
	if err := json.Unmarshal(mustRead(t, path), &st); err != nil {
		t.Fatal(err)
	}
	if st.LastOpen != "closing" {
		t.Errorf("Close did not flush: LastOpen = %q", st.LastOpen)
	}
	if err := s.Save(); err != ErrClosed {
		t.Errorf("Save after Close = %v, want ErrClosed", err)
	}
	if err := s.Flush(); err != ErrClosed {
		t.Errorf("Flush after Close = %v, want ErrClosed", err)
	}
	s.Update(func(st *State) { st.LastOpen = "ignored" })
	if got := s.State().LastOpen; got != "closing" {
		t.Errorf("Update after Close was applied: %q", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

func TestKey(t *testing.T) {
	base := time.Unix(1700000000, 0)
	k1 := Key("/books/a.epub", 100, base)
	k2 := Key("/books/a.epub", 100, base.Add(time.Nanosecond))
	k3 := Key("/books/a.epub", 101, base)
	k4 := Key("/books/a.epub", 100, base)

	if k1 == k2 {
		t.Error("a different mtime produced the same key")
	}
	if k1 == k3 {
		t.Error("a different size produced the same key")
	}
	if k1 != k4 {
		t.Errorf("Key is not deterministic: %q != %q", k1, k4)
	}
	if len(k1) != 16 {
		t.Errorf("key length = %d, want 16", len(k1))
	}
	if k1 == Key("/books/other.epub", 100, base) {
		t.Error("a different path produced the same key")
	}
	// Relative and absolute spellings of the same file agree.
	abs, err := filepath.Abs("books/a.epub")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := Key("books/a.epub", 100, base), Key(abs, 100, base); a != b {
		t.Errorf("relative path key %q != absolute path key %q", a, b)
	}
}

func TestTouchBookAndRecent(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now()
	first := Book{Key: "k1", Path: "/a.epub", Title: "A", Added: now.Add(-time.Hour)}
	s.TouchBook(first)
	s.TouchBook(Book{Key: "k2", Path: "/b.txt", Title: "B"})
	s.TouchBook(Book{Key: "k1", Path: "/a.epub", Title: "A2"})

	books := s.State().Books
	if len(books) != 2 {
		t.Fatalf("books = %d, want 2 (upsert by key)", len(books))
	}
	if got := s.State().LastOpen; got != "k1" {
		t.Errorf("LastOpen = %q, want k1", got)
	}
	for _, b := range books {
		if b.Key == "k1" {
			if b.Title != "A2" {
				t.Errorf("upsert did not replace the entry: %q", b.Title)
			}
			if !b.Added.Equal(first.Added) {
				t.Errorf("Added was overwritten: %v != %v", b.Added, first.Added)
			}
			if b.LastOpened.Before(now) {
				t.Errorf("LastOpened = %v, want now or later", b.LastOpened)
			}
		}
		if b.Added.IsZero() {
			t.Errorf("%s has a zero Added", b.Key)
		}
	}

	recent := s.Recent(1)
	if len(recent) != 1 || recent[0].Key != "k1" {
		t.Errorf("Recent(1) = %+v, want the most recently opened book", recent)
	}
	if got := len(s.Recent(0)); got != 2 {
		t.Errorf("Recent(0) = %d books, want all 2", got)
	}
	if got := len(s.Recent(-1)); got != 2 {
		t.Errorf("Recent(-1) = %d books, want all 2", got)
	}
}

// A book whose file changed has a new key but the same path: it keeps one row
// and its reading state follows, so a re-download does not strand a position or
// leave a ghost book on the shelf.
func TestTouchBookFollowsAReplacedFile(t *testing.T) {
	s, _ := openTemp(t)
	s.TouchBook(Book{Key: "old", Path: "/books/a.epub", Title: "A"})
	s.SetPosition("old", Position{Chapter: 3, Progress: 0.4})
	s.AddBookmark("old", Bookmark{Label: "here"})
	s.AddNote("old", Note{Text: "passage", Comment: "note"})

	s.TouchBook(Book{Key: "new", Path: "/books/a.epub", Title: "A, second edition"})

	books := s.State().Books
	if len(books) != 1 {
		t.Fatalf("books = %d, want the replaced file to keep one row", len(books))
	}
	if books[0].Key != "new" || books[0].Title != "A, second edition" {
		t.Errorf("row = %+v, want the new revision", books[0])
	}
	if _, ok := s.Position("old"); ok {
		t.Error("the old key still holds a position")
	}
	if _, ok := s.Position("new"); !ok {
		t.Error("the position did not follow the file")
	}
	if got := s.BookmarksOf("new"); len(got) != 1 || got[0].Label != "here" {
		t.Errorf("bookmarks = %+v, want them under the new key", got)
	}
	if got := s.NotesOf("new"); len(got) != 1 || got[0].Comment != "note" {
		t.Errorf("notes = %+v, want them under the new key", got)
	}
	if got := s.State().LastOpen; got != "new" {
		t.Errorf("LastOpen = %q, want new", got)
	}
}

func TestPositionGetSet(t *testing.T) {
	s, _ := openTemp(t)
	if _, ok := s.Position("missing"); ok {
		t.Error("Position reported a value for a missing key")
	}
	s.SetPosition("k1", Position{Chapter: 2, Node: 17, Offset: 4, Progress: 0.5})
	p, ok := s.Position("k1")
	if !ok {
		t.Fatal("Position did not find k1")
	}
	if p.Chapter != 2 || p.Node != 17 || p.Offset != 4 {
		t.Errorf("Position = %+v", p)
	}
	if p.Updated.IsZero() {
		t.Error("SetPosition left Updated zero")
	}
	stamp := time.Unix(1600000000, 0)
	s.SetPosition("k2", Position{Chapter: 1, Updated: stamp})
	if p, _ := s.Position("k2"); !p.Updated.Equal(stamp) {
		t.Errorf("SetPosition overwrote an explicit Updated")
	}
	s.SetPosition("", Position{Chapter: 9})
	if _, ok := s.Position(""); ok {
		t.Error("SetPosition accepted an empty key")
	}
}

func TestConcurrentUpdateAndSave(t *testing.T) {
	s, path := openTemp(t)
	const goroutines, iterations = 4, 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s.Update(func(st *State) {
					st.Books = append(st.Books, Book{Key: fmt.Sprintf("g%d-%d", g, i)})
					st.Prefs.FontSize = float32(10 + i)
				})
				s.SetPosition(fmt.Sprintf("g%d", g), Position{Chapter: i, Node: uint32(i)})
				if i%8 == 0 {
					if err := s.Save(); err != nil {
						t.Errorf("Save: %v", err)
					}
				}
				_ = s.Recent(2)
			}
		}()
	}
	wg.Wait()
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if got := len(reopened.State().Books); got != goroutines*iterations {
		t.Errorf("books on disk = %d, want %d", got, goroutines*iterations)
	}
	if got := len(reopened.State().Positions); got != goroutines {
		t.Errorf("positions on disk = %d, want %d", got, goroutines)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/xdg-test", "layx-reader", "state.json"); got != want {
		t.Errorf("DefaultPath with XDG_CONFIG_HOME = %q, want %q", got, want)
	}

	// A relative XDG_CONFIG_HOME is invalid per the spec; fall back to the
	// home directory instead of writing next to the process.
	t.Setenv("XDG_CONFIG_HOME", "relative/dir")
	got, err = DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("DefaultPath = %q, want an absolute path", got)
	}
	if filepath.Base(filepath.Dir(got)) != "layx-reader" {
		t.Errorf("DefaultPath = %q, want a layx-reader directory", got)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before the timeout")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
