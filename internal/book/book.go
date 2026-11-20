// Package book opens a book for the reader: an EPUB, or a plain text file.
//
// EPUB is not parsed here. The container, OPF, navigation document and the
// XHTML → render.Document conversion live in github.com/laic7092/layx/epub, a
// separate module of the render library, so the host does not maintain its own
// copy of them. Book and Chapter are that module's types; this package only
// adds the plain text path, which turns a .txt file into the same chapters an
// EPUB would produce so the rest of the pipeline never knows the difference.
package book

import "github.com/laic7092/layx/epub"

// Book is a book opened for reading. An EPUB keeps its archive path and image
// IDs in the fields the module owns; a text book is the same struct with only
// the exported fields set, which is what its Build expects.
type Book = epub.Book

// Chapter is one spine item: a title for the UI and the original XHTML.
type Chapter = epub.Chapter

// TOCEntry is one row of the book's navigation document, in reading order, with
// the chapter and render anchor it points at. An EPUB has it; a text book has
// no navigation document, so Book.Toc is empty and the chapters are the table
// of contents.
type TOCEntry = epub.TOCEntry

// Options are the host's choices for a build: the paper's link colour is the
// one this package adds, since the rest of the styling comes from the module.
type Options = epub.Options
