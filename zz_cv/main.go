// Measures what drawing a screenful into a ui.Canvas costs the frame: the
// submission (CPU) and the frame's own time, against the same scene drawn
// straight into the frame.
package main

import (
	"fmt"
	"image"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	w, h  = 1170, 1536
	lines = 26
	cols  = 40
)

var (
	frame   int
	last    time.Time
	gaps    []time.Duration
	submit  []time.Duration
	out     *os.File
	useCv   = true
	fresh   = len(os.Args) > 1 && os.Args[1] == "fresh"
	pool    []*ui.Canvas
	cv      *ui.Canvas
	atlas   *ui.Bitmap
	drawing = false
)

func main() {
	out, _ = os.Create("cv.txt")
	pix := image.NewRGBA(image.Rect(0, 0, 24, 32))
	for i := range pix.Pix {
		pix.Pix[i] = byte(i * 7)
	}
	atlas = ui.NewBitmap(pix)
	defer out.Close()
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "cv", Width: 800, Height: 700, Content: ui.View(view)})
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(out, "run:", err)
	}
}

// quads is a screenful of glyph quads: the shape of a tile's display list,
// one atlas bitmap drawn a thousand times.
func quads(p *ui.Painter, x, y float32) {
	for row := 0; row < lines; row++ {
		for c := 0; c < cols; c++ {
			p.Image(atlas, ui.Rect{X: x + float32(c)*28, Y: y + float32(row)*56, W: 24, H: 32}, ui.FillBox)
		}
	}
}

func view(c *ui.Context) {
	if frame >= 40 {
		summarize()
		mygo.App.Quit()
		return
	}
	ui.Box(c).Size(1, 1).Draw(func(p *ui.Painter, r ui.Rect) {
		now := time.Now()
		if !last.IsZero() {
			gaps = append(gaps, now.Sub(last))
		}
		last = now
		if useCv {
			if fresh {
				// A canvas of its own a frame: texture allocated and cleared anew.
				cv = ui.NewCanvas(w, h)
			} else {
				if n := len(pool); n < 4 {
					pool = append(pool, ui.NewCanvas(w, h))
				}
				cv = pool[frame%len(pool)]
			}
			// One new canvas a frame: the bake, submitted and drawn by the GPU.
			start := time.Now()
			cv.Draw(p, func(cp *ui.Painter) { quads(cp, 0, 0) })
			submit = append(submit, time.Since(start))
			p.Image(cv.Bitmap(), ui.Rect{W: float32(w) / 3, H: float32(h) / 3}, ui.FillBox)
		} else {
			quads(p, 0, 0)
		}
		frame++
		p.AnimationFrame()
	})
}

func summarize() {
	var s, mx, sub time.Duration
	for _, d := range gaps {
		s += d
		if d > mx {
			mx = d
		}
	}
	for _, d := range submit {
		sub += d
	}
	n := time.Duration(max(1, len(gaps)))
	fmt.Fprintf(out, "fresh=%v canvas=%v frames=%d gap avg=%v max=%v | cv.Draw avg=%v (n=%d)\n",
		fresh, useCv, len(gaps), s/n, mx, sub/time.Duration(max(1, len(submit))), len(submit))
	out.Sync()
}
