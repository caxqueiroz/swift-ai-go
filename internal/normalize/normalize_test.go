package normalize

import (
	"slices"
	"testing"
)

func TestDuplicateIfSaintInName(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{
			name: "SAINT-ETIENNE",
			want: []string{"SAINT-ETIENNE", "ST. ETIENNE", "ST-ETIENNE"},
		},
		{
			name: "ST. JOHN'S",
			want: []string{"SAINT-JOHN'S", "ST. JOHN'S", "ST-JOHN'S"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStringSet(t, DuplicateIfSaintInName(tt.name), tt.want)
		})
	}
}

func TestDuplicateIfSeparatorPresent(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{
			name: "Val-d'Oise",
			want: []string{"Val-d'Oise", "Val d'Oise"},
		},
		{
			name: "NoSeparator",
			want: []string{"NoSeparator"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStringSet(t, DuplicateIfSeparatorPresent(tt.name), tt.want)
		})
	}
}

func TestDecodeAndClean(t *testing.T) {
	input := "this needs to be replaced: `test @ Rock´n roll` and ‘–others–‘"
	want := "this needs to be replaced: 'test a Rock'n roll' and '-others-'"

	if got := DecodeAndClean(input); got != want {
		t.Fatalf("DecodeAndClean() = %q, want %q", got, want)
	}
}

func TestDecodeAndCleanAppliesReplacementAfterTransliteration(t *testing.T) {
	if got, want := DecodeAndClean("\u1800"), " a "; got != want {
		t.Fatalf("DecodeAndClean() = %q, want %q", got, want)
	}
}

func TestGenerateDuplicateAliasesComposesSaintThenSeparator(t *testing.T) {
	got := GenerateDuplicateAliases("MONT-SAINT-MICHEL")
	want := []string{
		"MONT SAINT MICHEL",
		"MONT ST MICHEL",
		"MONT ST. MICHEL",
		"MONT-SAINT-MICHEL",
		"MONT-ST-MICHEL",
		"MONT-ST. MICHEL",
		"MONT-ST.-MICHEL",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("GenerateDuplicateAliases() = %v, want %v", got, want)
	}
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d; got %v, want %v", len(got), len(want), got, want)
	}

	counts := make(map[string]int, len(want))
	for _, value := range want {
		counts[value]++
	}

	for _, value := range got {
		counts[value]--
	}

	for value, count := range counts {
		if count != 0 {
			t.Fatalf("set mismatch for %q: count delta %d; got %v, want %v", value, count, got, want)
		}
	}
}
