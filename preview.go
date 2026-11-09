package main

import (
	"context"
	"time"

	"github.com/laix/layx/render"
	"github.com/laix/quire/internal/reader"
)

// A window resize is the one gesture the reader cannot wait out. A width change
// is a new break for every chapter of the book — the measure is what lines are
// broken at — so laying the whole book out for it costs hundreds of milliseconds
// and hundreds of megabytes on a long novel. Doing that once per pixel of a drag
// is what made the sheet sit still until the last job landed: dozens of full
// books were shaped, the wanted one behind all of them, and the garbage they
// made stalled the very thread that draws.
//
// So a resize is answered in two steps instead. The chapters on screen are laid
// out for the new sheet at once, in this file, and drawn from that moment: the
// text reflows under the pointer. The whole book is laid out afterwards, once the
// sheet holds still (resizeDelay), off the UI thread, with the job before it
// cancelled — and when it lands it replaces the preview and takes the page
// numbering with it.
//
// The preview never touches what the reader's place means: a.page, the panels
// and the folio all keep naming the published layout, and only the sheet and the
// pointer read the preview (sheetPlan and columnPlan). A page the preview does
// not hold — the book jumped elsewhere while it was in flight — falls back to the
// published layout for that frame rather than drawing the wrong text.

// resizeDelay is how long the sheet has to hold still before the whole book is
// laid out for it. It is the same quiet period a settings change waits
// (settingsDelay): long enough that a drag costs one layout, short enough that
// letting go of the window finishes the job in the same blink.
const resizeDelay = 160 * time.Millisecond

// previewChapters is how many chapters either side of the reading place the
// preview covers: the spread being shown, the next page after it, and enough of
// the column for a scroll to move before the whole book arrives. It is what
// keeps a preview cheap — the cost is these chapters, not the book.
const previewChapters = 2

// resizeStep is what a sheet change asks for on one frame, from (*app).resizeStep.
type resizeStep int

const (
	// resizeIdle: the sheet is the one the published layout was built for.
	resizeIdle resizeStep = iota
	// resizePreview: the sheet moved; the chapters on screen are laid out at the
	// new size and the whole book waits for it to hold still.
	resizePreview
	// resizeGo: the sheet has held still long enough; lay the whole book out.
	resizeGo
)

// resizeStep is what a sheet change asks for this frame: nothing, the chapters
// on screen laid out at the new size (resizePreview), or the whole book once the
// sheet has held still for resizeDelay (resizeGo). It keeps the state, so the
// three answers follow each other however often the frames ask.
func (a *app) resizeStep(sw, sh float32) resizeStep {
	if a.layout == nil || a.layW <= 0 || (sw == a.layW && sh == a.layH) {
		a.sheetSettled()
		return resizeIdle
	}
	if !a.sheetPending {
		a.sheetPending = true
		a.sheetAt = a.clock()
		return resizePreview
	}
	if a.clock().Sub(a.sheetAt) >= resizeDelay {
		a.sheetSettled()
		return resizeGo
	}
	return resizePreview
}

// beginResize answers a resize: it remembers the words the reader is on, drops
// the job the new size supersedes, and lays out the chapters on screen at the
// sheet being dragged to. The place is kept as a location and not as a page
// number, since a page number names another place once the measure changes; the
// whole book lands on it when it arrives.
func (a *app) beginResize(sw, sh float32, opts reader.Options) {
	if loc, ok := a.currentLoc(); ok {
		a.jumpLoc, a.jumpAt = loc, true
	}
	a.cancelLayout()
	a.buildPreview(sw, sh, opts)
}

