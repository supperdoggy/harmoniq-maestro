package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultYTDLPBinary = "yt-dlp"
	defaultSearchLimit = 10
)

// YTDLPConfig configures direct yt-dlp resolution and acquisition.
type YTDLPConfig struct {
	Binary         string
	OutputTemplate string
	AudioFormat    string
	SearchLimit    int
	MinimumScore   float64
	CommandTimeout time.Duration
}

// YTDLPProvider resolves candidates from yt-dlp JSON and acquires one selected
// candidate. It is used only when a caller explicitly selects ProviderYTDLP.
type YTDLPProvider struct {
	binary          string
	outputTemplate  string
	audioFormat     string
	searchLimit     int
	minimumScore    float64
	commandTimeout  time.Duration
	outputDirectory string
	runner          CommandRunner
}

// NewYTDLPProvider creates a direct yt-dlp provider.
func NewYTDLPProvider(config YTDLPConfig, runner CommandRunner) (*YTDLPProvider, error) {
	outputTemplate := strings.TrimSpace(config.OutputTemplate)
	if outputTemplate == "" {
		return nil, errors.New("yt-dlp output template is required")
	}
	absoluteOutputTemplate, err := filepath.Abs(outputTemplate)
	if err != nil {
		return nil, fmt.Errorf("make yt-dlp output template absolute: %w", err)
	}
	outputDirectory := filepath.Dir(absoluteOutputTemplate)
	if strings.Contains(outputDirectory, "%(") {
		return nil, errors.New("yt-dlp output template directory must be concrete")
	}

	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = defaultYTDLPBinary
	}

	audioFormat, err := normalizeAudioFormat(config.AudioFormat)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp audio format: %w", err)
	}

	searchLimit := config.SearchLimit
	if searchLimit == 0 {
		searchLimit = defaultSearchLimit
	}
	if searchLimit < 1 || searchLimit > 50 {
		return nil, errors.New("yt-dlp search limit must be between 1 and 50")
	}

	minimumScore := config.MinimumScore
	if minimumScore < 0 || minimumScore > 1 {
		return nil, errors.New("yt-dlp minimum score must be between 0 and 1")
	}

	return &YTDLPProvider{
		binary:          binary,
		outputTemplate:  absoluteOutputTemplate,
		outputDirectory: outputDirectory,
		audioFormat:     audioFormat,
		searchLimit:     searchLimit,
		minimumScore:    minimumScore,
		commandTimeout:  normalizeTimeout(config.CommandTimeout),
		runner:          normalizeRunner(runner),
	}, nil
}

func (p *YTDLPProvider) Name() ProviderName {
	return ProviderYTDLP
}

// Resolve uses yt-dlp's stable JSON output and retains only candidates that
// pass conservative metadata and version checks.
func (p *YTDLPProvider) Resolve(
	ctx context.Context,
	track TrackSpec,
) ([]Candidate, error) {
	if strings.TrimSpace(track.Title) == "" {
		return nil, errors.New("yt-dlp resolution requires a track title")
	}

	query := buildSearchQuery(track, p.searchLimit)
	result, err := runCommand(
		ctx,
		p.commandTimeout,
		ProviderYTDLP,
		p.runner,
		p.binary,
		"--dump-single-json",
		"--flat-playlist",
		"--no-warnings",
		query,
	)
	if err != nil {
		return nil, err
	}

	entries, err := decodeYTDLPEntries(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("decode yt-dlp candidate JSON: %w", err)
	}

	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		candidate := entry.candidate()
		if candidate.Title == "" || candidate.SourceURL == "" {
			continue
		}

		score, rejection := scoreCandidate(track, candidate)
		if rejection != "" || score < p.minimumScore {
			continue
		}
		candidate.Score = score
		candidate.Reasons = candidateAuditReasons(track, candidate)
		candidates = append(candidates, candidate)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}
	return candidates, nil
}

