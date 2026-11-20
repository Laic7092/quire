package reader

import (
	"os"
	"testing"
)

// 没有可读的系统 CJK 字体时（手机就是这样）用内置的那一份，而不是打不开书。
func TestBundledFontFallback(t *testing.T) {
	saved := DefaultFonts
	DefaultFonts = nil
	defer func() { DefaultFonts = saved }()
	t.Setenv("LAYX_FONT", "")

	path, err := FindFont("")
	if err != nil {
		t.Fatalf("FindFont: %v", err)
	}
	if path != BundledFont {
		t.Fatalf("FindFont = %q, want %q", path, BundledFont)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%q 应该是内置字体的标记，不是文件", path)
	}

	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatalf("OpenFont(%q): %v", path, err)
	}
	if fonts == nil || fonts.Set == nil {
		t.Fatal("no font set")
	}
	if fonts.Path != BundledFont {
		t.Fatalf("Path = %q", fonts.Path)
	}
}
