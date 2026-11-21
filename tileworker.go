package main

import (
	"image"
	"math"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/internal/reader"
)

// Drawing a tile into pixels is the one thing a scrolling frame cannot afford:
// on a phone a screenful of a book's glyphs is 20 to 34 ms drawn at once, which
// is longer than the refresh it has to fit in, and the frame it lands in misses
// — what the display answers by dropping the rate it draws at.
//
// So the tiles are drawn where the frame is not: a goroutine of the window's
// own, with an offscreen runtime of its own to draw through, the way a browser
// draws in a worker into an OffscreenCanvas. The frame only shows what came
// back, and asks again for what did not: a frame never waits on it, and a tile
// that is late costs the frame the replay of its display list, which is a
// millisecond, not the twenty it takes to draw it.
type tileWorker struct {
	fonts *reader.Fonts
	// up draws the tiles here, with a painter of its own: the window's painter
	// holds the glyphs and images of the frame it draws, and a second goroutine
	// drawing through it would be two threads in one cache.
	up *reader.UIPainter
	// jobs holds the tile asked for last, and no more: a tile the reader has
	// scrolled past is not worth drawing, and the next ask replaces it.
	jobs chan tileJob
	// done holds the tiles drawn, for the frame to take.
	done   chan tileDone
	closed chan struct{}
}

// tileJob is one tile to draw.
type tileJob struct {
	key   tileKey
	index int
	tile  *render.Tile
	// w and h are the tile's own size in DIPs, strip what its bitmap holds
	// above and below it, ratio the device pixels a DIP is drawn at.
	w, h  float32
	strip float32
	ratio float32
	fade  float32
	pal   reader.Palette
}

// tileDone is one tile drawn: its pixels, and what they were drawn for.
type tileDone struct {
	key   tileKey
	index int
	pix   *image.RGBA
}

// newTileWorker starts the worker of one window. A window without faces to read
// with gets none.
func newTileWorker(fonts *reader.Fonts) *tileWorker {
	if fonts == nil {
		return nil
	}
	w := &tileWorker{
		fonts:  fonts,
		up:     reader.NewUIPainter(nil, fonts),
		jobs:   make(chan tileJob, 1),
		done:   make(chan tileDone, 4),
		closed: make(chan struct{}),
	}
	go w.run()
	return w
}

// close stops the worker.
func (w *tileWorker) close() {
	if w == nil {
		return
	}
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
}

// ask hands a tile to the worker, replacing one it has not started: the frames
// of a scroll ask for what they are heading for, and what they passed is no
// longer worth the pixels.
func (w *tileWorker) ask(job tileJob) {
	if w == nil {
		return
	}
	select {
	case <-w.closed:
		return
	default:
	}
	select {
	case w.jobs <- job:
		return
	default:
	}
	select {
	case <-w.jobs:
	default:
	}
	select {
	case w.jobs <- job:
	default:
	}
}

// take returns the tiles drawn, without waiting for one.
func (w *tileWorker) take() []tileDone {
	if w == nil {
		return nil
	}
	var out []tileDone
	for {
		select {
		case d := <-w.done:
			out = append(out, d)
		default:
			return out
		}
	}
}

// run draws the tiles it is asked for, one at a time.
func (w *tileWorker) run() {
	for {
		var job tileJob
		select {
		case <-w.closed:
			return
		case job = <-w.jobs:
		}
		pix := w.draw(job)
		if pix == nil {
			continue
		}
		select {
		case <-w.closed:
			return
		case w.done <- tileDone{key: job.key, index: job.index, pix: pix}:
		default:
			// The frame has not taken the last one: drop the oldest rather than
			// hold the worker, a tile the reader has passed being worth nothing.
			select {
			case <-w.done:
			default:
			}
			select {
			case w.done <- tileDone{key: job.key, index: job.index, pix: pix}:
			case <-w.closed:
				return
			default:
			}
		}
	}
}

// draw renders one tile into pixels of its own: an offscreen runtime the size of
// the tile's bitmap, and the column's display list drawn into it at the ratio
// the screen shows.
func (w *tileWorker) draw(job tileJob) *image.RGBA {
	if job.tile == nil || !(job.ratio > 0) || job.w <= 0 || job.h <= 0 {
		return nil
	}
	cw := int(math.Ceil(float64(job.w) * float64(job.ratio)))
	ch := int(math.Ceil(float64(job.h+2*job.strip) * float64(job.ratio)))
	if cw <= 0 || ch <= 0 {
		return nil
	}
	w.up.SetPalette(job.pal)
	w.up.SetFade(job.fade)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(float32(cw), float32(ch)).Draw(func(p *ui.Painter, r ui.Rect) {
			box := ui.Rect{X: 0, Y: job.strip, W: job.w, H: job.h}
			view := ui.Rect{X: 0, Y: 0, W: job.w, H: job.h + 2*job.strip}
			w.up.DrawTileZoom(p, job.tile, box, view, job.ratio)
		})
	}, cw, ch)
	tt.Frame()
	return tt.Image()
}
