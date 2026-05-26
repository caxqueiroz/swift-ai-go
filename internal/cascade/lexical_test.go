package cascade_test

import (
	"testing"

	"github.com/tipmarket/swift-ai/internal/cascade"
)

func TestLexicalIdentityScoresNearExactAddressHigh(t *testing.T) {
	got := cascade.LexicalIdentity(
		"77 RUE DE RIVOLI 75001 PARIS",
		"77, Rue de Rivoli - 75001 Paris",
	)

	if got < 0.85 {
		t.Fatalf("LexicalIdentity = %f, want >= 0.85", got)
	}
}

func TestLexicalIdentityScoresDifferentPlacesLow(t *testing.T) {
	got := cascade.LexicalIdentity(
		"SAN PO KONG HONG KONG",
		"SAN FERNANDO PHILIPPINES",
	)

	if got >= 0.85 {
		t.Fatalf("LexicalIdentity = %f, want < 0.85", got)
	}
}
