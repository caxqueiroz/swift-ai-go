package fuzzy

import (
	"sort"
	"strings"

	"github.com/tipmarket/swift-ai/internal/core"
)

type candidate struct {
	possibility string
	origins     []string
}

type nearMatch struct {
	start   int
	end     int
	matched string
	dist    int
}

// ScanAllBatched scans each query for fuzzy matches against words.
func ScanAllBatched(queries []string, words map[string][]string, scoreCutoff int, maxDistance int) [][]core.FuzzyMatch {
	candidates := sortedCandidates(words)
	results := make([][]core.FuzzyMatch, 0, len(queries))

	for _, query := range queries {
		matches := make([]core.FuzzyMatch, 0)

		for _, cand := range candidates {
			matchDistance := maxDistance
			if len(cand.possibility) <= 2 {
				matchDistance = 0
			}

			if partialRatio(query, cand.possibility, matchDistance) < scoreCutoff {
				continue
			}

			for _, match := range findNearMatches(query, cand.possibility, matchDistance) {
				flags := flagsForMatch(query, match)
				if len(cand.possibility) <= 2 && hasFlag(flags, core.FlagIsInsideAnotherWord) {
					continue
				}
				dist := adjustDistanceForMiddleNewlines(match.matched, match.dist)

				for _, origin := range cand.origins {
					matches = append(matches, core.FuzzyMatch{
						Start:       match.start,
						End:         match.end,
						Matched:     match.matched,
						Possibility: cand.possibility,
						Dist:        dist,
						Flags:       flags,
						Origin:      origin,
					})
				}
			}
		}

		results = append(results, matches)
	}

	return results
}

func sortedCandidates(words map[string][]string) []candidate {
	originSets := make(map[string]map[string]struct{}, len(words))
	for key, origins := range words {
		possibility := strings.ToUpper(key)
		if possibility == "" {
			continue
		}

		if originSets[possibility] == nil {
			originSets[possibility] = make(map[string]struct{}, len(origins))
		}
		for _, origin := range origins {
			originSets[possibility][origin] = struct{}{}
		}
	}

	possibilities := make([]string, 0, len(originSets))
	for possibility := range originSets {
		possibilities = append(possibilities, possibility)
	}
	sort.Strings(possibilities)

	candidates := make([]candidate, 0, len(possibilities))
	for _, possibility := range possibilities {
		origins := make([]string, 0, len(originSets[possibility]))
		for origin := range originSets[possibility] {
			origins = append(origins, origin)
		}
		sort.Strings(origins)

		candidates = append(candidates, candidate{
			possibility: possibility,
			origins:     origins,
		})
	}

	return candidates
}

func partialRatio(query, possibility string, maxDistance int) int {
	query = strings.ToUpper(query)

	if query == "" || possibility == "" {
		return 0
	}

	if strings.Contains(query, possibility) {
		return 100
	}

	best := 0
	minLength := max(1, len(possibility)-maxDistance)
	maxLength := min(len(query), len(possibility)+maxDistance)
	for length := minLength; length <= maxLength; length++ {
		for start := 0; start+length <= len(query); start++ {
			dist := levenshteinDistance(possibility, query[start:start+length])
			score := ratioScore(possibility, query[start:start+length], dist)
			if score > best {
				best = score
			}
		}
	}

	return best
}

func ratioScore(possibility string, match string, dist int) int {
	length := max(len(possibility), len(match))
	if length == 0 {
		return 100
	}

	score := 100 * (length - dist) / length
	if score < 0 {
		return 0
	}

	return score
}

func findNearMatches(query, possibility string, maxDistance int) []nearMatch {
	queryForDistance := strings.ToUpper(query)
	candidates := make([]nearMatch, 0)

	minLength := len(possibility) - maxDistance
	if minLength < 1 {
		minLength = 1
	}
	maxLength := len(possibility) + maxDistance
	if maxLength > len(query) {
		maxLength = len(query)
	}

	for start := 0; start < len(query); start++ {
		for length := minLength; length <= maxLength; length++ {
			end := start + length
			if end > len(query) {
				continue
			}

			dist := levenshteinDistance(possibility, queryForDistance[start:end])
			if dist > maxDistance {
				continue
			}

			candidates = append(candidates, nearMatch{
				start:   start,
				end:     end,
				matched: query[start:end],
				dist:    dist,
			})
		}
	}

	return bestNonOverlappingMatches(candidates)
}

func bestNonOverlappingMatches(candidates []nearMatch) []nearMatch {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist != candidates[j].dist {
			return candidates[i].dist < candidates[j].dist
		}
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		return candidates[i].end < candidates[j].end
	})

	matches := make([]nearMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if overlapsAny(candidate, matches) {
			continue
		}
		matches = append(matches, candidate)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		if matches[i].end != matches[j].end {
			return matches[i].end < matches[j].end
		}
		return matches[i].dist < matches[j].dist
	})

	return matches
}

func overlapsAny(candidate nearMatch, matches []nearMatch) bool {
	for _, match := range matches {
		if candidate.start < match.end && match.start < candidate.end {
			return true
		}
	}

	return false
}

func flagsForMatch(query string, match nearMatch) []core.Flag {
	if isStandaloneMatch(query, match) {
		return nil
	}

	return []core.Flag{core.FlagIsInsideAnotherWord}
}

func isStandaloneMatch(query string, match nearMatch) bool {
	if match.matched == "" {
		return false
	}

	end := match.end
	if strings.HasSuffix(match.matched, ".") {
		end--
	}
	if end <= match.start {
		return false
	}

	return hasWordBoundaryBefore(query, match.start) && hasWordBoundaryAfter(query, end)
}

func hasWordBoundaryBefore(query string, start int) bool {
	return start == 0 || !isWordByte(query[start-1])
}

func hasWordBoundaryAfter(query string, end int) bool {
	return end == len(query) || !isWordByte(query[end])
}

func isWordByte(b byte) bool {
	return b == '_' ||
		('0' <= b && b <= '9') ||
		('A' <= b && b <= 'Z') ||
		('a' <= b && b <= 'z')
}

func levenshteinDistance(a string, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}

	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}

	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			current[j] = min(
				previous[j]+1,
				current[j-1]+1,
				previous[j-1]+cost,
			)
		}
		previous, current = current, previous
	}

	return previous[len(b)]
}

func hasFlag(flags []core.Flag, flag core.Flag) bool {
	for _, existing := range flags {
		if existing == flag {
			return true
		}
	}

	return false
}

func adjustDistanceForMiddleNewlines(matched string, dist int) int {
	if len(matched) <= 2 {
		return dist
	}

	adjusted := dist - strings.Count(matched[1:len(matched)-1], "\n")
	if adjusted < 0 {
		return 0
	}

	return adjusted
}
