package model

import (
	"reflect"
	"testing"

	"github.com/tipmarket/swift-ai/internal/core"
)

func TestCreateDetailsFromBIOTagsGroupsNonStrictSpans(t *testing.T) {
	tags := []core.BIOTag{
		{BIO: core.BioBefore, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
		{BIO: core.BioOther, Tag: core.TagOther},
		{BIO: core.BioBefore, Tag: core.TagTown},
	}

	details := CreateDetailsFromBIOTags("ABCD", "DE", 0.91, tags, false)

	if details.Content != "ABCD" {
		t.Fatalf("Content = %q, want %q", details.Content, "ABCD")
	}
	if details.CountryCode != "DE" {
		t.Fatalf("CountryCode = %q, want %q", details.CountryCode, "DE")
	}
	if details.CountryCodeConfidence != 0.91 {
		t.Fatalf("CountryCodeConfidence = %v, want 0.91", details.CountryCodeConfidence)
	}

	want := []core.TaggedSpan{
		{Start: 0, End: 2, Tag: core.TagCountry},
		{Start: 2, End: 3, Tag: core.TagOther},
		{Start: 3, End: 4, Tag: core.TagTown},
	}
	if !reflect.DeepEqual(details.Spans, want) {
		t.Fatalf("Spans = %#v, want %#v", details.Spans, want)
	}
}

func TestCreateDetailsFromBIOTagsReturnsEmptySpansForEmptyTags(t *testing.T) {
	details := CreateDetailsFromBIOTags("ABCD", "DE", 0.91, nil, false)

	if details.Spans == nil {
		t.Fatal("Spans = nil, want empty slice")
	}
	if len(details.Spans) != 0 {
		t.Fatalf("len(Spans) = %d, want 0", len(details.Spans))
	}
}

func TestCreateDetailsFromBIOTagsStrictTreatsLeadingInsideAsOther(t *testing.T) {
	tags := []core.BIOTag{
		{BIO: core.BioInside, Tag: core.TagCountry},
		{BIO: core.BioBefore, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
	}

	details := CreateDetailsFromBIOTags("ABC", "", 0, tags, true)

	want := []core.TaggedSpan{
		{Start: 0, End: 1, Tag: core.TagOther},
		{Start: 1, End: 3, Tag: core.TagCountry},
	}
	if !reflect.DeepEqual(details.Spans, want) {
		t.Fatalf("Spans = %#v, want %#v", details.Spans, want)
	}
}

func TestCreateDetailsFromBIOTagsStrictAcceptsBeforeInsideSequence(t *testing.T) {
	tags := []core.BIOTag{
		{BIO: core.BioBefore, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
		{BIO: core.BioInside, Tag: core.TagCountry},
	}

	details := CreateDetailsFromBIOTags("ABC", "", 0, tags, true)

	want := []core.TaggedSpan{
		{Start: 0, End: 3, Tag: core.TagCountry},
	}
	if !reflect.DeepEqual(details.Spans, want) {
		t.Fatalf("Spans = %#v, want %#v", details.Spans, want)
	}
}
