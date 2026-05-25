package postcode

import (
	"strings"
	"testing"
)

func TestFindTownMatchesReturnsDictionaryEntriesForPostcode(t *testing.T) {
	dict := map[string][]Entry{"75001": {{Town: "PARIS", Origin: "FR"}}}
	regexes := []string{`75001`}
	text := "1 RUE TEST, 75001 PARIS"

	matches, err := FindTownMatches(dict, regexes, text, "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	match := matches[0]
	if match.Possibility != "PARIS" {
		t.Errorf("expected possibility PARIS, got %q", match.Possibility)
	}
	if match.Origin != "FR" {
		t.Errorf("expected origin FR, got %q", match.Origin)
	}
	if match.Matched != "75001" {
		t.Errorf("expected matched text 75001, got %q", match.Matched)
	}
	if match.Start < 0 || match.End <= match.Start || match.End > len(text) {
		t.Errorf("expected valid start/end within text, got start=%d end=%d text length=%d", match.Start, match.End, len(text))
	}
}

func TestFindTownMatchesPreprocessesNonAlphanumericAsSpaces(t *testing.T) {
	dict := map[string][]Entry{"AB 123": {{Town: "TESTTOWN", Origin: "GB"}}}
	regexes := []string{`AB 123`}
	text := "AB-123 TESTTOWN"

	matches, err := FindTownMatches(dict, regexes, text, "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Matched != "AB 123" {
		t.Errorf("expected preprocessed matched text AB 123, got %q", matches[0].Matched)
	}
}

func TestFindTownMatchesDoesNotUppercaseBeforePreprocessing(t *testing.T) {
	dict := map[string][]Entry{"AB 123": {{Town: "TESTTOWN", Origin: "GB"}}}
	regexes := []string{`AB 123`}
	text := "ab-123 TESTTOWN"

	matches, err := FindTownMatches(dict, regexes, text, "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected lowercase letters to be replaced before matching, got %d matches", len(matches))
	}

	matches, err = FindTownMatches(dict, regexes, strings.ToUpper(text), "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error for caller-uppercased text: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected caller-uppercased text to match, got %d matches", len(matches))
	}
}

func TestFindTownMatchesPreservesOriginalByteOffsets(t *testing.T) {
	dict := map[string][]Entry{"75001": {{Town: "PARIS", Origin: "FR"}}}
	regexes := []string{`75001`}
	text := "CAFÉ 75001 PARIS"

	matches, err := FindTownMatches(dict, regexes, text, "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	match := matches[0]
	if got := text[match.Start:match.End]; got != "75001" {
		t.Fatalf("expected original slice 75001, got %q with start=%d end=%d", got, match.Start, match.End)
	}
}

func TestFindTownMatchesAppendsSuffixStructure(t *testing.T) {
	dict := map[string][]Entry{"ABC": {{Town: "SUFFIXTOWN", Origin: "AR"}}}
	regexes := []string{`ABC`}
	text := "ABC12 SUFFIXTOWN"

	matches, err := FindTownMatches(dict, regexes, text, `[0-9]{2}`)
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Matched != "ABC12" {
		t.Errorf("expected matched text ABC12, got %q", matches[0].Matched)
	}
	if matches[0].Possibility != "SUFFIXTOWN" {
		t.Errorf("expected possibility SUFFIXTOWN, got %q", matches[0].Possibility)
	}
}

func TestFindTownMatchesSkipsMatchesWithoutDictionaryEntry(t *testing.T) {
	dict := map[string][]Entry{}
	regexes := []string{`75001`}
	text := "1 RUE TEST, 75001 PARIS"

	matches, err := FindTownMatches(dict, regexes, text, "")
	if err != nil {
		t.Fatalf("FindTownMatches returned error: %v", err)
	}

	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(matches))
	}
}
