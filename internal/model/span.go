package model

import "github.com/tipmarket/swift-ai/internal/core"

func CreateDetailsFromBIOTags(raw string, country string, confidence float64, tags []core.BIOTag, strict bool) core.Details {
	details := core.Details{
		Content:               raw,
		CountryCode:           country,
		CountryCodeConfidence: confidence,
		Spans:                 []core.TaggedSpan{},
	}
	if len(tags) == 0 {
		return details
	}
	if strict {
		details.Spans = createStrictSpans(tags)
	} else {
		details.Spans = createNonStrictSpans(tags)
	}
	return details
}

func createNonStrictSpans(tags []core.BIOTag) []core.TaggedSpan {
	spans := make([]core.TaggedSpan, 0, len(tags))
	start := 0
	current := tagForSpan(tags[0])

	for i := 1; i < len(tags); i++ {
		next := tagForSpan(tags[i])
		if next == current {
			continue
		}
		spans = append(spans, core.TaggedSpan{Start: start, End: i, Tag: current})
		start = i
		current = next
	}

	spans = append(spans, core.TaggedSpan{Start: start, End: len(tags), Tag: current})
	return spans
}

func createStrictSpans(tags []core.BIOTag) []core.TaggedSpan {
	spans := make([]core.TaggedSpan, 0, len(tags))
	for i := 0; i < len(tags); {
		tag := tags[i]
		if tag.BIO == core.BioOther || tag.Tag == core.TagOther {
			start := i
			for i < len(tags) && (tags[i].BIO == core.BioOther || tags[i].Tag == core.TagOther) {
				i++
			}
			spans = append(spans, core.TaggedSpan{Start: start, End: i, Tag: core.TagOther})
			continue
		}

		if tag.BIO != core.BioBefore {
			start := i
			for i < len(tags) && tags[i].BIO != core.BioBefore && tags[i].BIO != core.BioOther {
				i++
			}
			spans = append(spans, core.TaggedSpan{Start: start, End: i, Tag: core.TagOther})
			continue
		}

		start := i
		entity := tag.Tag
		i++
		for i < len(tags) && tags[i].BIO == core.BioInside && tags[i].Tag == entity {
			i++
		}
		spans = append(spans, core.TaggedSpan{Start: start, End: i, Tag: entity})
	}
	return spans
}

func tagForSpan(tag core.BIOTag) core.Tag {
	if tag.BIO == core.BioOther {
		return core.TagOther
	}
	return tag.Tag
}
