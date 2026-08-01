package acquisition

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

type variantRule struct {
	variant Variant
	phrases []string
	strict  bool
}

var variantRules = []variantRule{
	{variant: VariantNightcore, phrases: []string{"nightcore"}, strict: true},
	{variant: VariantSpedUp, phrases: []string{"sped up", "speed up"}, strict: true},
	{variant: VariantSlowed, phrases: []string{"slowed", "slowed down"}, strict: true},
	{variant: VariantLive, phrases: []string{"live", "in concert"}, strict: true},
	{variant: VariantRemix, phrases: []string{"remix"}, strict: true},
	{variant: VariantCover, phrases: []string{"cover", "tribute", "karaoke"}, strict: true},
	{variant: VariantAcoustic, phrases: []string{"acoustic"}},
	{variant: VariantInstrumental, phrases: []string{"instrumental"}},
	{variant: VariantRemaster, phrases: []string{"remaster", "remastered"}},
	{variant: VariantDemo, phrases: []string{"demo"}},
	{variant: VariantEdit, phrases: []string{"radio edit", "edit"}},
}

var neutralTitlePhrases = []string{
	"official audio",
	"official video",
	"official music video",
	"audio only",
	"lyrics",
	"lyric video",
	"visualizer",
	"topic",
}

// scoreCandidate returns a normalized confidence score and a rejection reason.
// A non-empty rejection reason is a hard mismatch and must not be acquired.
func scoreCandidate(track TrackSpec, candidate Candidate) (float64, string) {
	wantedMarkers := requestedVariants(track)
	candidateMarkers := detectedVariants(candidate.Title)

	for _, rule := range variantRules {
		if !rule.strict {
			continue
		}

		wanted := wantedMarkers[rule.variant]
		found := candidateMarkers[rule.variant]
		switch {
		case found && !wanted:
			return 0, fmt.Sprintf("unexpected %s version", rule.variant)
		case wanted && !found:
			return 0, fmt.Sprintf("missing requested %s version", rule.variant)
		}
	}

	titleScore := titleSimilarity(track, candidate)
	artistScore := artistSimilarity(track.Artists, candidate)
	durationScore := durationSimilarity(track, candidate)
	versionScore := versionSimilarity(wantedMarkers, candidateMarkers)

	if titleScore < 0.75 {
		return 0, "title similarity is below the conservative threshold"
	}
	if len(track.Artists) > 0 && artistScore < 0.5 {
		return 0, "artist similarity is below the conservative threshold"
	}
	if track.Duration > 0 && candidate.Duration > 0 &&
		math.Abs(track.Duration.Seconds()-candidate.Duration.Seconds()) > 30 {
		return 0, "duration differs by more than 30 seconds"
	}

	score := (0.60 * titleScore) +
		(0.20 * artistScore) +
		(0.15 * durationScore) +
		(0.05 * versionScore)

	return math.Round(score*10000) / 10000, ""
}

func candidateAuditReasons(track TrackSpec, candidate Candidate) []string {
	wantedMarkers := requestedVariants(track)
	candidateMarkers := detectedVariants(candidate.Title)

	reasons := []string{
		fmt.Sprintf("title similarity %.2f", titleSimilarity(track, candidate)),
		fmt.Sprintf("artist similarity %.2f", artistSimilarity(track.Artists, candidate)),
		fmt.Sprintf("duration similarity %.2f", durationSimilarity(track, candidate)),
		fmt.Sprintf(
			"version similarity %.2f",
			versionSimilarity(wantedMarkers, candidateMarkers),
		),
	}
	for _, rule := range variantRules {
		if wantedMarkers[rule.variant] && candidateMarkers[rule.variant] {
			reasons = append(reasons, "matched requested "+string(rule.variant)+" variant")
		}
	}
	return reasons
}

func titleSimilarity(track TrackSpec, candidate Candidate) float64 {
	wanted := normalizedTokens(track.Title)
	rawCandidate := normalizedTokens(candidate.Title)
	rawScore := referenceCoverage(wanted, rawCandidate)

	artistTokens := normalizedTokens(strings.Join(track.Artists, " "))
	cleanWanted := removeKnownPhrases(track.Title, true)
	cleanCandidate := removeKnownPhrases(candidate.Title, true)
	candidateTokens := withoutTokens(normalizedTokens(cleanCandidate), artistTokens)
	cleanScore := referenceCoverage(normalizedTokens(cleanWanted), candidateTokens)

	return math.Max(rawScore, cleanScore)
}

