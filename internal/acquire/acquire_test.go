package acquire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/laix/quire/internal/source"
)

// fakeSource is a Source whose single Download result is scripted.
type fakeSource struct {
	file     source.File
	err      error
	progress []source.Progress
	// prepare runs inside Download, before the result is returned. It lets a
	// test cancel the context or record that the call happened.
	prepare func(context.Context)
	calls   int
}

func (f *fakeSource) ID() source.ID     { return "fake" }
func (f *fakeSource) Name() string      { return "Fake" }
func (f *fakeSource) Caps() source.Caps { return source.Caps{Search: true, Download: true} }
func (f *fakeSource) Search(context.Context, source.Query) (source.Result, error) {
	return source.Result{}, nil
}
func (f *fakeSource) Download(ctx context.Context, _ source.Book, progress func(source.Progress)) (source.File, error) {
	f.calls++
	if f.prepare != nil {
		f.prepare(ctx)
	}
	if f.err != nil {
		return source.File{}, f.err
	}
	for _, p := range f.progress {
		if progress != nil {
			progress(p)
		}
	}
	return f.file, nil
}

func epubData(s string) []byte { return append([]byte("PK\x03\x04"), []byte(s)...) }

func readDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func assertNoPart(t *testing.T, dir string) {
	t.Helper()
	for _, name := range readDir(t, dir) {
		if strings.HasSuffix(name, ".part") || strings.HasPrefix(name, ".quire-") {
			t.Errorf("temporary file left behind: %q", name)
		}
	}
}

func TestSaveSucceeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shelf", "nested")
	src := &fakeSource{
		file: source.File{Name: "My Book.epub", Format: "epub", Data: epubData("hello")},
		progress: []source.Progress{
			{Done: 0, Total: 10, Stage: "connect"},
			{Done: 10, Total: 10, Stage: "done"},
		},
	}
	m := New(Options{Dir: dir})
	var got []source.Progress
	saved, err := m.Save(context.Background(), src, source.Book{Title: "My Book", Author: "An Author"}, func(p source.Progress) {
		got = append(got, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(saved.Path) {
		t.Errorf("path %q is not absolute", saved.Path)
	}
	if filepath.Dir(saved.Path) != mustAbs(t, dir) {
		t.Errorf("path %q is outside %q", saved.Path, dir)
	}
	if filepath.Base(saved.Path) != "My Book.epub" {
		t.Errorf("name = %q", filepath.Base(saved.Path))
	}
	if saved.Title != "My Book" || saved.Author != "An Author" || saved.Format != "epub" {
		t.Errorf("saved metadata = %+v", saved)
	}
	if saved.Size != int64(len(src.file.Data)) {
		t.Errorf("size = %d, want %d", saved.Size, len(src.file.Data))
	}
	onDisk, err := os.ReadFile(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(src.file.Data) {
		t.Error("file contents differ from the download")
	}
	info, err := os.Stat(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	if len(got) != 2 || got[1].Stage != "done" {
		t.Errorf("progress = %+v", got)
	}
	assertNoPart(t, dir)
}

func TestSaveTextFormat(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{file: source.File{Name: "novel", Format: "txt", Data: []byte("plain text")}}
	saved, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{Title: "Novel"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Format != "txt" || filepath.Base(saved.Path) != "novel.txt" {
		t.Errorf("saved = %+v", saved)
	}
	if saved.Size != int64(len("plain text")) {
		t.Errorf("size = %d", saved.Size)
	}
}

func TestSaveWithoutDir(t *testing.T) {
	src := &fakeSource{file: source.File{Name: "a.epub", Format: "epub", Data: epubData("x")}}
	_, err := New(Options{}).Save(context.Background(), src, source.Book{}, nil)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if src.calls != 0 {
		t.Error("source was called without a destination directory")
	}
}

func TestSaveInvalidDownloads(t *testing.T) {
	tests := []struct {
		name string
		file source.File
	}{
		{"unknown format", source.File{Name: "x.bin", Format: "pdf", Data: []byte("x")}},
		{"empty data", source.File{Name: "x.epub", Format: "epub"}},
		{"not a zip", source.File{Name: "x.epub", Format: "epub", Data: []byte("plain")}},
		{"no name", source.File{Name: "  ", Format: "epub", Data: epubData("x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := &fakeSource{file: tt.file}
			_, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil)
			if !errors.Is(err, source.ErrBadResponse) {
				t.Fatalf("err = %v, want a bad-response error", err)
			}
			if names := readDir(t, dir); len(names) != 0 {
				t.Errorf("directory is not empty: %v", names)
			}
			assertNoPart(t, dir)
		})
	}
}

func TestSaveMaxBytes(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{file: source.File{Name: "big.epub", Format: "epub", Data: epubData("0123456789")}}
	_, err := New(Options{Dir: dir, MaxBytes: 8}).Save(context.Background(), src, source.Book{}, nil)
	if !errors.Is(err, source.ErrBadResponse) {
		t.Fatalf("err = %v, want a bad-response error", err)
	}
	if len(readDir(t, dir)) != 0 {
		t.Errorf("oversized download was written: %v", readDir(t, dir))
	}
	assertNoPart(t, dir)
}

func TestSaveDownloadErrorLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{err: source.ErrRateLimited}
	_, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil)
	if !errors.Is(err, source.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if len(readDir(t, dir)) != 0 {
		t.Errorf("directory is not empty: %v", readDir(t, dir))
	}
}

func TestSaveCancellation(t *testing.T) {
	t.Run("before download", func(t *testing.T) {
		dir := t.TempDir()
		src := &fakeSource{file: source.File{Name: "x.epub", Format: "epub", Data: epubData("x")}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(Options{Dir: dir}).Save(ctx, src, source.Book{}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if src.calls != 0 {
			t.Error("source was called after cancellation")
		}
	})

	t.Run("during download", func(t *testing.T) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		src := &fakeSource{
			file:    source.File{Name: "x.epub", Format: "epub", Data: epubData("x")},
			prepare: func(context.Context) { cancel() },
		}
		_, err := New(Options{Dir: dir}).Save(ctx, src, source.Book{}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if names := readDir(t, dir); len(names) != 0 {
			t.Errorf("cancelled download was written: %v", names)
		}
		assertNoPart(t, dir)
	})

	t.Run("source reports cancellation", func(t *testing.T) {
		dir := t.TempDir()
		src := &fakeSource{err: context.Canceled}
		_, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestSaveFileNames(t *testing.T) {
	fixed := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name   string
		file   source.File
		book   source.Book
		prefix string
		want   string
	}{
		{
			name: "suggested name wins",
			file: source.File{Name: "From Source.epub"},
			book: source.Book{Title: "Title"},
			want: "From Source.epub",
		},
		{
			name: "title when the source suggests none",
			file: source.File{Name: ""},
			book: source.Book{Title: "书籍 标题"},
			want: "书籍 标题.epub",
		},
		{
			name: "unix traversal",
			file: source.File{Name: "../../evil.epub"},
			want: "evil.epub",
		},
		{
			name: "windows traversal",
			file: source.File{Name: `..\..\evil.epub`},
			want: "evil.epub",
		},
		{
			name: "absolute path",
			file: source.File{Name: "/etc/passwd.epub"},
			want: "passwd.epub",
		},
		{
			name: "control characters",
			file: source.File{Name: "a\x00b\x01c\x7f.epub"},
			want: "abc.epub",
		},
		{
			name: "reserved characters",
			file: source.File{Name: `a:b*c?d"e<f>g|h.epub`},
			want: "abcdefgh.epub",
		},
		{
			name: "wrong book extension is replaced",
			file: source.File{Name: "book.txt", Format: "epub"},
			want: "book.epub",
		},
		{
			name: "extension case is normalized",
			file: source.File{Name: "BOOK.EPUB"},
			want: "BOOK.epub",
		},
		{
			name: "extension is added",
			file: source.File{Name: "book"},
			want: "book.epub",
		},
		{
			name: "dots and spaces are trimmed",
			file: source.File{Name: "  ..book..  "},
			want: "book.epub",
		},
		{
			name: "empty name falls back to the clock",
			file: source.File{Name: " . "},
			book: source.Book{Title: "..."},
			want: "book-20240304-050607.epub",
		},
		{
			name:   "prefix",
			file:   source.File{Name: "book.epub"},
			prefix: "Quire - ",
			want:   "Quire - book.epub",
		},
		{
			name:   "prefix with a title fallback",
			file:   source.File{Name: ""},
			book:   source.Book{Title: "书名"},
			prefix: "Quire - ",
			want:   "Quire - 书名.epub",
		},
		{
			name:   "prefix with a clock fallback",
			file:   source.File{Name: " . "},
			prefix: "Quire - ",
			want:   "Quire - book-20240304-050607.epub",
		},
		{
			name:   "prefix cannot traverse either",
			file:   source.File{Name: "book.epub"},
			prefix: "../shelf/",
			want:   "shelfbook.epub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			f := tt.file
			if f.Format == "" {
				f.Format = "epub"
			}
			f.Data = epubData("x")
			src := &fakeSource{file: f}
			saved, err := New(Options{Dir: dir, Prefix: tt.prefix, Now: func() time.Time { return fixed }}).
				Save(context.Background(), src, tt.book, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := filepath.Base(saved.Path); got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveLongNameIsTruncatedAtRuneBoundary(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("长标题", 80) + ".epub"
	src := &fakeSource{file: source.File{Name: long, Format: "epub", Data: epubData("x")}}
	saved, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(saved.Path)
	if len(base) > maxNameBytes {
		t.Errorf("name is %d bytes, limit is %d", len(base), maxNameBytes)
	}
	if !utf8.ValidString(base) {
		t.Errorf("name %q is not valid UTF-8", base)
	}
	if !strings.HasSuffix(base, ".epub") {
		t.Errorf("name %q lost its extension", base)
	}
}

func TestSaveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	m := New(Options{Dir: dir})
	want := []string{"book.epub", "book (2).epub", "book (3).epub"}
	for i, name := range want {
		data := epubData(fmt.Sprintf("version %d", i))
		src := &fakeSource{file: source.File{Name: "book.epub", Format: "epub", Data: data}}
		saved, err := m.Save(context.Background(), src, source.Book{Title: "Book"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(saved.Path); got != name {
			t.Errorf("save %d name = %q, want %q", i, got, name)
		}
		onDisk, err := os.ReadFile(saved.Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(onDisk) != string(data) {
			t.Errorf("save %d overwrote an earlier book", i)
		}
	}
	if names := readDir(t, dir); len(names) != len(want) {
		t.Errorf("names = %v", names)
	}
}

func TestSaveConcurrentNamesDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	m := New(Options{Dir: dir})
	const n = 12
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := epubData(fmt.Sprintf("book %d", i))
			src := &fakeSource{file: source.File{Name: "same.epub", Format: "epub", Data: data}}
			saved, err := m.Save(context.Background(), src, source.Book{Title: "Same"}, nil)
			paths[i] = saved.Path
			errs[i] = err
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("save %d: %v", i, errs[i])
		}
		if seen[paths[i]] {
			t.Errorf("two saves share %q", paths[i])
		}
		seen[paths[i]] = true
		data, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if !strings.HasPrefix(string(data), "PK\x03\x04book ") {
			t.Errorf("save %d content = %q", i, data)
		}
	}
	if names := readDir(t, dir); len(names) != n {
		t.Errorf("directory has %d files, want %d: %v", len(names), n, names)
	}
	assertNoPart(t, dir)
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	src := &fakeSource{file: source.File{Name: "x.epub", Format: "epub", Data: epubData("x")}}
	if _, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("download directory was not created as a directory")
	}
}

func TestSaveBadDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{file: source.File{Name: "x.epub", Format: "epub", Data: epubData("x")}}
	_, err := New(Options{Dir: file}).Save(context.Background(), src, source.Book{}, nil)
	if err == nil {
		t.Fatal("a file used as a directory was accepted")
	}
	assertNoPart(t, root)
}

func TestSaveRelativeDirGivesAbsolutePath(t *testing.T) {
	t.Chdir(t.TempDir())
	src := &fakeSource{file: source.File{Name: "x.epub", Format: "epub", Data: epubData("x")}}
	saved, err := New(Options{Dir: "shelf"}).Save(context.Background(), src, source.Book{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(saved.Path) {
		t.Errorf("path %q is not absolute", saved.Path)
	}
	if _, err := os.Stat(saved.Path); err != nil {
		t.Errorf("saved file is missing: %v", err)
	}
}

func TestSaveKeepsExistingBookWhenCollisionNameIsTakenByDirectory(t *testing.T) {
	dir := t.TempDir()
	// A directory occupies the first candidate name; the save must move on
	// rather than fail or write inside it.
	if err := os.Mkdir(filepath.Join(dir, "book.epub"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{file: source.File{Name: "book.epub", Format: "epub", Data: epubData("x")}}
	saved, err := New(Options{Dir: dir}).Save(context.Background(), src, source.Book{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(saved.Path); got != "book (2).epub" {
		t.Errorf("name = %q, want book (2).epub", got)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
