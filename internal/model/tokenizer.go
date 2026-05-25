package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	unknownIndex = 0
	padIndex     = 1

	unknownToken = "\ue003"
	padToken     = "\ue004"
)

type CharacterTokenizer struct {
	tokenToIndex map[rune]int
	indexToToken []string
}

func NewCharacterTokenizer(vocabulary []string) (*CharacterTokenizer, error) {
	indexToToken := make([]string, 0, len(vocabulary)+2)
	indexToToken = append(indexToToken, unknownToken, padToken)

	tokenToIndex := make(map[rune]int, len(vocabulary))
	for i, token := range vocabulary {
		if utf8.RuneCountInString(token) != 1 {
			return nil, fmt.Errorf("vocabulary entry %q at index %d must be one character", token, i)
		}

		r, _ := utf8.DecodeRuneInString(token)
		idx := i + 2
		tokenToIndex[r] = idx
		indexToToken = append(indexToToken, token)
	}

	return &CharacterTokenizer{
		tokenToIndex: tokenToIndex,
		indexToToken: indexToToken,
	}, nil
}

func (t *CharacterTokenizer) Encode(s string) []int {
	indices := make([]int, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		idx, ok := t.tokenToIndex[r]
		if !ok {
			idx = unknownIndex
		}
		indices = append(indices, idx)
	}
	return indices
}

func (t *CharacterTokenizer) Decode(indices []int) string {
	var b strings.Builder
	for _, idx := range indices {
		switch idx {
		case unknownIndex:
			b.WriteString("<UNKNOWN>")
		case padIndex:
			b.WriteString("<PAD>")
		default:
			if idx >= 0 && idx < len(t.indexToToken) {
				b.WriteString(t.indexToToken[idx])
			} else {
				b.WriteString("<UNKNOWN>")
			}
		}
	}
	return b.String()
}

func (t *CharacterTokenizer) VocabSize() int {
	return len(t.indexToToken)
}

func (t *CharacterTokenizer) PadIndex() int {
	return padIndex
}
