package cascade

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agnivade/levenshtein"

	"github.com/tipmarket/swift-ai/internal/normalize"
)

func NormalizeAddress(text string) string {
	text = strings.ReplaceAll(text, `\n`, "\n")
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ToUpper(normalize.DecodeAndClean(text))

	var b strings.Builder
	lastSpace := true
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func LexicalIdentity(a string, b string) float64 {
	left := NormalizeAddress(a)
	right := NormalizeAddress(b)
	if left == "" && right == "" {
		return 1
	}
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}

	maxLen := max(utf8.RuneCountInString(left), utf8.RuneCountInString(right))
	levenshteinScore := 1 - float64(levenshtein.ComputeDistance(left, right))/float64(maxLen)
	trigramScore := trigramJaccard(left, right)
	return 0.6*levenshteinScore + 0.4*trigramScore
}

func trigramJaccard(a string, b string) float64 {
	left := trigrams(a)
	right := trigrams(b)
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	if len(left) == 0 || len(right) == 0 {
		return 0
	}

	var intersection int
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func trigrams(s string) map[string]bool {
	runes := []rune("  " + s + "  ")
	out := make(map[string]bool)
	if len(runes) < 3 {
		out[string(runes)] = true
		return out
	}
	for i := 0; i <= len(runes)-3; i++ {
		out[string(runes[i:i+3])] = true
	}
	return out
}
