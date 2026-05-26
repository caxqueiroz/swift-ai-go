package cache_test

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/cache"
)

func TestVectorLiteralFormatsPgvectorInput(t *testing.T) {
	got := cache.VectorLiteral([]float64{0.125, -2, 3.5})
	want := "[0.125,-2,3.5]"
	if got != want {
		t.Fatalf("VectorLiteral = %q, want %q", got, want)
	}
}
