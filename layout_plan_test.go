package main

import (
	"testing"

	"github.com/laix/quire/internal/reader"
)

func TestChooseLayoutGate(t *testing.T) {
	opts := reader.DefaultOptions()
	paper := paperOf(reader.Quiet)
	base := layoutGateInput{
		width: 900, height: 600, sheetW: 800, sheetH: 560,
		measured: true, bookReady: true, fontsReady: true,
		opts: opts, layOpts: opts, paper: paper, layPaper: paper,
		docAt: opts.FontSize, hasLayout: true, layW: 800, layH: 560,
	}
	tests := []struct {
		name string
		edit func(*layoutGateInput)
		want layoutGate
	}{
		{"the same published layout", func(*layoutGateInput) {}, layoutGateCurrent},
		{"too narrow wins over missing state", func(in *layoutGateInput) {
			in.sheetW, in.bookReady = 2*opts.Margin, false
		}, layoutGateTooNarrow},
		{"wait for the reading area", func(in *layoutGateInput) { in.measured = false }, layoutGateWaitMeasure},
		{"book not ready", func(in *layoutGateInput) { in.bookReady = false }, layoutGateNotReady},
		{"fonts not ready", func(in *layoutGateInput) { in.fontsReady = false }, layoutGateNotReady},
		{"stable failure", func(in *layoutGateInput) {
			in.failed, in.failW, in.failH, in.failFontSize = true, in.width, in.height, opts.FontSize
		}, layoutGateStableFailure},
		{"failure for another request is retried", func(in *layoutGateInput) {
			in.failed, in.failW, in.failH, in.failFontSize = true, in.width, in.height, opts.FontSize+1
			in.docAt = opts.FontSize + 1 // the old layout cannot satisfy the new request
		}, layoutGateWork},
		{"matching job in flight", func(in *layoutGateInput) { in.loading = true }, layoutGateLoading},
		{"job in flight for another sheet", func(in *layoutGateInput) {
			in.loading, in.layW = true, in.layW-1
		}, layoutGateWork},
		{"sheet changed", func(in *layoutGateInput) { in.sheetW-- }, layoutGateWork},
		{"paper changed", func(in *layoutGateInput) { in.paper = paperOf(reader.Bold) }, layoutGateWork},
		{"options changed", func(in *layoutGateInput) { in.opts.FontSize++ }, layoutGateWork},
		{"document size changed", func(in *layoutGateInput) { in.docAt++ }, layoutGateWork},
		{"no published layout", func(in *layoutGateInput) { in.hasLayout = false }, layoutGateWork},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.edit(&in)
			if got := chooseLayoutGate(in); got != tc.want {
				t.Fatalf("chooseLayoutGate() = %v, want %v", got, tc.want)
			}
		})
	}
}
