// Package source is the one seam between Quire and the outside world of books.
//
// A book source is an independent implementation of Source: it owns its
// transport, its parsing, its credentials and its failure modes, and it
// imports nothing but this package. The registry keeps the sources apart, so a
// site that is down, slow or broken costs its own row and no one else's.
//
// The reader only searches and downloads. A downloaded book is written to the
// shelf as an EPUB or TXT and then opened by the local reader, so reading,
// pagination, bookmarks, notes and statistics never learn that a book came
// from the network.
//
// This package deliberately has no UI and no store: hosts build sources, list
// them, and move bytes. Configuration and imported definitions are exposed as
// data (Setting, Importer) rather than as source-specific UI.
package source

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ID names a source in the store and in every Book it returns. It is stable
// across restarts: changing one orphans the configuration and definitions
// saved under it.
type ID string

// Errors a host can act on. Implementations wrap these with %w; the host must
// be able to tell "wrong password" from "site is down" without importing the
// source's package.
var (
	// ErrAuthRequired reports that the source needs credentials (or a fresh
	// login) before it can answer. A Configurable source returns it until
	// Configure succeeded.
	ErrAuthRequired = errors.New("source: authentication required")
	// ErrNotFound reports that the requested book or page does not exist.
	ErrNotFound = errors.New("source: not found")
	// ErrUnsupported reports a rule, format or operation this source cannot
	// do, such as a Legado rule that needs the JavaScript engine.
	ErrUnsupported = errors.New("source: unsupported")
	// ErrRateLimited reports that the source asked the caller to slow down.
	ErrRateLimited = errors.New("source: rate limited")
	// ErrBadResponse reports bytes that cannot be the answer the source
	// expected: an HTML error page instead of a book list, a truncated
	// archive, a download that is not an EPUB or TXT after all.
	ErrBadResponse = errors.New("source: bad response")
)

// Caps says what a source can do, so the host can offer only the operations
// that exist. Every source that appears in the UI can search.
type Caps struct {
	Search bool
	// Download means Search results can be fetched and put on the shelf.
	Download bool
	// NeedsLogin marks a source whose Settings include credentials and whose
	// Search returns ErrAuthRequired until they are given.
	NeedsLogin bool
}

// Query is one search. Page is 1-based; Limit 0 means the source's default.
// Text is what the reader typed, without surrounding whitespace.
type Query struct {
	Text  string
	Page  int
	Limit int
}

// Book is one search result. The host shows the display fields and passes the
// whole value back to Download; Key, Extra and Source are the source's own
// tokens and must be treated as opaque.
type Book struct {
	Source ID
	// Key identifies the book inside its source. It is never shown, and it is
	// what Download needs to find the book again.
	Key string
	// Title is required; the host drops a result without one.
	Title       string
	Author      string
	Description string
	// CoverURL is an absolute http(s) URL, or empty. The host may show it.
	CoverURL string
	Language string
	// Format is the source's best guess: "epub", "txt", or "" when it only
	// finds out at download time.
	Format string
	// Size is the download size in bytes when known, 0 otherwise.
	Size int64
	// Year is the publication year or date as the source prints it.
	Year string
	// Extra carries source-specific values needed by Download that are not
	// worth naming here (an encrypted token, a book id, a sub-source index).
	Extra map[string]string
}

// Result is one page of a search. HasMore tells whether Page+1 is worth
// asking for. Next is an opaque cursor for sources whose paging is not a page
// number; currently unused by the host.
type Result struct {
	Books   []Book
	HasMore bool
	Next    string
}

// Progress reports a download. Total is -1 while unknown. Stage is a short
// machine-readable word ("connect", "chapters", "epub", …); the host localizes
// it if it wants and never depends on the exact set.
type Progress struct {
	Done  int64
	Total int64
	Stage string
}

// Percent is Done over Total, 0 while the total is unknown.
func (p Progress) Percent() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Done) / float64(p.Total)
}

// File is a downloaded book: the bytes and what they are. Name is a suggested
// file name without any directory part; the host sanitizes it and may replace
// it. Format is "epub" or "txt", and must match the bytes.
type File struct {
	Name   string
	Format string
	Data   []byte
}

// Source is one book source. Implementations are safe for concurrent use and
// must honour ctx in every network call.
type Source interface {
	// ID is stable and unique in the registry.
	ID() ID
	// Name is the human name shown in the source list.
	Name() string
	// Caps says what the source supports.
	Caps() Caps
	// Search finds books. It returns ErrAuthRequired, ErrRateLimited,
	// ErrNotFound or ErrBadResponse (wrapped) when those describe the failure
	// better than a bare network error. An empty query is not an error for a
	// source whose catalogue can be browsed; otherwise it returns no books.
	Search(ctx context.Context, q Query) (Result, error)
	// Download fetches one book returned by Search. The host calls it only
	// when Caps().Download is true. progress may be nil.
	Download(ctx context.Context, b Book, progress func(Progress)) (File, error)
}

