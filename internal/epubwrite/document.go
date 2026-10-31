package epubwrite

import (
	"fmt"
	"strconv"
	"strings"
)

// xmlHeader starts every XML document. XHTML documents additionally carry a
// plain HTML doctype so both XML parsers and browser-grade HTML parsers (which
// layx/epub uses) see the same document.
const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

const xhtmlNS = `xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"`

// opfDocument builds OEBPS/content.opf for the given chapters.
func opfDocument(b Book, chapters []Chapter, identifier, title, lang string, cover coverImage) []byte {
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">`)
	s.WriteString("<metadata>")
	s.WriteString(`<dc:identifier id="bookid" xmlns:dc="http://purl.org/dc/elements/1.1/">`)
	s.WriteString(escape(identifier))
	s.WriteString("</dc:identifier>")
	s.WriteString(`<dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">`)
	s.WriteString(escape(title))
	s.WriteString("</dc:title>")
	s.WriteString(`<dc:language xmlns:dc="http://purl.org/dc/elements/1.1/">`)
	s.WriteString(escape(lang))
	s.WriteString("</dc:language>")
	if author := strings.TrimSpace(b.Author); author != "" {
		s.WriteString(`<dc:creator xmlns:dc="http://purl.org/dc/elements/1.1/">`)
		s.WriteString(escape(author))
		s.WriteString("</dc:creator>")
	}
	if desc := strings.TrimSpace(b.Description); desc != "" {
		s.WriteString(`<dc:description xmlns:dc="http://purl.org/dc/elements/1.1/">`)
		s.WriteString(escape(desc))
		s.WriteString("</dc:description>")
	}
	if src := strings.TrimSpace(b.Source); src != "" {
		s.WriteString(`<dc:source xmlns:dc="http://purl.org/dc/elements/1.1/">`)
		s.WriteString(escape(src))
		s.WriteString("</dc:source>")
	}
	if cover.ok() {
		// Both forms: EPUB 3's cover-image property and the legacy meta tag
		// EPUB 2 readers (and layx/epub's fallback) look for.
		s.WriteString(`<meta name="cover" content="cover-image"/>`)
	}
	s.WriteString(`<meta property="dcterms:modified">1980-01-01T00:00:00Z</meta>`)
	s.WriteString("</metadata>")

	s.WriteString("<manifest>")
	s.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	s.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
	if cover.ok() {
		s.WriteString(`<item id="cover-image" href="` + cover.path + `" media-type="` + cover.mediaType + `" properties="cover-image"/>`)
	}
	for i := range chapters {
		s.WriteString(fmt.Sprintf(`<item id="chapter-%d" href="chapter-%d.xhtml" media-type="application/xhtml+xml"/>`, i+1, i+1))
	}
	s.WriteString("</manifest>")

	s.WriteString(`<spine toc="ncx">`)
	for i := range chapters {
		s.WriteString(fmt.Sprintf(`<itemref idref="chapter-%d"/>`, i+1))
	}
	s.WriteString("</spine>")

	if cover.ok() {
		s.WriteString(`<guide><reference type="cover" title="Cover" href="` + cover.path + `"/></guide>`)
	}
	s.WriteString("</package>\n")
	return []byte(s.String())
}

// navDocument builds the EPUB 3 navigation document. The first nav with
// epub:type="toc" is what a reader uses; one link per chapter keeps the table
// of contents flat and predictable.
func navDocument(chapters []Chapter, title, lang string) []byte {
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString("<!DOCTYPE html>\n")
	s.WriteString(`<html ` + xhtmlNS + ` xml:lang="` + escape(lang) + `" lang="` + escape(lang) + `">`)
	s.WriteString("<head>")
	s.WriteString(`<meta charset="utf-8"/>`)
	s.WriteString("<title>Contents</title>")
	s.WriteString("</head><body>")
	s.WriteString(`<nav epub:type="toc" id="toc"><h1>Contents</h1><ol>`)
	for i := range chapters {
		s.WriteString(`<li><a href="chapter-` + strconv.Itoa(i+1) + `.xhtml">`)
		s.WriteString(escape(chapterTitle(chapters[i].Title, i+1)))
		s.WriteString("</a></li>")
	}
	s.WriteString("</ol></nav>")
	s.WriteString("</body></html>\n")
	return []byte(s.String())
}

// ncxDocument builds the EPUB 2 navigation map. It mirrors nav.xhtml so the
// NCX fallback in layx/epub finds the same titles.
func ncxDocument(chapters []Chapter, identifier, title string) []byte {
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString(`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">`)
	s.WriteString("<head>")
	s.WriteString(`<meta name="dtb:uid" content="` + escape(identifier) + `"/>`)
	s.WriteString(`<meta name="dtb:depth" content="1"/>`)
	s.WriteString(`<meta name="dtb:totalPageCount" content="0"/>`)
	s.WriteString(`<meta name="dtb:maxPageNumber" content="0"/>`)
	s.WriteString("</head>")
	s.WriteString("<docTitle><text>" + escape(title) + "</text></docTitle>")
	s.WriteString("<navMap>")
	for i := range chapters {
		n := i + 1
		s.WriteString(`<navPoint id="navPoint-` + strconv.Itoa(n) + `" playOrder="` + strconv.Itoa(n) + `">`)
		s.WriteString("<navLabel><text>" + escape(chapterTitle(chapters[i].Title, n)) + "</text></navLabel>")
		s.WriteString(`<content src="chapter-` + strconv.Itoa(n) + `.xhtml"/>`)
		s.WriteString("</navPoint>")
	}
	s.WriteString("</navMap></ncx>\n")
	return []byte(s.String())
}

// chapterDocument wraps a sanitized body fragment in an XHTML document.
func chapterDocument(title, lang, body string) []byte {
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString("<!DOCTYPE html>\n")
	s.WriteString(`<html ` + xhtmlNS + ` xml:lang="` + escape(lang) + `" lang="` + escape(lang) + `">`)
	s.WriteString("<head>")
	s.WriteString(`<meta charset="utf-8"/>`)
	s.WriteString("<title>" + escape(title) + "</title>")
	s.WriteString("</head><body>")
	if strings.TrimSpace(body) == "" {
		// An empty chapter still needs a legal body; a paragraph keeps
		// readers that drop empty documents from losing the spine item.
		s.WriteString("<p></p>")
	} else {
		s.WriteString(body)
	}
	s.WriteString("</body></html>\n")
	return []byte(s.String())
}
