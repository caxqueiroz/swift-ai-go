package model

import (
	"reflect"
	"testing"
)

func TestCharacterTokenizerIndexesReserveUnknownAndPad(t *testing.T) {
	tok, err := NewCharacterTokenizer([]string{"A", "B", " "})
	if err != nil {
		t.Fatalf("NewCharacterTokenizer() error = %v", err)
	}

	if got := tok.PadIndex(); got != 1 {
		t.Fatalf("PadIndex() = %d, want 1", got)
	}
	if got := tok.VocabSize(); got != 5 {
		t.Fatalf("VocabSize() = %d, want 5", got)
	}

	encoded := tok.Encode("AZ B")
	want := []int{2, 0, 4, 3}
	if !reflect.DeepEqual(encoded, want) {
		t.Fatalf("Encode(%q) = %#v, want %#v", "AZ B", encoded, want)
	}
}

func TestCharacterTokenizerDecodeReplacesReservedTokens(t *testing.T) {
	tok, err := NewCharacterTokenizer([]string{"A", "B", " "})
	if err != nil {
		t.Fatalf("NewCharacterTokenizer() error = %v", err)
	}

	got := tok.Decode([]int{2, 0, 4, 3, tok.PadIndex()})
	want := "A<UNKNOWN> B<PAD>"
	if got != want {
		t.Fatalf("Decode() = %q, want %q", got, want)
	}
}

func TestCharacterTokenizerRejectsMultiRuneVocabularyEntries(t *testing.T) {
	if _, err := NewCharacterTokenizer([]string{"AB"}); err == nil {
		t.Fatal("NewCharacterTokenizer() error = nil, want error")
	}
}
