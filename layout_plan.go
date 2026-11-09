package main

import "github.com/laix/quire/internal/reader"

// layoutGate is the decision ensureLayout makes before it starts or reuses a
// layout. Keeping the decision pure makes the many combinations of sheet,
// options, job state and failure state explicit and testable; the method that
// acts on it only has to perform the chosen operation.
type layoutGate int

const (
	// layoutGateTooNarrow: the sheet cannot hold the page margins.
	layoutGateTooNarrow layoutGate = iota
	// layoutGateWaitMeasure: the reading area has not been measured yet.
	layoutGateWaitMeasure
	// layoutGateNotReady: a book or its faces are still missing.
	layoutGateNotReady
	// layoutGateStableFailure: this exact request has already failed.
	layoutGateStableFailure
	// layoutGateLoading: the matching full-book job is already in flight.
	layoutGateLoading
	// layoutGateCurrent: the published layout is already the one requested.
	layoutGateCurrent
	// layoutGateWork: the request needs a preview, repaint, or full layout.
	layoutGateWork
)

// layoutGateInput is everything the decision needs. It deliberately contains
// values rather than *app so the transition table can be read and tested
// without constructing a window, a document, or a painter.
type layoutGateInput struct {
	width, height  float32
	sheetW, sheetH float32
	measured       bool
	bookReady      bool
	fontsReady     bool
	opts           reader.Options
	layOpts        reader.Options
	paper          pagePaper
	layPaper       pagePaper
	docAt          float32
	hasLayout      bool
	loading        bool
	layW, layH     float32
	failed         bool
	failW, failH   float32
	failFontSize   float32
}

// chooseLayoutGate follows the same precedence as ensureLayout: an invalid
// sheet is reported before missing measurements, an already failed request is
// stable, and a matching published layout wins over starting another job.
func chooseLayoutGate(in layoutGateInput) layoutGate {
	if in.sheetW < 3*in.opts.Margin || in.sheetH < 3*in.opts.Margin {
		return layoutGateTooNarrow
	}
	if !in.measured {
		return layoutGateWaitMeasure
	}
	if !in.bookReady || !in.fontsReady {
		return layoutGateNotReady
	}
	if in.failed && in.width == in.failW && in.height == in.failH && in.opts.FontSize == in.failFontSize {
		return layoutGateStableFailure
	}
	sameSheet := in.sheetW == in.layW && in.sheetH == in.layH
	if in.loading && sameSheet && in.opts.Same(in.layOpts) && in.paper == in.layPaper {
		return layoutGateLoading
	}
	if !in.loading && in.hasLayout && sameSheet && in.docAt == in.opts.FontSize &&
		in.opts.Same(in.layOpts) && in.paper == in.layPaper {
		return layoutGateCurrent
	}
	return layoutGateWork
}
