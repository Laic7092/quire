// layx-reader reads EPUB files with MyGo's native UI: the window is built from
// Go with package ui, page breaking and glyph positions come from the layx
// render engine, and a paint.Painter replays layx's display list through
// ui.Painter. There is no webview and no frontend.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render"
)

// app is the reader's state. The view builds the interface from it on the UI
// thread; background work publishes results through Window.Update.
type app struct {
	win   *mygo.Window
	fonts *reader.Fonts

	book  *book.Book
	doc   *render.Document
	docAt float32 // base font size doc was built with
	// layout is immutable once published; the draw callback only reads it.
	layout *render.Layout
	paint  *reader.UIPainter

	page      int
	fontSize  float32
	status    string
	file      string
	startPath string

	// Layout job bookkeeping. Only touched on the UI thread.
	layW, layH float32
	loading    bool
	job        int
	failed     bool
	failW      float32
	failH      float32
	failSize   float32

	body ui.Handle
}

func (a *app) view(c *ui.Context) {
	if a.layout != nil {
		if c.Shortcut(0, ui.KeyLeft) || c.Shortcut(0, ui.KeyPageUp) {
			a.turn(-1)
		}
		if c.Shortcut(0, ui.KeyRight) || c.Shortcut(0, ui.KeyPageDown) || c.Shortcut(0, ui.KeySpace) {
			a.turn(1)
		}
	}
	if c.Shortcut(0, ui.KeyMinus) {
		a.changeFont(-2)
	}
	if c.Shortcut(0, ui.KeyEqual) {
		a.changeFont(2)
	}

	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Gap(8).Padding(10, 14).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "打开").Clicked() {
				go a.chooseFile()
			}
			if ui.Button(c, "上一页").Disabled(a.layout == nil || a.page <= 0).Clicked() {
				a.turn(-1)
			}
			if ui.Button(c, "下一页").Disabled(a.layout == nil || a.page+1 >= a.layout.PageCount()).Clicked() {
				a.turn(1)
			}
			if ui.Button(c, "A-").Label("Smaller").Clicked() {
				a.changeFont(-2)
			}
			if ui.Button(c, "A+").Label("Larger").Clicked() {
				a.changeFont(2)
			}
			ui.Spacer(c)
			ui.Text(c, a.chapterLabel()).TextColor(t.TextMuted).MaxWidth(320)
			ui.Text(c, a.pageLabel()).TextColor(t.TextMuted)
		})

		ui.Box(c).Fill().Grow(1).Bind(&a.body).Draw(func(p *ui.Painter, r ui.Rect) {
			// Draw callbacks may run many times per frame and must not lay
			// anything out: only replay the current page.
			if a.layout == nil || a.paint == nil {
				return
			}
			if page := a.layout.Page(a.page); page != nil {
				a.paint.DrawPage(p, page, r)
			}
		}).Children(func() {
			if a.layout == nil {
				ui.Column(c).Fill().Center().Padding(24).Children(func() {
					ui.Text(c, a.status).TextColor(t.TextMuted)
				})
			}
		})
	})

	if b := a.body.Bounds(c); b.W > 0 && b.H > 0 {
		a.ensureLayout(b.W, b.H)
	}
}

func (a *app) pageLabel() string {
	if a.layout == nil {
		return "— / —"
	}
	return fmt.Sprintf("%d / %d", a.page+1, a.layout.PageCount())
}

func (a *app) chapterLabel() string {
	if a.book == nil {
		return ""
	}
	if a.layout == nil {
		return a.book.Title
	}
	i := reader.ChapterOfPage(a.layout, len(a.book.Chapters), a.page)
	if i < 0 || i >= len(a.book.Chapters) {
		return a.book.Title
	}
	return a.book.Chapters[i].Title
}

func (a *app) turn(delta int) {
	if a.layout == nil {
		return
	}
	page := a.page + delta
	if page < 0 {
		page = 0
	}
	if n := a.layout.PageCount(); page >= n {
		page = n - 1
	}
	a.page = page
}

func (a *app) changeFont(delta float32) {
	size := a.fontSize + delta
	if size < 12 {
		size = 12
	}
	if size > 36 {
		size = 36
	}
	if size == a.fontSize {
		return
	}
	a.fontSize = size
	a.layW, a.layH = 0, 0 // force a rebuild at the next frame
	a.failed = false
}

