package main

import (
	"image/png"
	"os"
	"testing"
)

func TestZZScales(t *testing.T) {
	for _, s := range []float32{1.5, 2, 2.625, 2.75, 3} {
		a, tt := newScrollWindow(t)
		a.instant = true
		tt.SetScale(s)
		tt.Frame()
		a.scrollTo(a.tileH*3 - 150)
		tt.Frame()
		dir := "/tmp/sc"
		_ = os.MkdirAll(dir, 0o755)
		tag := func(v float32) string { return string([]byte{byte('0' + int(v)), byte('0' + int(v*100)%100/10), byte('0' + int(v*100)%10)}) }
		save := func(p string) {
			f, _ := os.Create(p)
			_ = png.Encode(f, tt.Image())
			f.Close()
		}
		tileBitmaps = false
		tt.Frame()
		save(dir + "/old_" + tag(s) + ".png")
		tileBitmaps = true
		tt.Frame()
		save(dir + "/new_" + tag(s) + ".png")
	}
}
