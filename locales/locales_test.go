package locales

import (
	"testing"

	"github.com/egoist/mygo/i18n"
)

// The catalogs say the app's messages in both of its languages, with the plural
// forms of each: English has two, Chinese one.
func TestCatalogsSayBothLanguages(t *testing.T) {
	en, zh := In("en"), In("zh-Hans")
	for _, tc := range []struct{ name, got, want string }{
		{"books", en.LibraryCount(3), "3 books"},
		{"books", zh.LibraryCount(3), "3 本书"},
		{"one book", en.LibraryCount(1), "1 book"},
		{"pages left", en.ChapterPagesLeft(12), "12 pages left in chapter"},
		{"pages left", zh.ChapterPagesLeft(12), "本章还有 12 页"},
		{"one page left", en.ChapterPagesLeft(1), "1 page left in chapter"},
		{"query", zh.SearchNotFound("阿Q"), "未找到「阿Q」"},
		{"title", zh.WindowTitle("边城"), "《边城》 — layx-reader"},
		{"title", en.WindowTitle("Border Town"), "Border Town — layx-reader"},
		{"one day", en.UnitDays(1), "1 day"},
		{"days", zh.UnitDays(30), "30 天"},
		// A language the catalogs do not carry falls back to the source.
		{"fallback", In("de").LibraryCount(2), "2 books"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// Importing the catalogs, which the app does by saying its messages with them,
// makes their languages the app's.
func TestAppLanguages(t *testing.T) {
	langs := i18n.Languages()
	for _, want := range []string{"en", "zh-Hans"} {
		found := false
		for _, got := range langs {
			found = found || got == want
		}
		if !found {
			t.Errorf("languages %v do not include %q", langs, want)
		}
	}
}

// The functions say their messages in the language the app shows, which MyGo
// makes the language of the users or the one chosen in the system's settings.
func TestMessagesFollowTheAppLanguage(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "en" }) })
	for _, tc := range []struct{ name, got, want string }{
		{"books", LibraryCount(3), "3 本书"},
		{"panel", PanelContents(), "目录"},
		{"menu", MenuOpen(), "打开…"},
		{"title", WindowTitle("边城"), "《边城》 — layx-reader"},
		{"layout", LayoutFailed("out of memory"), "排版失败：out of memory"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
