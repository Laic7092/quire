package source

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	id     ID
	name   string
	caps   Caps
	books  []Book
	err    error
	panics bool
	calls  atomic.Int32
}

func (f *fakeSource) ID() ID       { return f.id }
func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Caps() Caps   { return f.caps }
func (f *fakeSource) Search(ctx context.Context, q Query) (Result, error) {
	f.calls.Add(1)
	if f.panics {
		panic("boom")
	}
	if f.err != nil {
		return Result{}, f.err
	}
	return Result{Books: f.books}, nil
}
func (f *fakeSource) Download(ctx context.Context, b Book, progress func(Progress)) (File, error) {
	return File{Name: "book.epub", Format: "epub", Data: []byte("PK\x03\x04")}, nil
}

func TestRegistryKeepsSourcesApart(t *testing.T) {
	r := NewRegistry()
	good := &fakeSource{id: "good", name: "Good", caps: Caps{Search: true, Download: true},
		books: []Book{{Source: "good", Key: "1", Title: "A"}}}
	bad := &fakeSource{id: "bad", name: "Bad", caps: Caps{Search: true}, err: errors.New("down")}
	panicky := &fakeSource{id: "panic", name: "Panic", caps: Caps{Search: true}, panics: true}
	for _, s := range []Source{good, bad, panicky} {
		if err := r.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	out := r.Search(context.Background(), Query{Text: "x"})
	if len(out) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(out))
	}
	if out[0].Err != nil || len(out[0].Result.Books) != 1 {
		t.Errorf("good outcome = %+v", out[0])
	}
	if !strings.Contains(out[1].Err.Error(), "down") {
		t.Errorf("bad error = %v", out[1].Err)
	}
	if out[2].Err == nil || !strings.Contains(out[2].Err.Error(), "panicked") {
		t.Errorf("panic error = %v", out[2].Err)
	}
	// Registration order, not completion order.
	if out[0].Source.ID() != "good" || out[1].Source.ID() != "bad" || out[2].Source.ID() != "panic" {
		t.Errorf("order = %v, %v, %v", out[0].Source.ID(), out[1].Source.ID(), out[2].Source.ID())
	}
}

func TestRegistryRefusesBadAdds(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(nil); err == nil {
		t.Error("nil source accepted")
	}
	if err := r.Add(&fakeSource{id: ""}); err == nil {
		t.Error("empty id accepted")
	}
	s := &fakeSource{id: "x"}
	if err := r.Add(s); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(s); err == nil {
		t.Error("duplicate id accepted")
	}
	if !r.Remove("x") || r.Remove("x") {
		t.Error("Remove did not report presence")
	}
}

func TestCombine(t *testing.T) {
	for _, tc := range []struct{ base, ref, want string }{
		{"https://a.test/x/y", "z", "https://a.test/x/z"},
		{"https://a.test/x/y", "/z", "https://a.test/z"},
		{"https://a.test/x/y?q=1", "?p=2", "https://a.test/x/y?p=2"},
		{"https://a.test/x/y", "//b.test/z", "https://b.test/z"},
		{"https://a.test/x/y", "http://b.test/z", "http://b.test/z"},
		{"", "https://b.test/z", "https://b.test/z"},
		{"https://a.test", "z", "https://a.test/z"},
	} {
		if got := Combine(tc.base, tc.ref); got != tc.want {
			t.Errorf("Combine(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestValidateFile(t *testing.T) {
	if err := ValidateFile(File{Name: "a.epub", Format: "epub", Data: []byte("PK\x03\x04")}); err != nil {
		t.Errorf("valid epub: %v", err)
	}
	for _, f := range []File{
		{Name: "a", Format: "pdf", Data: []byte("x")},
		{Name: "a", Format: "epub", Data: nil},
		{Name: "a", Format: "epub", Data: []byte("nope")},
		{Name: "", Format: "txt", Data: []byte("x")},
	} {
		if err := ValidateFile(f); err == nil {
			t.Errorf("accepted %+v", f)
		}
	}
}

func TestRegistrySearchHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	r := NewRegistry()
	slow := &slowSource{fakeSource: fakeSource{id: "slow", caps: Caps{Search: true}}}
	_ = r.Add(slow)
	start := time.Now()
	out := r.Search(ctx, Query{Text: "x"})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("search took %v", elapsed)
	}
	if out[0].Err == nil {
		t.Error("slow source reported no error after its context ended")
	}
}

type slowSource struct{ fakeSource }

func (s *slowSource) Search(ctx context.Context, q Query) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}
