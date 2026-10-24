// Package icons holds the app's SVG icons, parsed once at startup.
//
// MyGo draws an SVG into its atlas once for each size it shows at, so sharing
// the parsed icon across the toolbar, the sidebar and the shelf costs nothing
// per frame. Icons take the colour of the text around them.
package icons

import "github.com/egoist/mygo/ui"

// TriangleUp is the pointer of a popover: a filled triangle in a 16×8 box.
var TriangleUp = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 8"><path d="M0 8 8 0l8 8z" fill="currentColor"/></svg>`))

// stroke wraps path markup in the 24×24 stroke outline every icon uses.
func stroke(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	// Book is a closed book, for the shelf and the window's home screen.
	Book = stroke(`<path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H19a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H6.5a1 1 0 0 1 0-5H20"/>`)
	// BookOpen is an open book, for the reader.
	BookOpen = stroke(`<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>`)
	// ListTree is the table of contents.
	ListTree = stroke(`<path d="M21 12h-8"/><path d="M21 6H8"/><path d="M21 18h-8"/><path d="M3 6v4c0 1.1.9 2 2 2h3"/><path d="M3 10v6c0 1.1.9 2 2 2h3"/>`)
	// Search is the magnifying glass of the search panel; the field draws its
	// own.
	Search = stroke(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)
	// PanelLeft toggles the sidebar.
	PanelLeft = stroke(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	// Folder opens a directory.
	Folder = stroke(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	// FilePlus opens one book.
	FilePlus = stroke(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M9 15h6"/><path d="M12 18v-6"/>`)
	// Refresh rescans the shelf.
	Refresh = stroke(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)
	// ChevronLeft turns back a page.
	ChevronLeft = stroke(`<path d="m15 18-6-6 6-6"/>`)
	// ChevronRight turns on a page.
	ChevronRight = stroke(`<path d="m9 18 6-6-6-6"/>`)
	// ChevronsLeft goes to the start of the book.
	ChevronsLeft = stroke(`<path d="m11 17-5-5 5-5"/><path d="m18 17-5-5 5-5"/>`)
	// Minus makes the text smaller.
	Minus = stroke(`<path d="M5 12h14"/>`)
	// Plus makes the text larger.
	Plus = stroke(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	// Close dismisses a panel or the cover.
	Close = stroke(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	// Type is the reading size, a letter A on a baseline.
	Type = stroke(`<path d="M4 7V4h16v3"/><path d="M9 20h6"/><path d="M12 4v16"/>`)
	// House is the library's home, the first row of its sidebar.
	House = stroke(`<path d="M3 10.2 12 3l9 7.2"/><path d="M5 9.5V21h14V9.5"/><path d="M10 21v-6h4v6"/>`)
	// Grid is "all books", a wall of covers.
	Grid = stroke(`<rect width="7" height="7" x="3" y="3" rx="1.5"/><rect width="7" height="7" x="14" y="3" rx="1.5"/><rect width="7" height="7" x="3" y="14" rx="1.5"/><rect width="7" height="7" x="14" y="14" rx="1.5"/>`)
	// Clock is "recently added", the books read last.
	Clock = stroke(`<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`)
	// Check is "finished", a book read to its last page.
	Check = stroke(`<circle cx="12" cy="12" r="9"/><path d="m8.5 12.2 2.6 2.6 4.6-5"/>`)
	// Half is "reading now", a book part way through.
	Half = stroke(`<circle cx="12" cy="12" r="9"/><path d="M12 3v18"/><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor" stroke="none"/>`)
	// Gear opens the settings the theme cards sit above.
	Gear = stroke(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>`)
	// Person is the account at the foot of the sidebar.
	Person = stroke(`<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>`)
	// Sort is the shelf's order, the small control the library floats at its
	// foot.
	Sort = stroke(`<path d="M4 6h16"/><path d="M7 12h10"/><path d="M10 18h4"/>`)
	// Share sends the book out of the app.
	Share = stroke(`<path d="M12 16V4"/><path d="m8 8 4-4 4 4"/><path d="M5 14v5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-5"/>`)
	// ListBullet is the contents: a bulleted list, as Books draws its table
	// of contents at the head of the reading bar.
	ListBullet = stroke(`<circle cx="4.5" cy="6" r="1.2" fill="currentColor" stroke="none"/><circle cx="4.5" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="4.5" cy="18" r="1.2" fill="currentColor" stroke="none"/><path d="M8.5 6h11"/><path d="M8.5 12h11"/><path d="M8.5 18h11"/>`)
	// BookMarkSquare is a book with a ribbon: the bookmarks panel at the head of
	// the reading bar, as Books draws a bookmark hanging from the card's edge.
	BookMarkSquare = stroke(`<rect x="3" y="4" width="18" height="17" rx="4"/><path d="M14 4v6.6l2.5-1.7 2.5 1.7V4z" fill="currentColor" stroke="none"/>`)
	// ListSquare is a page of lines: the sidebar's outline of what is open.
	ListSquare = stroke(`<rect x="3" y="4" width="18" height="17" rx="4"/><path d="M7 9h10"/><path d="M7 13h10"/><path d="M7 17h6"/>`)
	// NoteCard is the notes: a card with a filled head above the lines the
	// reader wrote, as Books draws the notes panel at the head of the bar.
	NoteCard = stroke(`<rect x="3" y="4" width="18" height="17" rx="4"/><path d="M3 8V7.9A3.9 3.9 0 0 1 6.9 4h10.2A3.9 3.9 0 0 1 21 7.9V8z" fill="currentColor" stroke="none"/><path d="M7.5 12h9"/><path d="M7.5 15.5h9"/><path d="M7.5 19h5"/>`)
	// Bookmark is the ribbon that marks a page.
	Bookmark = stroke(`<path d="M6 4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v17l-6-4-6 4z"/>`)
	// SunHorizon is the appearance control in the display settings.
	SunHorizon = stroke(`<path d="M3 18h18"/><path d="M7 18a5 5 0 0 1 10 0"/><path d="M12 5v2"/><path d="m6.3 8.3 1.4 1.4"/><path d="m17.7 8.3-1.4 1.4"/>`)
	// Sliders tunes a card's own settings, as the reading goals card's
	// controls icon does.
	Sliders = stroke(`<path d="M4 7h16"/><path d="M4 12h16"/><path d="M4 17h16"/><circle cx="9" cy="7" r="2.2"/><circle cx="15" cy="12" r="2.2"/><circle cx="8" cy="17" r="2.2"/>`)
	// Trash removes a bookmark or a note from its row.
	Trash = stroke(`<path d="M3 6h18"/><path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>`)
)