// buildPreview lays the chapters around the reading place out at the sheet being
// dragged to. It runs on the UI thread and is cheap by construction: a chapter
// is shaped in well under a millisecond, and chaining off the previous preview
// keeps the engine and the variant cache warm. Nothing is published; the sheet
// draws from it until the whole book lands (see sheetPlan).
func (a *app) buildPreview(sw, sh float32, opts reader.Options) {
	if a.layout == nil || a.doc == nil || a.fonts == nil || a.book == nil ||
		a.docAt != opts.FontSize || a.layout.Document() != a.doc {
		// The document itself has to be built again for this change (a new
		// reading size), and that is the job's work, not the preview's.
		a.dropPreview()
		return
	}
	prev := a.layout
	if a.preview != nil && a.preview.Document() == a.doc {
		// Chained through the last preview, which shares the published layout's
		// engine and variant cache, so this pass pays for the chapters it names.
		prev = a.preview
	}
	place := a.readingChapter()
	chapters := len(a.book.Chapters)
	first := max(place-previewChapters, 0)
	last := min(place+previewChapters+1, chapters)
	if last <= first {
		a.dropPreview()
		return
	}
	l, err := reader.RelayoutRangeWithPalette(context.Background(), prev, a.fonts, a.palette(), sw, sh, opts, first, last)
	if err != nil {
		a.dropPreview()
		return
	}
	a.preview, a.previewW, a.previewH = l, sw, sh
	// The veils and the selection name document locations, not pages, so they
	// are rebuilt for the new geometry instead of disappearing while the book
	// catches up.
	a.refreshMarks()
	a.selQuads = a.sel.Quads(a.showing())
}

// dropPreview forgets the preview, if one was drawn: the whole book is back on
// the sheet, and the veils go with it to the layout being shown.
func (a *app) dropPreview() {
	if a.preview == nil {
		return
	}
	a.preview = nil
	a.previewW, a.previewH = 0, 0
	a.refreshMarks()
	a.selQuads = a.sel.Quads(a.showing())
}

// readingChapter is the chapter the reader is on: the one the page starts in, or
// the one under the viewport of a scrolling column.
func (a *app) readingChapter() int {
	if a.book == nil || a.layout == nil {
		return 0
	}
	if a.scrolling() {
		return max(a.currentChapter(), 0)
	}
	return reader.ChapterOfPage(a.layout, len(a.book.Chapters), a.page)
}

// showing is the layout the sheet draws from: the preview while the whole book
// catches up, else the published layout. Everything derived from document
// locations rather than page numbers — the selection, the search hits, the notes
// — is built against this one, so the veils lie on the text they name in either.
func (a *app) showing() *render.Layout {
	if a.preview != nil && a.layout != nil {
		return a.preview
	}
	return a.layout
}

// sheetPlan is the layout and the page of it that the sheet draws for page: the
// preview when it holds that place, else the published layout. A preview covers
// the chapters around the reading place only, so a page outside it — a jump the
// preview did not follow — falls back to the published layout rather than
// drawing the wrong text from the range it does hold.
func (a *app) sheetPlan(page int) (*render.Layout, int) {
	l := a.layout
	if a.preview == nil || l == nil {
		return l, page
	}
	loc, ok := l.LocationOfPage(page)
	if !ok {
		return l, page
	}
	at, ok := a.preview.PageOf(loc)
	if !ok {
		return l, page
	}
	return a.preview, at
}

// columnPlan is the column the sheet draws and the place in it: the preview,
// placed at the location the published column is showing, or the published
// column itself when the preview does not hold that place. The tile height comes
// with it, since a preview is cut for the sheet being dragged to and the
// published column for the sheet before it.
func (a *app) columnPlan() (l *render.Layout, y, tileH float32) {
	l, y, tileH = a.layout, a.scrollY, a.tileH
	if a.preview == nil || a.layout == nil {
		return l, y, tileH
	}
	at := clampf(a.scrollY, 0, a.maxScroll())
	loc, ok := a.layout.LocationAt(at)
	if !ok {
		return l, y, tileH
	}
	py, ok := a.preview.OffsetOf(loc)
	if !ok {
		return l, y, tileH
	}
	return a.preview, py, a.previewH
}
