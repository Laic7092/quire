package main

import "github.com/laic7092/layx/render"

// navPlace is one stop on the reader's trail: a place in the book, or the
// cover in front of it. A scrolling place also remembers the exact content
// offset it was taken at, which a location alone cannot: the line at the top of
// the column can start below it when the column rests in the space above the
// book's first line. The offset is only used while the layout it was taken in
// is still the one on screen; after a rebuild the location names the place.
type navPlace struct {
	loc    render.Location
	cover  bool
	gen    int
	scroll float32
	col    bool // a place in a scrolling column rather than a page
}

// navHistory is the trail of places the reader jumped to in the open book — a
// table of contents row, a search hit, a bookmark, a note, an in-book link or a
// footnote — so the folio's two chevrons can take a jump back and step through
// it again. Page turns and scrolling are not stops: they are moving on from
// where a jump left the reader, and the stop they stand at follows them (see
// stand).
type navHistory struct {
	// places are the stops, oldest first; index is the one the reader stands
	// at, and the zero history is empty.
	places []navPlace
	index  int
}

// navLimit is how many stops the trail keeps. A long session of following
// footnotes should not grow the window without end, and a hundred jumps is far
// past the point where a reader steps back through them one by one.
const navLimit = 100

// reset empties the trail, as opening another book does.
func (h *navHistory) reset() {
	h.places = nil
	h.index = 0
}

// canBack reports whether there is a stop before the one the reader stands at.
func (h *navHistory) canBack() bool { return len(h.places) > 1 && h.index > 0 }

// canForward reports whether the reader stepped back from a stop that is still
// there to return to.
func (h *navHistory) canForward() bool {
	return len(h.places) > 0 && h.index >= 0 && h.index+1 < len(h.places)
}

// jump records a jump from one place to another. The live place replaces the
// stop the reader stands at — page turns since the last jump are where a step
// back should return — and whatever lay forward of it is dropped, as a new page
// drops a browser's forward history.
func (h *navHistory) jump(from, to navPlace) {
	if samePlace(from, to) {
		return
	}
	if len(h.places) == 0 {
		h.places = append(h.places, from)
		h.index = 0
	} else {
		h.index = min(max(h.index, 0), len(h.places)-1)
		h.places[h.index] = from
		h.places = h.places[:h.index+1]
	}
	h.places = append(h.places, to)
	h.index = len(h.places) - 1
	if extra := len(h.places) - navLimit; extra > 0 {
		h.places = append([]navPlace(nil), h.places[extra:]...)
		h.index -= extra
	}
}

// samePlace reports whether a jump between the two would move the reader at
// all. A scrolling place is the offset it stands at, not the line the offset
// rounds to, so a jump that lands on the same offset is no jump.
func samePlace(a, b navPlace) bool {
	if a.cover || b.cover {
		return a.cover == b.cover && a.loc == b.loc
	}
	if a.col && b.col && a.gen == b.gen {
		return a.scroll == b.scroll
	}
	return a.loc == b.loc
}

// stand writes the live place into the stop the reader stands at, so stepping
// away from it returns where they actually left rather than where the jump
// landed.
func (h *navHistory) stand(p navPlace) {
	if h.index >= 0 && h.index < len(h.places) {
		h.places[h.index] = p
	}
}

// back steps to the stop before the current one, given the live place.
func (h *navHistory) back(live navPlace) (navPlace, bool) {
	if !h.canBack() {
		return navPlace{}, false
	}
	h.stand(live)
	h.index--
	return h.places[h.index], true
}

// forward steps to the stop the reader stepped away from.
func (h *navHistory) forward(live navPlace) (navPlace, bool) {
	if !h.canForward() {
		return navPlace{}, false
	}
	h.stand(live)
	h.index++
	return h.places[h.index], true
}

// currentPlace is where the reader stands now, in the form the trail keeps. It
// fails while there is no layout to name a place in.
func (a *app) currentPlace() (navPlace, bool) {
	if a.showCover {
		return navPlace{cover: true}, true
	}
	loc, ok := a.currentLoc()
	if !ok {
		return navPlace{}, false
	}
	p := navPlace{loc: loc, gen: a.layGen}
	if a.scrolling() {
		p.col = true
		p.scroll = a.scrollY
	}
	return p, true
}

// navBack takes the last jump back: a footnote returns to the passage that
// linked to it, a table of contents row to the page it was followed from.
func (a *app) navBack() {
	live, ok := a.currentPlace()
	if !ok {
		return
	}
	to, ok := a.nav.back(live)
	if !ok {
		return
	}
	a.goPlace(to)
}

// navForward steps forward again through the jumps taken back.
func (a *app) navForward() {
	live, ok := a.currentPlace()
	if !ok {
		return
	}
	to, ok := a.nav.forward(live)
	if !ok {
		return
	}
	a.goPlace(to)
}

// goPlace restores a stop of the trail: the cover in front of the book, or the
// place in it. It does not touch the trail, which is what stepping through it
// means. A scrolling stop put back into the layout it was taken from lands on
// the offset itself; any other returns by location, so a rebuilt or repaginated
// book still finds the same line.
func (a *app) goPlace(p navPlace) {
	if p.cover {
		a.showCover = true
		a.coverIntro = nil
		a.page = 0
		a.turning = nil
		a.clearSelection()
		a.afterPage()
		return
	}
	if a.scrolling() && p.col && p.gen == a.layGen {
		a.dismissCover()
		a.scrollTo(p.scroll)
		return
	}
	a.moveTo(p.loc)
}
