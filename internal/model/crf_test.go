package model

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestCRFDecodeSelectsBestPath(t *testing.T) {
	crf := CRF{
		Start:       []float64{0, 0},
		End:         []float64{0, 0},
		Transitions: [][]float64{{0, 0}, {0, 0}},
	}
	emissions := [][][]float64{
		{
			{3, 0},
			{0, 4},
			{1, 2},
		},
	}

	got := crf.Decode(emissions, nil)
	want := [][]int{{0, 1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestCRFDecodeHonorsMaskLength(t *testing.T) {
	crf := CRF{
		Start:       []float64{0, 0},
		End:         []float64{0, 0},
		Transitions: [][]float64{{0, 0}, {0, 0}},
	}
	emissions := [][][]float64{
		{
			{5, 0},
			{0, 9},
		},
	}
	mask := [][]bool{{true, false}}

	got := crf.Decode(emissions, mask)
	want := [][]int{{0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestCRFMarginalProbabilitiesEqualEmissionsAreUniform(t *testing.T) {
	crf := CRF{
		Start:       []float64{0, 0},
		End:         []float64{0, 0},
		Transitions: [][]float64{{0, 0}, {0, 0}},
	}
	emissions := [][][]float64{
		{
			{0, 0},
			{0, 0},
		},
	}
	mask := [][]bool{{true, true}}

	got := crf.MarginalProbabilities(emissions, mask)
	if len(got) != 2 || len(got[0]) != 1 || len(got[0][0]) != 2 {
		t.Fatalf("MarginalProbabilities() shape = [%d][...], want [2][1][2]", len(got))
	}
	for seq := range got {
		for batch := range got[seq] {
			for tag, p := range got[seq][batch] {
				if math.Abs(p-0.5) > 1e-9 {
					t.Fatalf("MarginalProbabilities()[%d][%d][%d] = %.17g, want 0.5", seq, batch, tag, p)
				}
			}
		}
	}
}

func TestCRFMarginalProbabilitiesImpossibleLatticeReturnsZeroes(t *testing.T) {
	crf := CRF{
		Start:       []float64{0, 0},
		End:         []float64{0, 0},
		Transitions: [][]float64{{0, 0}, {0, 0}},
	}
	emissions := [][][]float64{
		{
			{math.Inf(-1), math.Inf(-1)},
			{math.Inf(-1), math.Inf(-1)},
		},
	}

	got := crf.MarginalProbabilities(emissions, nil)
	for seq := range got {
		for batch := range got[seq] {
			for tag, p := range got[seq][batch] {
				if math.IsNaN(p) {
					t.Fatalf("MarginalProbabilities()[%d][%d][%d] is NaN, want 0", seq, batch, tag)
				}
				if p != 0 {
					t.Fatalf("MarginalProbabilities()[%d][%d][%d] = %.17g, want 0", seq, batch, tag, p)
				}
			}
		}
	}
}

func TestCRFRejectsMalformedShapes(t *testing.T) {
	tests := []struct {
		name      string
		emissions [][][]float64
		mask      [][]bool
		wantPanic string
	}{
		{
			name:      "ragged tag dimensions",
			emissions: [][][]float64{{{0, 0}, {0}}},
			wantPanic: "emissions[0][1] tag count 1 does not match tag count 2",
		},
		{
			name:      "mask batch length mismatch",
			emissions: [][][]float64{{{0, 0}}},
			mask:      [][]bool{},
			wantPanic: "mask batch size 0 does not match emissions batch size 1",
		},
		{
			name:      "mask row length mismatch",
			emissions: [][][]float64{{{0, 0}, {0, 0}}},
			mask:      [][]bool{{true}},
			wantPanic: "mask[0] length 1 does not match emissions[0] sequence length 2",
		},
		{
			name:      "zero tag count",
			emissions: [][][]float64{{{}}},
			wantPanic: "emissions[0][0] has zero tags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/Decode", func(t *testing.T) {
			crf := CRF{}
			requirePanicContains(t, tt.wantPanic, func() {
				_ = crf.Decode(tt.emissions, tt.mask)
			})
		})
		t.Run(tt.name+"/MarginalProbabilities", func(t *testing.T) {
			crf := CRF{}
			requirePanicContains(t, tt.wantPanic, func() {
				_ = crf.MarginalProbabilities(tt.emissions, tt.mask)
			})
		})
	}
}

func requirePanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("panic = nil, want message containing %q", want)
		}
		msg := fmt.Sprint(got)
		if !strings.Contains(msg, want) {
			t.Fatalf("panic = %q, want message containing %q", msg, want)
		}
	}()
	fn()
}