// Acquire downloads the caller-selected candidate and reads the final path
// from yt-dlp's after_move print hook.
func (p *YTDLPProvider) Acquire(
	ctx context.Context,
	_ TrackSpec,
	candidate Candidate,
) (AssetResult, error) {
	if err := validateCandidate(ProviderYTDLP, candidate); err != nil {
		return AssetResult{}, err
	}
	if err := ensureOutputParent(p.outputTemplate); err != nil {
		return AssetResult{}, err
	}
	attemptDirectory, err := os.MkdirTemp(
		p.outputDirectory,
		AttemptDirectoryPrefix+
			safeFilenameComponent(canonicalSourceID(candidate.SourceID, candidate.SourceURL))+
			"-",
	)
	if err != nil {
		return AssetResult{}, fmt.Errorf("create yt-dlp attempt directory: %w", err)
	}
	keepAttempt := false
	defer func() {
		if !keepAttempt {
			_ = os.RemoveAll(attemptDirectory)
		}
	}()
	if err := markOwnedAttempt(attemptDirectory); err != nil {
		return AssetResult{}, err
	}
	outputTemplate := filepath.Join(attemptDirectory, filepath.Base(p.outputTemplate))

	args := []string{
		"--quiet",
		"--no-warnings",
		"--no-playlist",
		"--no-simulate",
		"--extract-audio",
		"--audio-format", ytDLPAudioFormat(p.audioFormat),
		"--output", outputTemplate,
		"--print", "after_move:filepath",
		candidate.SourceURL,
	}
	result, err := runCommand(
		ctx,
		p.commandTimeout,
		ProviderYTDLP,
		p.runner,
		p.binary,
		args...,
	)
	if err != nil {
		return AssetResult{}, err
	}

	finalPath := lastNonEmptyLine(result.Stdout)
	if finalPath == "" {
		return AssetResult{}, ErrMissingFinalPath
	}

	absoluteFinalPath, err := filepath.Abs(finalPath)
	if err != nil {
		return AssetResult{}, fmt.Errorf("make yt-dlp final path absolute: %w", err)
	}
	if err := ensurePathWithinRoot(attemptDirectory, absoluteFinalPath); err != nil {
		return AssetResult{}, fmt.Errorf("%w: %v", ErrUnsafeFinalPath, err)
	}
	info, err := os.Lstat(absoluteFinalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AssetResult{}, fmt.Errorf("%w: yt-dlp did not create %q", ErrMissingFinalPath, absoluteFinalPath)
		}
		return AssetResult{}, fmt.Errorf("inspect yt-dlp output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return AssetResult{}, fmt.Errorf("%w: yt-dlp output is not a non-empty regular file", ErrMissingFinalPath)
	}

	format := p.audioFormat
	if extension := strings.TrimPrefix(filepath.Ext(absoluteFinalPath), "."); extension != "" {
		format = strings.ToLower(extension)
	}

	checksum, err := checksumFileIfExists(absoluteFinalPath)
	if err != nil {
		return AssetResult{}, err
	}
	keepAttempt = true

	return AssetResult{
		Provider:   ProviderYTDLP,
		SourceID:   candidate.SourceID,
		SourceURL:  candidate.SourceURL,
		FinalPath:  absoluteFinalPath,
		Format:     format,
		Checksum:   checksum,
		MatchScore: candidate.Score,
	}, nil
}

func ytDLPAudioFormat(format string) string {
	if format == "ogg" {
		return "vorbis"
	}
	return format
}

func buildSearchQuery(track TrackSpec, limit int) string {
	terms := make([]string, 0, len(track.Artists)+3)
	terms = append(terms, track.Artists...)
	terms = append(terms, track.Title)
	if strings.TrimSpace(track.Version) != "" {
		terms = append(terms, track.Version)
	}
	terms = append(terms, "audio")

	return "ytsearch" + strconv.Itoa(limit) + ":" + strings.Join(terms, " ")
}

type ytDLPEnvelope struct {
	Entries []ytDLPEntry `json:"entries"`
	ytDLPEntry
}

type ytDLPEntry struct {
	ID           string  `json:"id"`
	URL          string  `json:"url"`
	WebpageURL   string  `json:"webpage_url"`
	OriginalURL  string  `json:"original_url"`
	Title        string  `json:"title"`
	Duration     float64 `json:"duration"`
	Artist       string  `json:"artist"`
	Creator      string  `json:"creator"`
	Uploader     string  `json:"uploader"`
	Channel      string  `json:"channel"`
	Extractor    string  `json:"extractor"`
	ExtractorKey string  `json:"extractor_key"`
	Ext          string  `json:"ext"`
}

func decodeYTDLPEntries(data []byte) ([]ytDLPEntry, error) {
	var envelope ytDLPEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Entries) > 0 {
		return envelope.Entries, nil
	}
	if envelope.ID != "" || envelope.Title != "" {
		return []ytDLPEntry{envelope.ytDLPEntry}, nil
	}
	return nil, nil
}

func (entry ytDLPEntry) candidate() Candidate {
	sourceURL := entry.sourceURL()
	artists := uniqueNonEmpty(
		entry.Artist,
		entry.Creator,
		trimTopicSuffix(entry.Uploader),
		trimTopicSuffix(entry.Channel),
	)

	return Candidate{
		Provider:  ProviderYTDLP,
		SourceID:  canonicalSourceID(entry.ID, sourceURL),
		SourceURL: sourceURL,
		Title:     strings.TrimSpace(entry.Title),
		Artists:   artists,
		Duration:  time.Duration(entry.Duration * float64(time.Second)),
		Format:    strings.TrimSpace(entry.Ext),
		Uploader:  strings.TrimSpace(entry.Uploader),
	}
}

func (entry ytDLPEntry) sourceURL() string {
	for _, value := range []string{
		entry.WebpageURL,
		entry.OriginalURL,
		entry.URL,
	} {
		value = strings.TrimSpace(value)
		if parsed, err := url.Parse(value); err == nil &&
			(parsed.Scheme == "http" || parsed.Scheme == "https") {
			return value
		}
	}

	extractor := strings.ToLower(entry.Extractor + " " + entry.ExtractorKey)
	if strings.Contains(extractor, "youtube") && entry.ID != "" {
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(entry.ID)
	}
	return strings.TrimSpace(entry.URL)
}

func uniqueNonEmpty(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func trimTopicSuffix(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.HasSuffix(lower, " - topic") {
		return strings.TrimSpace(value[:len(value)-len(" - Topic")])
	}
	return value
}

func lastNonEmptyLine(data []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
