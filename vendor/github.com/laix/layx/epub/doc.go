// Package epub reads an EPUB container and turns its spine into the render
// package's document IR. It is a separate module on purpose: the layout core
// does not import it, so a host that already has a compiler never pays for the
// XHTML/OPF dependencies.
//
// The boundary is the same one described in layx/docs/design.md:
//
//	EPUB container / OPF / nav / XHTML  →  render.Document
//
// This package owns the first half. It does not lay out, shape, rasterize or
// paint; it hands a Document to render.Engine. It owns a CSS subset too: the
// loading, selector matching, cascade and render.Style mapping live here, never
// in render, because the styles a book needs change faster than the layout
// model. The supported CSS surface is deliberately a census-driven subset.
//
// The navigation document is not discarded: Book.Toc keeps its order, nesting
// and #fragment targets, and each target carries a render.AnchorID that
// Layout.Locate resolves to a page or scroll offset. In-book links keep their
// destination in render.Node.Link, which Book.ResolveLink turns into the same
// kind of chapter + anchor pair; external URLs are left for the host.
//
// ReadMetadata covers the other reason a host opens the container: a shelf that
// wants a title and a cover and nothing else does not need a Document. It reads
// the package document and the cover image, opens no spine item, and reports a
// file that is not a zip as ErrNotArchive so a directory scan can skip it.
//
// Nothing is dropped silently. Content the converter or the cascade cannot
// represent (a linearized <table>, an image whose bytes are missing, a ruby
// annotation, an @font-face font) is reported through Options.OnIssue and
// Book.Issues, with the chapter's archive path in the detail, so a reader can
// tell the user which page lost something instead of showing a blank spot with
// no explanation.
package epub
