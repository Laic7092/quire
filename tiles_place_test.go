package main

import (
	"math"
	"testing"
)

// A tile stands on the device grid while the column rests, and is drawn at the
// place the column has reached while it moves: holding a moving column to the
// grid steps it two pixels on one frame and one on the next, which at the speed
// of a slow drag is the stutter a reader sees (see drawScroll).
func TestTilesStandOnTheGridOnlyWhenTheColumnDoes(t *testing.T) {
	const scale = 3 // a phone
	x, y := tilePlace(10, 100.4, scale, true)
	if math.Abs(float64(y)-100.333) > 0.001 || math.Abs(float64(x)-10) > 0.001 {
		t.Fatalf("a standing tile went to (%.3f, %.3f), want it on the grid (10, 100.333)", x, y)
	}
	x, y = tilePlace(10, 100.4, scale, false)
	if x != 10 || y != 100.4 {
		t.Fatalf("a moving tile went to (%.3f, %.3f), want the place it reached (10, 100.4)", x, y)
	}
}