// ensureLayout starts background layout when the viewport or reading size
// changed. It runs on the UI thread and passes only copies to the goroutine;
// Window.Update publishes the result back on the UI thread.
func (a *app) ensureLayout(w, h float32) {
	if a.win == nil || a.book == nil || a.fonts == nil {
		return
	}
	if a.failed && w == a.failW && h == a.failH && a.fontSize == a.failSize {
		return
	}
	if a.loading && w == a.layW && h == a.layH {
		return
	}
	if !a.loading && a.layout != nil && w == a.layW && h == a.layH && a.docAt == a.fontSize {
		return
	}

	a.job++
	job := a.job
	a.loading = true
	a.layW, a.layH = w, h
	a.failed = false

	bookPtr, fonts, win := a.book, a.fonts, a.win
	doc, docAt := a.doc, a.docAt
	size := a.fontSize
	ctx := context.Background()

	go func() {
		if doc == nil || docAt != size {
			d, err := bookPtr.DocumentSized(size)
			if err != nil {
				win.Update(func() {
					if job != a.job {
						return
					}
					a.loading = false
					a.failed, a.failW, a.failH, a.failSize = true, w, h, size
					a.status = "排版失败：" + err.Error()
				})
				return
			}
			doc, docAt = d, size
		}
		layout, err := reader.BuildLayout(ctx, doc, fonts, w, h, size)
		win.Update(func() {
			if job != a.job {
				return
			}
			a.loading = false
			if err != nil {
				a.failed, a.failW, a.failH, a.failSize = true, w, h, size
				a.status = "排版失败：" + err.Error()
				return
			}
			a.doc, a.docAt = doc, docAt
			a.layout = layout
			if a.page >= layout.PageCount() {
				a.page = layout.PageCount() - 1
			}
			if a.page < 0 {
				a.page = 0
			}
			a.status = ""
		})
	}()
}

// chooseFile runs off the UI thread: the file dialog and EPUB parsing block.
func (a *app) chooseFile() {
	win := a.win
	if win == nil {
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  win,
		Title:   "打开 EPUB",
		Filters: []mygo.FileFilter{{Name: "EPUB", Extensions: []string{"epub"}}},
	})
	if err != nil {
		win.Update(func() { a.status = "打开失败：" + err.Error() })
		return
	}
	if len(paths) == 0 {
		return
	}
	a.openAsync(paths[0])
}

// openAsync parses a book off the UI thread and publishes it through
// Window.Update. The caller runs it in a goroutine.
func (a *app) openAsync(path string) {
	win := a.win
	if win == nil {
		return
	}
	b, err := book.Open(path)
	if err != nil {
		win.Update(func() { a.status = "无法读取：" + err.Error() })
		return
	}
	win.Update(func() {
		a.job++ // drop any layout still in flight
		a.loading = false
		a.failed = false
		a.book = b
		a.doc = nil
		a.docAt = 0
		a.layout = nil
		a.page = 0
		a.layW, a.layH = 0, 0
		a.file = path
		a.status = "《" + b.Title + "》"
		win.SetTitle("《" + b.Title + "》 — layx-reader")
	})
}

func main() {
	fontFlag := flag.String("font", "", "path to a .ttf/.otf/.ttc font (default: $LAYX_FONT or a system CJK font)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: layx-reader [-font path] [book.epub]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	a := &app{
		fontSize:  reader.DefaultFontSize,
		status:    "打开一本 EPUB 开始阅读",
		startPath: flag.Arg(0),
	}
	if fontPath, err := reader.FindFont(*fontFlag); err != nil {
		a.status = err.Error()
		log.Println(err)
	} else if fonts, err := reader.OpenFont(fontPath); err != nil {
		a.status = err.Error()
		log.Println(err)
	} else {
		a.fonts = fonts
		a.paint = reader.NewUIPainter(nil, fonts)
		log.Printf("layx-reader: font %s", fontPath)
	}
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "layx-reader",
			Width:     900,
			Height:    700,
			MinWidth:  480,
			MinHeight: 360,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		if a.startPath != "" {
			go a.openAsync(a.startPath)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
