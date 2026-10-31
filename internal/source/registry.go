package source

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// Registry is the set of sources the reader knows about, in the order the UI
// lists them. It is safe for concurrent use.
//
// The registry is where independence is enforced: Search asks every source at
// once, on its own goroutine, and an error, a panic or a slow answer from one
// is carried in that source's Outcome and nowhere else.
type Registry struct {
	mu      sync.RWMutex
	order   []ID
	sources map[ID]Source
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sources: make(map[ID]Source)}
}

// Add registers s. It refuses a nil source, an empty ID and a duplicate ID,
// because each would make the source list ambiguous.
func (r *Registry) Add(s Source) error {
	if s == nil {
		return errors.New("source: nil source")
	}
	id := s.ID()
	if id == "" {
		return errors.New("source: empty id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sources == nil {
		r.sources = make(map[ID]Source)
	}
	if _, exists := r.sources[id]; exists {
		return fmt.Errorf("source: duplicate id %q", id)
	}
	r.sources[id] = s
	r.order = append(r.order, id)
	return nil
}

// Remove takes a source out. It reports whether it was there.
func (r *Registry) Remove(id ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sources[id]; !ok {
		return false
	}
	delete(r.sources, id)
	for i, got := range r.order {
		if got == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

// Get returns the source with id.
func (r *Registry) Get(id ID) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[id]
	return s, ok
}

// All returns the sources in registration order.
func (r *Registry) All() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.order))
	for _, id := range r.order {
		if s, ok := r.sources[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Outcome is one source's answer to a search. Err belongs to Source alone;
// the other outcomes stand whatever it says.
type Outcome struct {
	Source  Source
	Result  Result
	Err     error
	Elapsed time.Duration
}

// Search asks every registered source concurrently. The returned slice is in
// registration order, not completion order, so the UI is stable from one
// search to the next. A source that panics becomes an Err outcome with the
// panic value; the other sources still answer.
func (r *Registry) Search(ctx context.Context, q Query) []Outcome {
	sources := r.All()
	out := make([]Outcome, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			start := time.Now()
			out[i] = Outcome{Source: s}
			out[i].Result, out[i].Err = searchOne(ctx, s, q)
			out[i].Elapsed = time.Since(start)
		}(i, s)
	}
	wg.Wait()
	return out
}

// SearchOne asks one source, guarding the caller against a panicking
// implementation.
func (r *Registry) SearchOne(ctx context.Context, id ID, q Query) (Result, error) {
	s, ok := r.Get(id)
	if !ok {
		return Result{}, fmt.Errorf("source: unknown %q", id)
	}
	return searchOne(ctx, s, q)
}

func searchOne(ctx context.Context, s Source, q Query) (res Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("source %s panicked: %v\n%s", s.ID(), v, debug.Stack())
		}
	}()
	if !s.Caps().Search {
		return Result{}, fmt.Errorf("%w: %s cannot search", ErrUnsupported, s.ID())
	}
	return s.Search(ctx, q)
}

// Download fetches b from its source, guarding the caller against a panicking
// implementation and refusing a file the host cannot use.
func (r *Registry) Download(ctx context.Context, b Book, progress func(Progress)) (f File, err error) {
	s, ok := r.Get(b.Source)
	if !ok {
		return File{}, fmt.Errorf("source: unknown %q", b.Source)
	}
	if !s.Caps().Download {
		return File{}, fmt.Errorf("%w: %s cannot download", ErrUnsupported, b.Source)
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("source %s panicked: %v\n%s", b.Source, v, debug.Stack())
		}
	}()
	f, err = s.Download(ctx, b, progress)
	if err != nil {
		return File{}, err
	}
	if err := ValidateFile(f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Configurable returns the Configurable behind id, if it is one.
func (r *Registry) Configurable(id ID) (Configurable, bool) {
	s, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	c, ok := s.(Configurable)
	return c, ok
}

// Importer returns the Importer behind id, if it is one.
func (r *Registry) Importer(id ID) (Importer, bool) {
	s, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	im, ok := s.(Importer)
	return im, ok
}

// IDs returns the registered IDs in order, for tests and diagnostics.
func (r *Registry) IDs() []ID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ID(nil), r.order...)
}

// SortByID is a helper for hosts that want a stable list independent of
// registration order.
func SortByID(sources []Source) {
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID() < sources[j].ID() })
}