func artistSimilarity(wantedArtists []string, candidate Candidate) float64 {
	if len(wantedArtists) == 0 {
		return 0.5
	}

	haystacks := append([]string(nil), candidate.Artists...)
	haystacks = append(haystacks, candidate.Title)
	haystackTokens := normalizedTokens(strings.Join(haystacks, " "))

	total := 0.0
	valid := 0
	for _, artist := range wantedArtists {
		artistTokens := normalizedTokens(artist)
		if len(artistTokens) == 0 {
			continue
		}
		total += referenceCoverage(artistTokens, haystackTokens)
		valid++
	}
	if valid == 0 {
		return 0.5
	}
	return total / float64(valid)
}

func durationSimilarity(track TrackSpec, candidate Candidate) float64 {
	if track.Duration <= 0 || candidate.Duration <= 0 {
		return 0.4
	}

	delta := math.Abs(track.Duration.Seconds() - candidate.Duration.Seconds())
	switch {
	case delta <= 2:
		return 1
	case delta <= 5:
		return 0.9
	case delta <= 10:
		return 0.7
	case delta <= 15:
		return 0.4
	case delta <= 30:
		return 0.1
	default:
		return 0
	}
}

func versionSimilarity(wanted, candidate map[Variant]bool) float64 {
	mismatches := 0
	for _, rule := range variantRules {
		if wanted[rule.variant] != candidate[rule.variant] {
			mismatches++
		}
	}

	switch mismatches {
	case 0:
		return 1
	case 1:
		return 0.25
	default:
		return 0
	}
}

func requestedVariants(track TrackSpec) map[Variant]bool {
	result := detectedVariants(track.Title + " " + track.Version)
	for _, variant := range track.AllowedVariants {
		result[variant] = true
	}
	return result
}

func detectedVariants(value string) map[Variant]bool {
	normalized := " " + strings.Join(normalizedTokens(value), " ") + " "
	result := make(map[Variant]bool)
	for _, rule := range variantRules {
		for _, phrase := range rule.phrases {
			if strings.Contains(normalized, " "+phrase+" ") {
				result[rule.variant] = true
				break
			}
		}
	}
	return result
}

func removeKnownPhrases(value string, includeVariants bool) string {
	normalized := " " + strings.Join(normalizedTokens(value), " ") + " "
	phrases := append([]string(nil), neutralTitlePhrases...)
	if includeVariants {
		for _, rule := range variantRules {
			phrases = append(phrases, rule.phrases...)
		}
	}

	sort.Slice(phrases, func(i, j int) bool {
		return len(phrases[i]) > len(phrases[j])
	})
	for _, phrase := range phrases {
		normalized = strings.ReplaceAll(normalized, " "+phrase+" ", " ")
	}
	return strings.TrimSpace(normalized)
}

func normalizedTokens(value string) []string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(r)
		default:
			builder.WriteByte(' ')
		}
	}
	return strings.Fields(builder.String())
}

func withoutTokens(tokens, unwanted []string) []string {
	if len(unwanted) == 0 {
		return tokens
	}

	excluded := make(map[string]struct{}, len(unwanted))
	for _, token := range unwanted {
		excluded[token] = struct{}{}
	}

	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, found := excluded[token]; !found {
			result = append(result, token)
		}
	}
	return result
}

func referenceCoverage(reference, candidate []string) float64 {
	if len(reference) == 0 || len(candidate) == 0 {
		return 0
	}

	candidateSet := make(map[string]struct{}, len(candidate))
	for _, token := range candidate {
		candidateSet[token] = struct{}{}
	}

	referenceSet := make(map[string]struct{}, len(reference))
	for _, token := range reference {
		referenceSet[token] = struct{}{}
	}

	matched := 0
	for token := range referenceSet {
		if _, found := candidateSet[token]; found {
			matched++
		}
	}
	return float64(matched) / float64(len(referenceSet))
}
