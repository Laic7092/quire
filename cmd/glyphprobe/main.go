// Command glyphprobe measures the frames of a scrolling column in a real
// window, on the GPU when the host has one, to compare drawing the glyphs from
// their outlines with drawing them from the atlas (see
// internal/reader/glyph_atlas.go).
//
// It opens a window, glides the sample book through it, and prints wall time and
// CPU time a frame before quitting on its own, so it runs unattended:
//
//	go run ./cmd/glyphprobe
//	LAYX_GLYPH_ATLAS=0 go run ./cmd/glyphprobe   # the outlines, for the A/B
//	MYGO_FRAME_STATS=1 go run ./cmd/glyphprobe   # mygo's own build/paint/present split
//
// A window is limited by the display's refresh rate, so the frames per second
// say little on their own: compare the two runs on one machine, and read
// cpu/frame, which is what a 144 Hz frame budget is spent from.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render"
)

func main() {
	frames := flag.Int("frames", 120, "frames to measure, after the warm-up")
	warmup := flag.Int("warmup", 40, "frames to draw before measuring")
	path := flag.String("path", "internal/book/testdata/samples/aq-zhuan.epub", "book to read")
	step := flag.Float64("step", 6.3, "scroll step a frame, in DIPs")
	width := flag.Int("width", 787, "window content width")
	height := flag.Int("height", 700, "window content height")
	cpuprof := flag.String("cpuprofile", "", "write a CPU profile here")
	flag.Parse()

	if *cpuprof != "" {
		f, err := os.Create(*cpuprof)
		if err != nil {
			log.Fatalf("glyphprobe: %v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("glyphprobe: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	fontPath, err := reader.FindFont("")
	if err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
	bk, err := book.Open(*path)
	if err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
	doc, images, err := bk.Build(reader.DefaultFontSize)
	if err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
	up := reader.NewUIPainter(nil, fonts)
	for id, data := range images {
		up.SetImage(id, data)
	}
	w, h := float32(*width), float32(*height)
	l, err := reader.BuildLayout(context.Background(), doc, fonts, w, h,
		reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
	mode := os.Getenv("LAYX_GLYPH_ATLAS")
	if mode == "" {
		mode = "by the renderer"
	}
	log.Printf("glyphprobe: font %s, %d tiles, atlas %s", fontPath, l.TileCount(), mode)

	p := &probe{
		layout: l, paint: up, frames: *frames, warmup: *warmup,
		step: float32(*step), scrollY: 4000, view: ui.Rect{W: w, H: h},
	}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:          "glyphprobe",
			Width:          *width,
			Height:         *height,
			UseContentSize: true,
			Content:        ui.View(p.frame),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatalf("glyphprobe: %v", err)
	}
}

// probe draws the column the reader would, one glide step a frame, and measures
// the frames after the warm-up.
type probe struct {
	layout  *render.Layout
	paint   *reader.UIPainter
	view    ui.Rect
	frames  int
	warmup  int
	step    float32
	scrollY float32

	n         int
	paints    int
	lastWall  time.Time
	lastCPU   cpuTime
	wall      []time.Duration
	cpu       []time.Duration
	collect   time.Duration // inside the draw callback, while measuring
	accelerat bool
	scale     float32
}

// frame is the window's view: it scrolls, draws the tiles of the column, and
// reports once it has measured enough.
func (p *probe) frame(c *ui.Context) {
	now := time.Now()
	cpu := readCPU()
	if p.n > 0 {
		p.wall = append(p.wall, now.Sub(p.lastWall))
		p.cpu = append(p.cpu, time.Duration(cpu-p.lastCPU))
	}
	p.lastWall, p.lastCPU = now, cpu
	p.n++
	if p.n == p.warmup {
		p.paint.ResetGlyphStats()
	}
	p.scrollY += p.step
	if p.scrollY > float32(p.layout.TileCount()-4)*p.view.H {
		p.scrollY = 2 * p.view.H // stay inside the book
	}

	ui.Box(c).Fill().Draw(func(pt *ui.Painter, r ui.Rect) {
		p.paints++
		p.accelerat, p.scale = pt.Accelerated(), pt.Scale()
		start := time.Now()
		pt.Clip(p.view, 0, func() {
			first := max(int(p.scrollY/p.view.H)-1, 0)
			last := min(int((p.scrollY+p.view.H)/p.view.H), p.layout.TileCount()-1)
			for i := last; i >= first; i-- {
				tile := p.layout.Tile(i)
				if tile == nil {
					continue
				}
				box := ui.Rect{X: p.view.X, Y: p.view.Y + tile.Top() - p.scrollY, W: p.view.W, H: tile.Height()}
				p.paint.DrawTile(pt, tile, box, p.view)
			}
		})
		// As the reader's draw callback does: what the atlas rasterized this
		// frame is uploaded before the frame is drawn.
		p.paint.FlushGlyphs()
		if p.n > p.warmup {
			p.collect += time.Since(start)
		}
	})
	c.AnimationFrame()
	if p.n >= p.warmup+p.frames {
		p.report()
		pprof.StopCPUProfile()
		mygo.App.Quit()
	}
}

// report prints the steady state of the glide: wall time a frame, which says
// whether the window kept up with the display, and CPU time a frame, which says
// what the frame cost wherever the pixels landed.
func (p *probe) report() {
	wall := p.wall[min(p.warmup, len(p.wall)):]
	cpu := p.cpu[min(p.warmup, len(p.cpu)):]
	stats := p.paint.GlyphStats()
	n := time.Duration(max(len(wall), 1))
	fmt.Printf("GLIDE gpu=%v scale=%g frames=%d paints=%d (%.2f/frame)\n",
		p.accelerat, p.scale, len(wall), p.paints, float64(p.paints)/float64(max(p.n, 1)))
	fmt.Printf("  wall/frame        mean=%s p50=%s p95=%s max=%s  (%.1f fps)\n",
		mean(wall), pct(wall, 50), pct(wall, 95), maxOf(wall), 1/mean(wall).Seconds())
	fmt.Printf("  cpu/frame         mean=%s p50=%s p95=%s max=%s\n",
		mean(cpu), pct(cpu, 50), pct(cpu, 95), maxOf(cpu))
	fmt.Printf("  draw callback     mean=%s (%.1f%% of cpu)\n",
		p.collect/n, 100*float64(p.collect)/float64(mean(cpu)*n))
	fmt.Printf("  glyphs/frame      runs=%d glyphs=%d lookups=%d hits=%.1f%% rasters=%d uploads=%d\n",
		stats.Runs/int(n), stats.Glyphs/int(n), stats.Lookups/int(n), stats.HitRate()*100,
		stats.Rasters, stats.Uploads)
	fmt.Printf("  atlas             slots=%d layers=%d bytes=%d\n", stats.Slots, stats.Layers, stats.Bytes)
}

func mean(d []time.Duration) time.Duration {
	var sum time.Duration
	for _, v := range d {
		sum += v
	}
	return sum / time.Duration(max(len(d), 1))
}

func pct(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(int(float64(len(s))*float64(p)/100), len(s)-1)]
}

func maxOf(d []time.Duration) time.Duration {
	var m time.Duration
	for _, v := range d {
		m = max(m, v)
	}
	return m
}
