package library

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// errNotArchive reports a file that cannot be opened as a zip. Scan skips
// such a file; a zip whose package document is missing or broken is listed
// under its file name instead, as a book that may still open later.
var errNotArchive = errors.New("library: not a readable archive")

// maxCoverBytes caps the encoded cover kept per book. A shelf should not hold
// every full-resolution cover in memory; a larger one is ignored.
const maxCoverBytes = 2 << 20

// maxPackageBytes caps container.xml and the OPF document, which are a few
// kilobytes in every real book.
const maxPackageBytes = 4 << 20

// epubMeta is what a shelf needs from an EPUB without opening the book.
type epubMeta struct {
	Title    string
	Creator  string
	Language string
	Cover    []byte
}

// readEPUBMeta reads META-INF/container.xml, the OPF package document it
// names, and the cover image. Spine items are never opened, so this costs a
// few kilobytes rather than the whole book.
func readEPUBMeta(file string) (epubMeta, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return epubMeta{}, fmt.Errorf("%w: %v", errNotArchive, err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[path.Clean(f.Name)] = f
	}
	read := func(name string, limit int64) ([]byte, error) {
		name = path.Clean(strings.TrimPrefix(name, "/"))
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("library: %s is missing from the archive", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, limit+1))
	}

	containerXML, err := read("META-INF/container.xml", maxPackageBytes)
	if err != nil {
		return epubMeta{}, err
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerXML, &container); err != nil {
		return epubMeta{}, fmt.Errorf("library: parse container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 || container.Rootfiles[0].FullPath == "" {
		return epubMeta{}, fmt.Errorf("library: %s names no package document", file)
	}
	opfPath := path.Clean(strings.TrimPrefix(container.Rootfiles[0].FullPath, "/"))

	opfXML, err := read(opfPath, maxPackageBytes)
	if err != nil {
		return epubMeta{}, err
	}
	// encoding/xml matches element names by local name, so dc:title and
	// dc:creator are found through the default and the dc namespace alike.
	var pkg struct {
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
		Guide struct {
			References []struct {
				Href string `xml:"href,attr"`
				Type string `xml:"type,attr"`
			} `xml:"reference"`
		} `xml:"guide"`
	}
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return epubMeta{}, fmt.Errorf("library: parse %s: %w", opfPath, err)
	}

	meta := epubMeta{
		Title:    firstNonEmpty(pkg.Metadata.Title),
		Creator:  firstNonEmpty(pkg.Metadata.Creator),
		Language: firstNonEmpty(pkg.Metadata.Language),
	}

	manifest := make(map[string]string, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		manifest[item.ID] = resolveHref(opfPath, item.Href)
	}
	cover := ""
	// Cover discovery follows EPUB: properties="cover-image" first, then
	// the legacy <meta name="cover"> manifest reference, then the guide.
	for _, item := range pkg.Manifest.Items {
		if hasProperty(item.Properties, "cover-image") {
			cover = resolveHref(opfPath, item.Href)
			break
		}
	}
	if cover == "" {
		for _, m := range pkg.Metadata.Meta {
			if strings.EqualFold(strings.TrimSpace(m.Name), "cover") {
				cover = manifest[strings.TrimSpace(m.Content)]
				break
			}
		}
	}
	if cover == "" {
		for _, ref := range pkg.Guide.References {
			if strings.EqualFold(strings.TrimSpace(ref.Type), "cover") {
				cover = resolveHref(opfPath, ref.Href)
				break
			}
		}
	}
	if cover != "" {
		// A guide cover may name a wrapper XHTML page instead of the
		// image; without an HTML parser only an image is taken.
		if data, err := read(cover, maxCoverBytes); err == nil && len(data) <= maxCoverBytes && isImage(data) {
			meta.Cover = data
		}
	}
	return meta, nil
}

// resolveHref resolves an EPUB href against the document that names it, as
// book.resolve does: zip paths have no root, so a leading slash only means
// "from the archive root".
func resolveHref(base, href string) string {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	if href == "" {
		return path.Clean(base)
	}
	return path.Clean(path.Join(path.Dir(base), strings.TrimPrefix(href, "/")))
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

// isImage reports whether data starts like a PNG or a JPEG, the two formats
// a shelf cover is kept in.
func isImage(data []byte) bool {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return true
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return true
	}
	return false
}
