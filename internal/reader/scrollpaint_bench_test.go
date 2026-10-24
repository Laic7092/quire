package reader

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// benchScrollColumn builds the sample book as a scrolling column of
// 620×700 tiles, with a painter ready for the drawing.
func benchScrollColumn(b testing.TB) (*render.Layout, *UIPainter, float32) {
	b.Helper()
	bk, err := book.Open("../book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		b.Fatal(err)
	}
	path, err := FindFont("")
	if err != nil {
		b.Skipf("no font available: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		b.Fatal(err)
	}
	doc, images, err := bk.Build(DefaultFontSize)
	if err != nil {
		b.Fatal(err)
	}
	const w, h float32 = 620, 700
	l, err := BuildLayout(context.Background(), doc, fonts, w, h, Options{FontSize: DefaultFontSize, Scroll: true})
	if err != nil {
		b.Fatal(err)
	}
	p := NewUIPainter(nil, fonts)
	for id, data := range images {
		p.SetImage(id, data)
	}
	return l, p, h
}

// countPainter counts the glyphs the column asks the painter for.
type countPainter struct {
	*UIPainter
	runs, glyphs int
}

func (c *countPainter) DrawGlyphs(run paint.GlyphRun) {
	c.runs++
	c.glyphs += len(run.Glyphs)
	c.UIPainter.DrawGlyphs(run)
}

// drawViewport replays the tiles a 620×700 viewport covers into p, the way the
// reader's draw callback does, counting the glyphs it hands the painter.
func drawViewport(l *render.Layout, p *ui.Painter, counter *countPainter, scrollY float32) {
	view := ui.Rect{X: 0, Y: 0, W: 620, H: 700}
	p.Clip(view, 0, func() {
		first := max(int(scrollY/700)-1, 0)
		last := min(int((scrollY+view.H)/700), l.TileCount()-1)
		for i := last; i >= first; i-- {
			tile := l.Tile(i)
			if tile == nil {
				continue
			}
			box := ui.Rect{X: view.X, Y: view.Y + tile.Top() - scrollY, W: view.W, H: tile.Height()}
			counter.P, counter.dx, counter.dy = p, box.X, box.Y
			tile.Draw(counter)
		}
	})
}

// BenchmarkScrollViewportPaint paints the tiles a 620×700 viewport covers, as
// the reader does per frame, with and without skipping the runs that lie
// outside the column: a tile is one viewport tall, so the column asks for
// about three viewports' worth of glyphs.
func BenchmarkScrollViewportPaint(b *testing.B) {
	const scrollY float32 = 4000
	for _, skip := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			name := fmt.Sprintf("outlines/scale=%g", scale)
			if skip {
				name = fmt.Sprintf("skip-outside/scale=%g", scale)
			}
			b.Run(name, func(b *testing.B) {
				l, up, _ := benchScrollColumn(b)
				up.noCull = !skip
				counter := &countPainter{UIPainter: up}
				tt := ui.NewTester(func(c *ui.Context) {
					ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
						drawViewport(l, p, counter, scrollY)
						p.AnimationFrame()
					})
				}, 620, 700)
				tt.SetScale(scale)
				tt.Frame()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					tt.Advance(16 * time.Millisecond)
				}
				b.StopTimer()
				b.ReportMetric(float64(counter.glyphs)/float64(b.N), "glyphs/frame")
			})
		}
	}
}

// TestDrawGlyphsCullChangesNoPixel renders one viewport of a scrolling column
// with and without the run cull: the two images must be identical, so skipping
// what lies below the clip can never drop a line that shows.
func TestDrawGlyphsCullChangesNoPixel(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	const scrollY float32 = 4000

	draw := func(cull bool) *image.RGBA {
		up.noCull = !cull
		defer func() { up.noCull = false }()
		return ui.Render(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				counter := &countPainter{UIPainter: up}
				drawViewport(l, p, counter, scrollY)
			})
		}, 620, 700, 1)
	}

	culled, whole := draw(true), draw(false)
	if !bytes.Equal(culled.Pix, whole.Pix) {
		t.Fatal("the cull changed the painted column")
	}
}
