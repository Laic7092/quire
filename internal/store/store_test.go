package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	// Damage in the middle, not a short tail: nothing can be salvaged, so the
	// reader starts empty and the file is kept for inspection.
	corrupt := []byte(`{"version":1,"books":[}not json at all`)
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
	if got := s.Recovered(); got != path+".bak" {
		t.Errorf("Recovered() = %q, want the backup path", got)
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

// Taking a book off the shelf forgets the row and everything the reader
// recorded about that revision: the position, the bookmarks and the notes. The
// other books keep theirs, and LastOpen names a book that is still there.
func TestRemoveBookForgetsTheBook(t *testing.T) {
	s, _ := openTemp(t)
	s.TouchBook(Book{Key: "gone", Path: "/books/gone.epub", Title: "Gone"})
	s.TouchBook(Book{Key: "kept", Path: "/books/kept.epub", Title: "Kept"})
	s.SetPosition("gone", Position{Chapter: 2, Progress: 0.3})
	s.SetPosition("kept", Position{Chapter: 5, Progress: 0.7})
	s.AddBookmark("gone", Bookmark{Label: "here"})
	s.AddNote("gone", Note{Text: "passage", Comment: "note"})
	if got := s.State().LastOpen; got != "kept" {
		t.Fatalf("LastOpen = %q, want the book opened last", got)
	}

	if !s.RemoveBook("/books/gone.epub") {
		t.Fatal("RemoveBook reported nothing removed")
	}
	if got := s.Recent(0); len(got) != 1 || got[0].Key != "kept" {
		t.Errorf("shelf = %+v, want the other book alone", got)
	}
	if _, ok := s.Position("gone"); ok {
		t.Error("the removed book kept its position")
	}
	if got := s.BookmarksOf("gone"); len(got) != 0 {
		t.Errorf("bookmarks = %+v, want none", got)
	}
	if got := s.NotesOf("gone"); len(got) != 0 {
		t.Errorf("notes = %+v, want none", got)
	}
	// The book that stayed keeps its own reading state.
	if p, ok := s.Position("kept"); !ok || p.Chapter != 5 {
		t.Errorf("the other book's position = %+v, ok = %v", p, ok)
	}
	if s.RemoveBook("/books/gone.epub") {
		t.Error("RemoveBook removed a book that was gone")
	}
	if s.RemoveBook("") {
		t.Error("RemoveBook removed the empty path")
	}

	// The next launch opens the book that is still on the shelf.
	s.RemoveBook("/books/kept.epub")
	if got := s.State().LastOpen; got != "" {
		t.Errorf("LastOpen = %q, want none on an empty shelf", got)
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

// A state file cut short — a crash mid-write, a sync client, a disk that filled
// up — used to leave the reader with an empty shelf and a .bak nobody opens. The
// books written whole before the cut are still there, so they are recovered.
func TestOpenTruncatedJSONSalvagesTheShelf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	full := `{"version":1,"books":[` +
		`{"key":"k1","path":"/books/one.epub","title":"One"},` +
		`{"key":"k2","path":"/books/two.epub","title":"Two"},` +
		`{"key":"k3","path":"/books/three.epub","title":"Three"}],"positions":{},"prefs":{}}`
	// Cut after the second book, as a power failure mid-write would leave it:
	// the two books written whole are still there, the third never got there.
	marker := `"title":"Two"},`
	cut := full[:strings.Index(full, marker)+len(marker)]
	if err := os.WriteFile(path, []byte(cut), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	books := s.State().Books
	if len(books) != 2 || books[0].Key != "k1" || books[1].Key != "k2" {
		t.Fatalf("salvaged books = %+v, want the two written whole", books)
	}
	if books[1].Title != "Two" {
		t.Errorf("salvaged title = %q, want Two", books[1].Title)
	}
	if got := s.Recovered(); got != path+".bak" {
		t.Errorf("Recovered() = %q, want %q", got, path+".bak")
	}
	if bak := string(mustRead(t, path+".bak")); bak != cut {
		t.Errorf("backup = %q, want the damaged file verbatim", bak)
	}

	// Saving the salvaged state replaces the damaged file and the shelf keeps
	// the recovered books.
	s.Update(func(st *State) { st.Prefs.Theme = "sepia" })
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if got := reopened.State(); len(got.Books) != 2 || got.Prefs.Theme != "sepia" {
		t.Errorf("reopened state = %+v", got)
	}
	if got := reopened.Recovered(); got != "" {
		t.Errorf("a clean file reported recovery %q", got)
	}
}

// A file that holds a single bad field keeps everything else: one damaged
// reading position must not cost the whole shelf.
func TestOpenBadFieldKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	doc := `{"version":1,"books":[{"key":"k1","path":"/a.epub","title":"A"}],` +
		`"positions":{"k1":{"chapter":2,"progress":1e999}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := len(s.State().Books); got != 1 {
		t.Fatalf("books = %d, want the one that parsed", got)
	}
	if s.Recovered() == "" {
		t.Error("a partly decoded file reported no recovery")
	}
}

// A document that is valid JSON but not a state (null, an array, a number) is
// backed up instead of being mistaken for a fresh install.
func TestOpenNonObjectIsBackedUp(t *testing.T) {
	for _, doc := range []string{"null", "[]", "42"} {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", doc, err)
		}
		if got := len(s.State().Books) + len(s.State().Positions); got != 0 {
			t.Errorf("Open(%q) produced a non-empty state", doc)
		}
		if got := string(mustRead(t, path+".bak")); got != doc {
			t.Errorf("Open(%q) backup = %q", doc, got)
		}
		if s.Recovered() == "" {
			t.Errorf("Open(%q) reported no recovery", doc)
		}
		s.Close()
	}
}

// A state file from a newer version is read, but its fields are not silently
// dropped: the original is kept next to it before anything overwrites it.
func TestOpenNewerSchemaBacksUpBeforeDowngrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	doc := `{"version":9,"books":[{"key":"k1","path":"/a.epub","title":"A"}],"collections":["to read"]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := len(s.State().Books); got != 1 {
		t.Fatalf("books = %d, want the newer file's shelf", got)
	}
	if got := s.Recovered(); got != path+".bak" {
		t.Errorf("Recovered() = %q, want %q", got, path+".bak")
	}
	if bak := string(mustRead(t, path+".bak")); bak != doc {
		t.Errorf("backup = %q, want the newer document verbatim", bak)
	}

	// What this version writes is this version's shape, so it says so; the
	// newer fields live on in the backup.
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var st State
	if err := json.Unmarshal(mustRead(t, path), &st); err != nil {
		t.Fatal(err)
	}
	if st.Version != Version {
		t.Errorf("written version = %d, want %d", st.Version, Version)
	}
}

// Two rows for one book are collapsed on open: Recent would list the file twice
// and TouchBook would only ever replace one of them.
func TestOpenDedupesBookKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	doc := `{"version":1,"books":[` +
		`{"key":"k","path":"/a.epub","title":"Old","lastOpened":"2020-01-01T00:00:00Z"},` +
		`{"key":"k","path":"/a.epub","title":"New","lastOpened":"2021-01-01T00:00:00Z"},` +
		`{"key":"","path":"","title":"nameless"}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	books := s.State().Books
	if len(books) != 2 {
		t.Fatalf("books = %+v, want the newest k plus the nameless row", books)
	}
	if books[0].Title != "New" {
		t.Errorf("kept %q, want the most recently opened revision", books[0].Title)
	}
	if got := len(s.Recent(0)); got != 2 {
		t.Errorf("Recent = %d rows, want 2", got)
	}
}

// The anchor is the identifier that survives a rebuild, so it decides on its
// own whether two positions are one place; the node number is only the faster
// path and must not split a place in two.
func TestSamePlace(t *testing.T) {
	base := Position{Chapter: 1, Node: 7, Anchor: 70, Offset: 3}
	if !samePlace(base, base) {
		t.Error("a position is not the same place as itself")
	}
	// The same node and offset, no anchor: still one place.
	if !samePlace(Position{Chapter: 1, Node: 7, Offset: 3}, Position{Chapter: 1, Node: 7, Offset: 3}) {
		t.Error("the same node and offset are two places")
	}
	// The same anchor resolved into another chapter is one place: a revision of
	// the book may move the chapter, not the anchor.
	moved := Position{Chapter: 4, Anchor: 70, Offset: 3}
	if !samePlace(base, moved) {
		t.Error("an anchor that moved chapter was split into two places")
	}
	// The same chapter and node but another offset are two places.
	if samePlace(base, Position{Chapter: 1, Node: 7, Anchor: 70, Offset: 4}) {
		t.Error("two offsets in one node are one place")
	}
	// A node number means nothing in another chapter.
	if samePlace(Position{Chapter: 1, Node: 7, Offset: 3}, Position{Chapter: 2, Node: 7, Offset: 3}) {
		t.Error("the same node number in another chapter matched")
	}
	// Nothing recorded: never the same place, so two bare positions do not
	// collapse into one bookmark.
	if samePlace(Position{Chapter: 1}, Position{Chapter: 1}) {
		t.Error("two positions with no node and no anchor matched")
	}
}

// repairJSON is what turns a truncated document back into something readable.
func TestRepairJSON(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" means the damage is not a short tail
	}{
		{`{"a":1`, `{"a":1}`},
		{`{"a":[1,2`, `{"a":[1,2]}`},
		{`{"a":[{"b":1}`, `{"a":[{"b":1}]}`},
		{`{"a":[{"b":1},`, `{"a":[{"b":1}]}`},
		{`{"a":"half`, `{"a":"half"}`},
		{`{"a":"a,`, `{"a":"a,"}`},
		{`{"a":"x\"`, `{"a":"x\""}`},
		{`{"a":1}}`, ``},
		{`{"a":[}`, ``},
		{`{"a":[1,2]}`, `{"a":[1,2]}`},
	}
	for _, tc := range cases {
		got, ok := repairJSON([]byte(tc.in))
		if tc.want == "" {
			if ok {
				t.Errorf("repairJSON(%q) = %q, want a refusal", tc.in, got)
			}
			continue
		}
		if !ok || string(got) != tc.want {
			t.Errorf("repairJSON(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
		if !json.Valid(got) {
			t.Errorf("repaired %q is not valid JSON", tc.in)
		}
	}
}

// A byte-order mark in front of the document — what an editor from another
// platform leaves behind — must not make the reader lose its shelf.
func TestOpenToleratesBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	doc := "\xef\xbb\xbf" + `{"version":1,"books":[{"key":"k1","path":"/a.epub","title":"A"}],"prefs":{"theme":"night"}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	st := s.State()
	if len(st.Books) != 1 || st.Prefs.Theme != "night" {
		t.Errorf("state = %+v, want the file's shelf and preferences", st)
	}
	if got := s.Recovered(); got != "" {
		t.Errorf("a readable file reported recovery %q", got)
	}
}

// A state file someone symlinked into a synced or versioned directory keeps the
// link: the writes follow it instead of replacing it with a plain file, which
// would silently stop the directory from tracking the reader.
func TestSaveFollowsASymlinkedStateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "synced", "state.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s, err := Open(link)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	s.Update(func(st *State) { st.LastOpen = "through-the-link" })
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced by a regular file (%v)", err)
	}
	var st State
	if err := json.Unmarshal(mustRead(t, target), &st); err != nil {
		t.Fatal(err)
	}
	if st.LastOpen != "through-the-link" {
		t.Errorf("LastOpen at the link's target = %q", st.LastOpen)
	}
}
