// Package book reads an EPUB into chapters and turns them into the render
// library's document IR.
//
// The EPUB layer is deliberately small: it reads the OPF package document for
// the spine order and metadata, then hands every spine item's raw XHTML to the
// converter below. There is no CSS engine and no image support.
package book

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig in imageLoader
	_ "image/jpeg" // DecodeConfig in imageLoader
	_ "image/png"  // DecodeConfig in imageLoader
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/laix/layx/render"
)

// Chapter is one spine item: a title for the UI and the original XHTML.
type Chapter struct {
	Title string
	XHTML []byte
}

// Image is one image resource of the book. Data is the encoded file, ready
// for a painter to decode once; Width and Height are its intrinsic size in
// CSS pixels, which the paginator uses to reserve space.
type Image struct {
	MediaType     string
	Data          []byte
	Width, Height float32
}

// imageEntry assigns a stable ID to a manifest image before its bytes are
// needed. The ID is what a render node and the painter share.
type imageEntry struct {
	id        render.ImageID
	mediaType string
}

// Book is an EPUB opened for reading.
type Book struct {
	Title    string
	Language string
	Chapters []Chapter

	// Cover is the ID of the cover image and HasCover reports whether the
	// book has one. The UI shows a cover on its own, before the first page.
	Cover    render.ImageID
	HasCover bool

	path      string
	paths     []string // archive path of each chapter, parallel to Chapters
	images    map[string]imageEntry
	coverHref string
}

// openEPUB reads an EPUB file and returns its reading order. The archive
// stays closed: Book.Build reopens it only when it needs image bytes, so keep
// the file where it was opened from.
func openEPUB(file string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("book: open %s: %w", file, err)
	}
	defer zr.Close()

	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}

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
			Meta     []struct {
				Name    string `xml:"name,attr"`
				Content string `xml:"content,attr"`
			} `xml:"meta"`
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
		Guide struct {
			References []struct {
				Href string `xml:"href,attr"`
				Type string `xml:"type,attr"`
			} `xml:"reference"`
		} `xml:"guide"`
	}
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return nil, fmt.Errorf("book: parse %s: %w", opfPath, err)
	}

	b := &Book{Language: firstNonEmpty(pkg.Metadata.Language), path: abs, images: map[string]imageEntry{}}
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

	// Image IDs are stable for the life of the Book, even though the bytes
	// are read lazily by Build.
	nextImage := render.ImageID(0)
	for _, it := range pkg.Manifest.Items {
		if !strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
			continue
		}
		href := resolve(opfPath, it.Href)
		if _, ok := b.images[href]; ok {
			continue
		}
		b.images[href] = imageEntry{id: nextImage, mediaType: it.MediaType}
		nextImage++
	}

	// Cover discovery, in the order EPUB itself defines it.
	for _, it := range pkg.Manifest.Items {
		if hasProperty(it.Properties, "cover-image") {
			b.coverHref = resolve(opfPath, it.Href)
			break
		}
	}
	if b.coverHref == "" {
		for _, m := range pkg.Metadata.Meta {
			if !strings.EqualFold(strings.TrimSpace(m.Name), "cover") {
				continue
			}
			if it, ok := manifest[strings.TrimSpace(m.Content)]; ok {
				b.coverHref = it.href
			}
		}
	}
	if b.coverHref == "" {
		for _, ref := range pkg.Guide.References {
			if !strings.EqualFold(strings.TrimSpace(ref.Type), "cover") {
				continue
			}
			href := resolve(opfPath, ref.Href)
			if _, ok := b.images[href]; ok {
				b.coverHref = href
				break
			}
			if data, err := read(href); err == nil {
				if img := firstImageHref(data); img != "" {
					b.coverHref = resolve(href, img)
					break
				}
			}
		}
	}
	if e, ok := b.images[b.coverHref]; ok {
		b.Cover, b.HasCover = e.id, true
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
	var chapterPaths []string
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
		chapterPaths = append(chapterPaths, it.href)
	}

	// A converter that bundles no cover metadata still usually starts with a
	// page that is nothing but the cover image. Treat it as the cover.
	if !b.HasCover && len(b.Chapters) > 0 && imageOnly(b.Chapters[0].XHTML) {
		if href := firstImageHref(b.Chapters[0].XHTML); href != "" {
			path := resolve(chapterPaths[0], href)
			if e, ok := b.images[path]; ok {
				b.Cover, b.coverHref, b.HasCover = e.id, path, true
			}
		}
	}

	// A cover page is shown by the UI on its own, not as a page of text.
	if b.HasCover {
		for i := 0; i < len(b.Chapters) && i < 5; i++ {
			if !imageOnly(b.Chapters[i].XHTML) {
				continue
			}
			href := firstImageHref(b.Chapters[i].XHTML)
			if href == "" || resolve(chapterPaths[i], href) != b.coverHref {
				continue
			}
			b.Chapters = append(b.Chapters[:i], b.Chapters[i+1:]...)
			chapterPaths = append(chapterPaths[:i], chapterPaths[i+1:]...)
			break
		}
	}
	b.paths = chapterPaths
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
	doc, _, err := b.Build(base)
	return doc, err
}

