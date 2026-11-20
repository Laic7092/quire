package render

import (
	"fmt"
	"sync"
)

// IssueCode classifies one diagnostic issue reported during a layout.
//
// The library never fails silently: content it cannot represent, fonts it
// cannot match and glyphs it cannot draw are reported through Options.OnIssue
// and Layout.Issues instead of disappearing without a trace.
type IssueCode uint8

const (
	// IssueMissingFont reports that a requested FontFamily has no face in the
	// FontSet. The text was laid out with a fallback face.
	IssueMissingFont IssueCode = iota + 1
	// IssueMissingGlyphs reports runes no configured face can draw. The runes
	// were shaped as .notdef (usually a blank or a box) and layout continued.
	IssueMissingGlyphs
	// IssueDroppedContent reports input content the IR or the converter cannot
	// express (an image without bytes, a table the EPUB converter had to
	// linearize, a ruby annotation, ...). It is dropped from the output.
	IssueDroppedContent
	// IssueSuppressed is the summary entry a full issue list ends with: Detail
	// holds how many further issues were not retained. It keeps a pathological
	// document from making the diagnostic list itself the memory problem.
	IssueSuppressed
)

// String implements fmt.Stringer.
func (c IssueCode) String() string {
	switch c {
	case IssueMissingFont:
		return "missing-font"
	case IssueMissingGlyphs:
		return "missing-glyphs"
	case IssueDroppedContent:
		return "dropped-content"
	case IssueSuppressed:
		return "suppressed"
	}
	return fmt.Sprintf("issue(%d)", uint8(c))
}

// Issue is one diagnostic reported by a layout.
//
// Chapter and Node identify where the issue came from when the library can:
// Chapter is the chapter ID and Node the node inside it, or InvalidNode when the
// issue is not tied to one node.
type Issue struct {
	Code    IssueCode
	Chapter string
	Node    NodeID
	Detail  string
}

// String implements fmt.Stringer for logs.
func (i Issue) String() string {
	s := i.Code.String()
	if i.Chapter != "" {
		s += fmt.Sprintf(" chapter %q", i.Chapter)
	}
	if i.Node != InvalidNode {
		s += fmt.Sprintf(" node %d", i.Node)
	}
	if i.Detail != "" {
		s += ": " + i.Detail
	}
	return s
}

// maxIssues caps the per-layout diagnostic list. A document with millions of
// missing glyphs must not turn diagnostics into the memory problem; the list
// ends with one IssueSuppressed entry instead.
const maxIssues = 1024

// issueSink collects the issues of one layout call. report is safe for
// concurrent use: every worker that shapes text reports into the same sink.
type issueSink struct {
	mu    sync.Mutex
	on    func(Issue)
	seen  map[issueKey]struct{}
	list  []Issue
	count int // unique issues seen, including suppressed ones
}

type issueKey struct {
	code    IssueCode
	chapter string
	node    NodeID
	detail  string
}

// newIssueSink returns a sink. on may be nil: the list is still collected for
// Layout.Issues.
func newIssueSink(on func(Issue)) *issueSink {
	return &issueSink{on: on}
}

// report records one issue. Duplicates (same code, chapter, node and detail)
// are folded; the list is capped at maxIssues.
func (s *issueSink) report(i Issue) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[issueKey]struct{}, 8)
	}
	k := issueKey{i.Code, i.Chapter, i.Node, i.Detail}
	if _, dup := s.seen[k]; dup {
		return
	}
	s.seen[k] = struct{}{}
	s.count++
	if len(s.list) < maxIssues {
		s.list = append(s.list, i)
	}
}

// add is report with printf-style detail formatting.
func (s *issueSink) add(code IssueCode, chapter string, node NodeID, format string, args ...any) {
	if s == nil {
		return
	}
	s.report(Issue{Code: code, Chapter: chapter, Node: node, Detail: fmt.Sprintf(format, args...)})
}

// flush freezes the list, appends the suppressed summary when needed and
// delivers every retained issue to the callback. It is called once per layout,
// on the goroutine that started the layout, after all workers have joined, so
// the callback never runs concurrently with itself.
func (s *issueSink) flush() []Issue {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	out := make([]Issue, len(s.list), len(s.list)+1)
	copy(out, s.list)
	if s.count > len(s.list) {
		out = append(out, Issue{
			Code:   IssueSuppressed,
			Node:   InvalidNode,
			Detail: fmt.Sprintf("%d further issues were not retained", s.count-len(s.list)),
		})
	}
	on := s.on
	s.mu.Unlock()
	if on != nil {
		for _, i := range out {
			on(i)
		}
	}
	return out
}
