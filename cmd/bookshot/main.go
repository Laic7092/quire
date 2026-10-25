// Command bookshot renders one page of a book to a PNG without opening a
// window. It is the reader's headless path - book → render.Document → layout →
// layx-raster surface - so a machine without a display (CI, a server, a bug
// report) can still produce the page the reader would show.
//
//	go run ./cmd/bookshot -book internal/book/testdata/samples/aq-zhuan.epub -out page.png
//	go run ./cmd/bookshot -book book.epub -page 12 -theme original -scale 2 -out p12.png
//
// It draws through internal/reader.PageRenderer, which replays the same
// display list the app draws on screen. The on-screen painter is still
// UIPainter: this tool does not change what the reader shows.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
)

func main() {
	bookPath := flag.String("book", "", "path to an EPUB or TXT book")
	pageIndex := flag.Int("page", 0, "page number to render, from 0")
	out := flag.String("out", "page.png", "output PNG path")
	width := flag.Float64("width", 420, "page width in DIPs")
	height := flag.Float64("height", 600, "page height in DIPs")
	fontSize := flag.Float64("font-size", reader.DefaultFontSize, "reading size in DIPs")
	lineHeight := flag.Float64("line-height", 0, "line box as a multiple of the size (0: reader default)")
	margin := flag.Float64("margin", 0, "page inset in DIPs (0: reader default)")
	scale := flag.Float64("scale", 1, "device pixel ratio: 2 renders at twice the pixels")
	theme := flag.String("theme", "quiet", "paper theme: original, quiet, paper, bold, calm, focus")
	fontFlag := flag.String("font", "", "path to a .ttf/.otf/.ttc font (default: $LAYX_FONT or a system CJK font)")
	serifFlag := flag.String("serif", "", "path to the serif face (default: $LAYX_SERIF or a system serif)")
	flag.Parse()

	if *bookPath == "" {
		log.Fatal("bookshot: -book is required")
	}
	if *scale <= 0 {
		log.Fatal("bookshot: -scale must be positive")
	}

	// The palette decides the paper, ink and link colours baked into the
	// display list, so it is set before the layout is built.
	reader.SetPalette(reader.ForTheme(reader.ParseTheme(*theme)))

	fontPath, err := reader.FindFont(*fontFlag)
	if err != nil {
		log.Fatalf("bookshot: %v", err)
	}
	fonts, err := reader.OpenFonts(fontPath, reader.FindSerif(*serifFlag))
	if err != nil {
		log.Fatalf("bookshot: %v", err)
	}

	b, err := book.Open(*bookPath)
	if err != nil {
		log.Fatalf("bookshot: %v", err)
	}
	opts := reader.Options{
		FontSize:   float32(*fontSize),
		LineHeight: float32(*lineHeight),
		Margin:     float32(*margin),
		Justify:    true,
	}.Normalized()

	// The reader paints links itself, so the document is built without the
	// paper's link colour; the display list carries the ink and the sheet.
	doc, images, err := b.BuildWith(opts.FontSize, book.Options{ThemeLinks: true})
	if err != nil {
		log.Fatalf("bookshot: build %s: %v", b.Title, err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts,
		float32(*width), float32(*height), opts)
	if err != nil {
		log.Fatalf("bookshot: layout: %v", err)
	}
	page := layout.Page(*pageIndex)
	if page == nil {
		log.Fatalf("bookshot: page %d does not exist (%d pages, font %s)",
			*pageIndex, layout.PageCount(), fontPath)
	}

	renderer := reader.NewPageRenderer()
	decoded := 0
	for id, data := range images {
		if renderer.SetImage(id, data) {
			decoded++
		}
	}
	img := renderer.DrawPage(page, float32(*scale))

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("bookshot: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		log.Fatalf("bookshot: encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("bookshot: %v", err)
	}
	fmt.Printf("%s: page %d/%d, %d images, %dx%d -> %s\n",
		b.Title, *pageIndex, layout.PageCount(), decoded,
		img.Bounds().Dx(), img.Bounds().Dy(), *out)
}