// Build converts the whole book into one render document and returns the
// image bytes its chapters reference, keyed by the IDs the nodes carry. The
// caller hands those bytes to its painter once; the map is per build, while
// the IDs stay stable for the life of the Book. The archive is reopened here
// and closed before Build returns.
func (b *Book) Build(base float32) (*render.Document, map[render.ImageID]Image, error) {
	if base <= 0 {
		base = 18
	}
	if len(b.Chapters) == 0 {
		return nil, nil, errors.New("book: no chapters to lay out")
	}

	load := &imageLoader{book: b, out: map[render.ImageID]Image{}}
	if b.path != "" && len(b.images) > 0 {
		zr, err := zip.OpenReader(b.path)
		if err != nil {
			return nil, nil, fmt.Errorf("book: reopen %s: %w", b.path, err)
		}
		defer zr.Close()
		load.zip = zr
		load.files = make(map[string]*zip.File, len(zr.File))
		for _, f := range zr.File {
			load.files[path.Clean(f.Name)] = f
		}
	}

	builder := render.NewDocumentBuilder().
		SetStyles(documentStyles(base)).
		SetMetadata(render.Metadata{
			Title:      b.Title,
			Language:   b.Language,
			Reflowable: true,
		})
	for i, ch := range b.Chapters {
		basePath := ""
		if i < len(b.paths) {
			basePath = b.paths[i]
		}
		chapter, err := convertChapter(ch.XHTML, strconv.Itoa(i), render.AnchorID(i+1), load.forBase(basePath))
		if err != nil {
			return nil, nil, fmt.Errorf("book: chapter %d (%s): %w", i+1, ch.Title, err)
		}
		if err := builder.AddChapter(chapter); err != nil {
			return nil, nil, fmt.Errorf("book: chapter %d (%s): %w", i+1, ch.Title, err)
		}
	}
	// The cover page is outside the reading flow, but its image is still
	// needed for the cover itself.
	if b.HasCover {
		load.load(b.coverHref, 0, 0)
	}
	doc, err := builder.Build()
	if err != nil {
		return nil, nil, err
	}
	return doc, load.out, nil
}

// imageLoader reads images from the reopened archive on demand and remembers
// what one build already read.
type imageLoader struct {
	book  *Book
	zip   *zip.ReadCloser
	files map[string]*zip.File
	out   map[render.ImageID]Image
}

func (l *imageLoader) forBase(base string) ImageResolver {
	return func(href string, widthHint, heightHint float32) (render.ImageID, float32, float32, bool) {
		if strings.TrimSpace(href) == "" {
			return 0, 0, 0, false
		}
		return l.load(resolve(base, href), widthHint, heightHint)
	}
}

func (l *imageLoader) load(name string, widthHint, heightHint float32) (render.ImageID, float32, float32, bool) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	entry, ok := l.book.images[name]
	if !ok {
		return 0, 0, 0, false
	}
	if img, ok := l.out[entry.id]; ok {
		return entry.id, img.Width, img.Height, true
	}
	if l.files == nil {
		return 0, 0, 0, false
	}
	f, ok := l.files[name]
	if !ok {
		return 0, 0, 0, false
	}
	rc, err := f.Open()
	if err != nil {
		return 0, 0, 0, false
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return 0, 0, 0, false
	}
	w, h := widthHint, heightHint
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
		w, h = float32(cfg.Width), float32(cfg.Height)
	}
	if w <= 0 && h <= 0 {
		return 0, 0, 0, false
	}
	l.out[entry.id] = Image{MediaType: entry.mediaType, Data: data, Width: w, Height: h}
	return entry.id, w, h, true
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
