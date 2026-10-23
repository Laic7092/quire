// Package book reads an EPUB into chapters and turns them into the render
// library's document IR.
//
// The EPUB layer is deliberately small: it reads the OPF package document for
// the spine order and metadata, then hands every spine item's raw XHTML to the
// converter below. There is no CSS engine and no image support.
package book

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/laix/layx/render"
)

// Chapter is one spine item: a title for the UI and the original XHTML.
type Chapter struct {
	Title string
	XHTML []byte
}

// Book is an EPUB opened for reading.
type Book struct {
	Title    string
	Language string
	Chapters []Chapter
}

// Open reads an EPUB file and returns its reading order.
func Open(file string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("book: open %s: %w", file, err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[path.Clean(f.Name)] = f
	}
	read := func(name string) ([]byte, error) {
		name = path.Clean(strings.TrimPrefix(name, "/"))
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("book: %s is missing from the archive", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("book: read %s: %w", name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	containerXML, err := read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerXML, &container); err != nil {
		return nil, fmt.Errorf("book: parse container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 || container.Rootfiles[0].FullPath == "" {
		return nil, errors.New("book: container.xml names no package document")
	}
	opfPath := path.Clean(container.Rootfiles[0].FullPath)

	opfXML, err := read(opfPath)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Metadata struct {
			Title    []string `xml:"title"`
			Language []string `xml:"language"`
		} `xml:"metadata"`
		Manifest struct {
			Items []struct {
				ID         string `xml:"id,attr"`
				Href       string `xml:"href,attr"`
				MediaType  string `xml:"media-type,attr"`
				Properties string `xml:"properties,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			Toc     string `xml:"toc,attr"`
			ItemRef []struct {
				IDRef  string `xml:"idref,attr"`
				Linear string `xml:"linear,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return nil, fmt.Errorf("book: parse %s: %w", opfPath, err)
	}

	b := &Book{Language: firstNonEmpty(pkg.Metadata.Language)}
	for _, t := range pkg.Metadata.Title {
		if strings.TrimSpace(t) != "" {
			b.Title = strings.TrimSpace(t)
			break
		}
	}

	type item struct {
		href       string
		mediaType  string
		properties string
	}
	manifest := make(map[string]item, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		manifest[it.ID] = item{
			href:       resolve(opfPath, it.Href),
			mediaType:  it.MediaType,
			properties: it.Properties,
		}
	}

	// Table of contents titles, keyed by archive path.
	toc := map[string]string{}
	for _, it := range pkg.Manifest.Items {
		if hasProperty(it.Properties, "nav") {
			if data, err := read(resolve(opfPath, it.Href)); err == nil {
				mergeTitles(toc, parseNavTOC(data, resolve(opfPath, it.Href)))
			}
		}
	}
	if len(toc) == 0 && pkg.Spine.Toc != "" {
		if it, ok := manifest[pkg.Spine.Toc]; ok {
			if data, err := read(it.href); err == nil {
				mergeTitles(toc, parseNCX(data, it.href))
			}
		}
	}

	seen := make(map[string]bool)
	for _, ref := range pkg.Spine.ItemRef {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		it, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}
		if it.mediaType != "" && !isHTMLMediaType(it.mediaType) {
			continue
		}
		if seen[it.href] {
			continue
		}
		seen[it.href] = true
		data, err := read(it.href)
		if err != nil {
			return nil, err
		}
		title := toc[it.href]
		if title == "" {
			title = headingTitle(data)
		}
		if title == "" {
			title = fmt.Sprintf("第 %d 章", len(b.Chapters)+1)
		}
		b.Chapters = append(b.Chapters, Chapter{Title: title, XHTML: data})
	}
	if len(b.Chapters) == 0 {
		return nil, errors.New("book: the spine has no readable chapters")
	}
	if b.Title == "" {
		b.Title = "未命名书籍"
	}
	return b, nil
}

// Document converts the whole book into one render document. Headings and
// indents are relative to a base font size of 18 CSS pixels; DocumentSized
// takes another base so the UI can change the reading size.
func (b *Book) Document() (*render.Document, error) {
	return b.DocumentSized(18)
}

// DocumentSized is Document with an explicit base font size.
func (b *Book) DocumentSized(base float32) (*render.Document, error) {
	if base <= 0 {
		base = 18
	}
	builder := render.NewDocumentBuilder().
		SetStyles(documentStyles(base)).
		SetMetadata(render.Metadata{
			Title:      b.Title,
			Language:   b.Language,
			Reflowable: true,
		})
	for i, ch := range b.Chapters {
		chapter, err := convertChapter(ch.XHTML, strconv.Itoa(i), render.AnchorID(i+1))
		if err != nil {
			return nil, fmt.Errorf("book: chapter %d (%s): %w", i+1, ch.Title, err)
		}
		if err := builder.AddChapter(chapter); err != nil {
			return nil, fmt.Errorf("book: chapter %d (%s): %w", i+1, ch.Title, err)
		}
	}
	if len(b.Chapters) == 0 {
		return nil, errors.New("book: no chapters to lay out")
	}
	return builder.Build()
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasProperty(props, want string) bool {
	for _, p := range strings.Fields(props) {
		if p == want {
			return true
		}
	}
	return false
}

func isHTMLMediaType(mt string) bool {
	switch strings.ToLower(mt) {
	case "application/xhtml+xml", "text/html", "application/x-dtbook+xml":
		return true
	}
	return false
}

// resolve joins href to the directory of base and strips any fragment.
func resolve(base, href string) string {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	if href == "" {
		return path.Clean(base)
	}
	href = strings.TrimPrefix(href, "/")
	// Zip paths have no root: every href is relative to the document that
	// names it, and a leading slash only means "from the archive root".
	return path.Clean(path.Join(path.Dir(base), href))
}

func mergeTitles(dst, src map[string]string) {
	for k, v := range src {
		if _, ok := dst[k]; !ok && strings.TrimSpace(v) != "" {
			dst[k] = strings.TrimSpace(v)
		}
	}
}
