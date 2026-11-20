package render

import (
	"errors"
	"fmt"
)

// ErrInternal reports a bug inside the library rather than a problem with the
// caller's input: a panic recovered at a worker boundary and turned back into
// an error.
//
// Layout promises that no input, however malformed or hostile, can crash the
// host process: every failure is returned as an error. A host that sees
// ErrInternal has found a library bug; it should treat the layout as failed and
// may report the error, but must not assume the document is invalid.
var ErrInternal = errors.New("render: internal error")

// InvalidStyle is the sentinel StyleID of a DocumentError that does not name a
// style. StyleID is unsigned, so zero would be ambiguous with the first style.
const InvalidStyle StyleID = ^StyleID(0)

// DocumentError is an error with structured context.
//
// Every error the library returns wraps one of ErrInvalidDocument,
// ErrInvalidOptions, ErrNoFonts, ErrLimitExceeded, ErrClosed or ErrInternal, so
// errors.Is keeps working. DocumentError adds the identifiers a host needs to
// act: which chapter, node or style the problem came from, and a human-readable
// detail for a log or a UI message.
//
// The zero value of the context fields means "not applicable": Chapter is "",
// Node is InvalidNode and Style is InvalidStyle. Inspect them with a comparison,
// not by parsing Error():
//
//	var de *render.DocumentError
//	if errors.As(err, &de) && de.Chapter != "" {
//		host.JumpToChapter(de.Chapter)
//	}
type DocumentError struct {
	// Kind is the sentinel the error wraps (ErrInvalidDocument, ...).
	Kind error
	// Chapter is the ID of the chapter the error belongs to, or "".
	Chapter string
	// Node is the node inside Chapter, or InvalidNode.
	Node NodeID
	// Style is the StyleID involved, or InvalidStyle.
	Style StyleID
	// Detail is the human-readable explanation without any context prefix.
	Detail string
}

// Error implements error.
func (e *DocumentError) Error() string {
	if e == nil {
		return "<nil>"
	}
	kind := "render: error"
	if e.Kind != nil {
		kind = e.Kind.Error()
	}
	s := kind
	if e.Chapter != "" {
		s += fmt.Sprintf(": chapter %q", e.Chapter)
	}
	if e.Node != InvalidNode {
		s += fmt.Sprintf(": node %d", e.Node)
	}
	if e.Style != InvalidStyle {
		s += fmt.Sprintf(": style %d", e.Style)
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Unwrap returns Kind, so errors.Is and errors.As reach the sentinel.
func (e *DocumentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// docErrorf builds a DocumentError with no document context.
func docErrorf(kind error, format string, args ...any) *DocumentError {
	return &DocumentError{Kind: kind, Node: InvalidNode, Style: InvalidStyle, Detail: fmt.Sprintf(format, args...)}
}

// chapterErrorf builds a DocumentError naming a chapter.
func chapterErrorf(kind error, chapter, format string, args ...any) *DocumentError {
	return &DocumentError{Kind: kind, Chapter: chapter, Node: InvalidNode, Style: InvalidStyle, Detail: fmt.Sprintf(format, args...)}
}

// nodeErrorf builds a DocumentError naming a chapter and a node.
func nodeErrorf(kind error, chapter string, node NodeID, format string, args ...any) *DocumentError {
	return &DocumentError{Kind: kind, Chapter: chapter, Node: node, Style: InvalidStyle, Detail: fmt.Sprintf(format, args...)}
}

// styleErrorf builds a DocumentError naming a chapter, a node and a style.
func styleErrorf(kind error, chapter string, node NodeID, style StyleID, format string, args ...any) *DocumentError {
	return &DocumentError{Kind: kind, Chapter: chapter, Node: node, Style: style, Detail: fmt.Sprintf(format, args...)}
}