// Setting describes one configuration value of a source, so the host can build
// a form without knowing which source it configures.
type Setting struct {
	// Key is what Configure's map uses.
	Key string
	// Label, Help and Placeholder are display strings; a source is free to
	// leave them English and the host may show them as they are.
	Label       string
	Help        string
	Placeholder string
	// Secret marks a password or token: the host stores it (in the same
	// place as other settings) but never shows it back.
	Secret bool
	// Value is the current value for a non-secret setting; empty for a
	// secret or an unset value.
	Value string
	// Required fails Configure when the value is empty.
	Required bool
}

// Configurable is a source with settings, such as a site and an account. The
// host loads saved values, fills Setting.Value where it can, and calls
// Configure with the reader's changes. Configure must be safe to call with a
// partial map: keys missing from values keep their current value.
type Configurable interface {
	Source
	Settings() []Setting
	Configure(values map[string]string) error
}

// Importer is a source that holds definitions the reader adds at runtime, such
// as Legado book source JSON. Import replaces or adds definitions and reports
// how many are now usable. Export returns the same document for backup.
type Importer interface {
	Source
	Import(data []byte) (added int, err error)
	Export() ([]byte, error)
}

// Combine joins a base URL and a possibly relative reference. Sources use it
// for the links their pages contain, so one implementation of the awkward
// cases (protocol-relative, absolute path, query only) is tested once here.
func Combine(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "//") {
		switch {
		case strings.HasPrefix(base, "https://"):
			return "https:" + ref
		case strings.HasPrefix(base, "http://"):
			return "http:" + ref
		default:
			return "https:" + ref
		}
	}
	if hasScheme(ref) {
		return ref
	}
	if !hasScheme(base) {
		return ref
	}
	i := strings.Index(base, "://")
	schemeEnd := i + 3
	rest := base[schemeEnd:]
	slash := strings.IndexByte(rest, '/')
	origin := base
	if slash >= 0 {
		origin = base[:schemeEnd+slash]
	}
	path := "/"
	if slash >= 0 {
		path = rest[slash:]
	}
	if at := strings.IndexAny(path, "?#"); at >= 0 {
		path = path[:at]
	}
	switch {
	case strings.HasPrefix(ref, "/"):
		return origin + ref
	case strings.HasPrefix(ref, "?"):
		return origin + path + ref
	case strings.HasPrefix(ref, "#"):
		return origin + path + ref
	}
	dir := path
	if at := strings.LastIndexByte(dir, '/'); at >= 0 {
		dir = dir[:at+1]
	} else {
		dir = "/"
	}
	return origin + dir + ref
}

func hasScheme(s string) bool {
	i := strings.Index(s, "://")
	if i <= 0 {
		return false
	}
	for _, r := range s[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// PageOf normalizes a 1-based page number.
func PageOf(q Query) int {
	if q.Page < 1 {
		return 1
	}
	return q.Page
}

// LimitOf returns the source's page size, using def when q asks for none.
func LimitOf(q Query, def int) int {
	if q.Limit > 0 {
		return q.Limit
	}
	if def > 0 {
		return def
	}
	return 20
}

// CleanText collapses runs of white space and trims the result, which is what
// almost every scraped field needs before it reaches the UI.
func CleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FormatOf normalizes a downloaded format into "epub", "txt" or "".
func FormatOf(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "epub", "application/epub+zip":
		return "epub"
	case "txt", "text", "text/plain":
		return "txt"
	}
	return ""
}

// SizeLabel is a short human size for a result row, "1.2 MB"; empty for an
// unknown size.
func SizeLabel(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 0, 64) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// ValidateFile refuses a download the host cannot put on the shelf, so a
// source's bug becomes a clear error instead of a corrupt book file.
func ValidateFile(f File) error {
	format := FormatOf(f.Format)
	if format == "" {
		return fmt.Errorf("%w: unknown format %q", ErrBadResponse, f.Format)
	}
	if len(f.Data) == 0 {
		return fmt.Errorf("%w: empty %s", ErrBadResponse, format)
	}
	if format == "epub" && (len(f.Data) < 4 || string(f.Data[:2]) != "PK") {
		return fmt.Errorf("%w: not an epub archive", ErrBadResponse)
	}
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("%w: download has no file name", ErrBadResponse)
	}
	return nil
}

// Deadline is a convenience for sources deriving a per-request timeout from
// the caller's context without replacing its deadline.
func Deadline(ctx context.Context, def time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || def <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, def)
}
