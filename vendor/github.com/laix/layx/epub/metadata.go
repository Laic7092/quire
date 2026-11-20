package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"path"
	"strings"
)

// ErrNotArchive reports a file that is not a readable zip container. A shelf
// scanning a directory skips such a file; a zip whose package document is
// missing or broken is a different, later failure.
var ErrNotArchive = errors.New("epub: not a readable archive")

const (
	// maxCoverBytes caps the encoded cover a Metadata keeps. A shelf should not
	// hold every full-resolution cover in memory; a larger one is ignored.
	maxCoverBytes = 2 << 20
	// maxPackageBytes caps container.xml and the OPF document, which are a few
	// kilobytes in every real book. It also caps a cover page the guide names,
	// which is read only to find the image inside it.
	maxPackageBytes = 4 << 20
)

// Metadata is what a shelf needs from an EPUB without opening the book: the
// package document's title, creator and language, and the cover image's bytes.
// No spine item is read, so scanning a directory costs a few kilobytes per
// book instead of a full parse.
type Metadata struct {
	Title    string
	Creator  string
	Language string
	// Cover is the encoded cover image, or nil when the book has none (or one
	// that cannot be shown). CoverType is its media type, "image/jpeg" and so
	// on, taken from the manifest when it names the file.
	Cover     []byte
	CoverType string
}

// ReadMetadata reads an EPUB's container, its package document and its cover
// image, and nothing else. The cover is discovered in the order EPUB defines:
// the manifest's cover-image property, the legacy <meta name="cover">
// reference, then the guide — which may name a wrapper XHTML page instead of
// the image, so its first <img> is taken.
//
// A file that is not a zip at all wraps ErrNotArchive. A book with no cover is
// not an error: Cover is nil.
func ReadMetadata(file string) (Metadata, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: %v", ErrNotArchive, err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[path.Clean(f.Name)] = f
	}
	pkg, opfPath, err := readPackage(files)
	if err != nil {
		return Metadata{}, err
	}

	meta := Metadata{
		Title:    firstNonEmpty(pkg.Metadata.Title),
		Creator:  firstNonEmpty(pkg.Metadata.Creator),
		Language: firstNonEmpty(pkg.Metadata.Language),
	}
	imageType := func(href string) string {
		if mt := pkg.mediaType(opfPath, href); strings.HasPrefix(strings.ToLower(mt), "image/") {
			return mt
		}
		return ""
	}
	cover := pkg.coverHref(opfPath, func(href string) bool { return imageType(href) != "" },
		func(name string) ([]byte, error) { return readFile(files, name, maxPackageBytes) })
	if cover == "" {
		return meta, nil
	}
	data, err := readFile(files, cover, maxCoverBytes)
	if err != nil || len(data) == 0 || len(data) > maxCoverBytes {
		return meta, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return meta, nil
	}
	meta.Cover = data
	meta.CoverType = imageType(cover)
	if meta.CoverType == "" {
		meta.CoverType = "image/" + format
	}
	return meta, nil
}

// opfDocument is the part of the OPF package document this module reads. XML
// matching is by local name, so dc:title and dc:creator are found through the
// default and the dc namespace alike.
type opfDocument struct {
	Metadata struct {
		Title    []string `xml:"title"`
		Creator  []string `xml:"creator"`
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

// readPackage reads META-INF/container.xml and the package document it names.
func readPackage(files map[string]*zip.File) (opfDocument, string, error) {
	var doc opfDocument
	containerXML, err := readFile(files, "META-INF/container.xml", maxPackageBytes)
	if err != nil {
		return doc, "", err
	}
	var container struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerXML, &container); err != nil {
		return doc, "", fmt.Errorf("epub: parse container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 || container.Rootfiles[0].FullPath == "" {
		return doc, "", errors.New("epub: container.xml names no package document")
	}
	opfPath := path.Clean(container.Rootfiles[0].FullPath)
	opfXML, err := readFile(files, opfPath, maxPackageBytes)
	if err != nil {
		return doc, "", err
	}
	if err := xml.Unmarshal(opfXML, &doc); err != nil {
		return doc, "", fmt.Errorf("epub: parse %s: %w", opfPath, err)
	}
	return doc, opfPath, nil
}

// readFile reads one archive entry, at most limit bytes when limit > 0.
func readFile(files map[string]*zip.File, name string, limit int64) ([]byte, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	f, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("epub: %s is missing from the archive", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("epub: read %s: %w", name, err)
	}
	defer rc.Close()
	if limit <= 0 {
		return io.ReadAll(rc)
	}
	// Read one byte past the cap so a larger entry is rejected instead of
	// silently truncated into a half image or a half package document.
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("epub: %s is larger than %d bytes", name, limit)
	}
	return data, nil
}

// mediaType returns the manifest's media type for an archive path, or "".
func (d opfDocument) mediaType(opfPath, href string) string {
	for _, it := range d.Manifest.Items {
		if resolve(opfPath, it.Href) == href {
			return it.MediaType
		}
	}
	return ""
}

// coverHref returns the archive path of the book's cover image. Candidates are
// tried in the order EPUB defines: the manifest's cover-image property, the
// legacy <meta name="cover"> reference, then the guide. A candidate may be a
// wrapper XHTML page rather than an image — EPUB 2 books do this — so the first
// <img> of such a page is followed. A book whose cover cannot be resolved at
// all returns "".
func (d opfDocument) coverHref(opfPath string, isImage func(string) bool, read func(string) ([]byte, error)) string {
	var candidates []string
	for _, it := range d.Manifest.Items {
		if hasProperty(it.Properties, "cover-image") {
			candidates = append(candidates, resolve(opfPath, it.Href))
		}
	}
	for _, m := range d.Metadata.Meta {
		if !strings.EqualFold(strings.TrimSpace(m.Name), "cover") {
			continue
		}
		id := strings.TrimSpace(m.Content)
		for _, it := range d.Manifest.Items {
			if it.ID == id {
				candidates = append(candidates, resolve(opfPath, it.Href))
			}
		}
	}
	for _, ref := range d.Guide.References {
		if strings.EqualFold(strings.TrimSpace(ref.Type), "cover") {
			candidates = append(candidates, resolve(opfPath, ref.Href))
		}
	}
	for _, href := range candidates {
		if isImage == nil || isImage(href) {
			return href
		}
		if read == nil {
			continue
		}
		data, err := read(href)
		if err != nil {
			continue
		}
		if img := firstImageHref(data); img != "" {
			if img = resolve(href, img); isImage == nil || isImage(img) {
				return img
			}
		}
	}
	return ""
}
