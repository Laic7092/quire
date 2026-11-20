package epub

import (
	"fmt"
	"sync"

	"github.com/laix/layx/render"
)

// maxReportedIssues caps the diagnostic list of one Open or Build call, the
// same way render caps one layout's list. A pathological book (thousands of
// broken images, say) must not turn its own diagnostics into the memory
// problem; the list then ends with one IssueSuppressed entry that counts the
// reports that were not retained.
const maxReportedIssues = 1024

// issueKey is the identity used to fold duplicate reports.
type issueKey struct {
	code    render.IssueCode
	chapter string
	node    render.NodeID
	detail  string
}

// issueCollector gathers this package's dropped-content diagnostics for one
// Open or Build call. It is mutex-guarded even though conversion is
// single-threaded today: a future parallel chapter pass would otherwise have to
// revisit every report site. A nil collector is valid and discards everything;
// call sites check it before formatting a detail, so the no-issue path stays
// free of allocation.
type issueCollector struct {
	mu    sync.Mutex
	seen  map[issueKey]struct{}
	list  []render.Issue
	count int
}

// newIssueCollector returns an empty collector. Collecting is unconditional:
// Book.Issues reports the list even when no Options.OnIssue callback is set.
func newIssueCollector() *issueCollector { return &issueCollector{} }

// add records one diagnostic with a printf-style detail. It is nil-safe and
// formats only when a collector exists.
func (c *issueCollector) add(code render.IssueCode, chapter string, node render.NodeID, format string, args ...any) {
	if c == nil {
		return
	}
	c.report(render.Issue{
		Code:    code,
		Chapter: chapter,
		Node:    node,
		Detail:  fmt.Sprintf(format, args...),
	})
}

// report records an already-built issue. Duplicates (same code, chapter, node
// and detail) are folded; the retained list is capped at maxReportedIssues.
func (c *issueCollector) report(i render.Issue) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[issueKey]struct{}, 8)
	}
	k := issueKey{i.Code, i.Chapter, i.Node, i.Detail}
	if _, dup := c.seen[k]; dup {
		return
	}
	c.seen[k] = struct{}{}
	c.count++
	if len(c.list) < maxReportedIssues {
		c.list = append(c.list, i)
	}
}

// flush freezes the list and appends the IssueSuppressed summary when the cap
// hid anything. It does not deliver: Open stores the result on the Book, and
// Build/BuildWith replay it to Options.OnIssue on the caller's goroutine after
// the conversion has finished.
func (c *issueCollector) flush() []render.Issue {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 0 && c.count == 0 {
		return nil
	}
	out := make([]render.Issue, len(c.list), len(c.list)+1)
	copy(out, c.list)
	if c.count > len(c.list) {
		out = append(out, render.Issue{
			Code:   render.IssueSuppressed,
			Node:   render.InvalidNode,
			Detail: fmt.Sprintf("%d further issues were not retained", c.count-len(c.list)),
		})
	}
	return out
}

// copyIssues returns an independent copy, so a caller cannot mutate the list a
// Book keeps.
func copyIssues(list []render.Issue) []render.Issue {
	if len(list) == 0 {
		return nil
	}
	out := make([]render.Issue, len(list))
	copy(out, list)
	return out
}
