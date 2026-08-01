package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	defaultSpotDLBinary = "spotdl"
	defaultAudioFormat  = "mp3"
)

// SpotDLConfig configures the compatibility provider. OutputDirectory is
// required. Each attempt uses an isolated directory and a formatter template
// ending in {output-ext}; this follows spotDL's output contract while still
// letting Acquire derive and verify the concrete result path.
type SpotDLConfig struct {
	Binary          string
	OutputDirectory string
	AudioFormat     string
	UseConfig       bool
	DisableCache    bool
	CommandTimeout  time.Duration
}

// SpotDLProvider adapts the existing spotDL CLI to Provider.
type SpotDLProvider struct {
	binary          string
	outputDirectory string
	audioFormat     string
	useConfig       bool
	disableCache    bool
	commandTimeout  time.Duration
	runner          CommandRunner
}

// NewSpotDLProvider creates a spotDL compatibility provider.
func NewSpotDLProvider(config SpotDLConfig, runner CommandRunner) (*SpotDLProvider, error) {
	outputDirectory := strings.TrimSpace(config.OutputDirectory)
	if outputDirectory == "" {
		return nil, errors.New("spotdl output directory is required")
	}

	absoluteOutputDirectory, err := filepath.Abs(outputDirectory)
	if err != nil {
		return nil, fmt.Errorf("make spotdl output directory absolute: %w", err)
	}

	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = defaultSpotDLBinary
	}

	audioFormat, err := normalizeAudioFormat(config.AudioFormat)
	if err != nil {
		return nil, fmt.Errorf("spotdl audio format: %w", err)
	}

	return &SpotDLProvider{
		binary:          binary,
		outputDirectory: absoluteOutputDirectory,
		audioFormat:     audioFormat,
		useConfig:       config.UseConfig,
		disableCache:    config.DisableCache,
		commandTimeout:  normalizeTimeout(config.CommandTimeout),
		runner:          normalizeRunner(runner),
	}, nil
}

func (p *SpotDLProvider) Name() ProviderName {
	return ProviderSpotDL
}

// Resolve returns the canonical URL as spotDL's sole candidate. spotDL keeps
// responsibility for its internal media-source matching during Acquire.
func (p *SpotDLProvider) Resolve(
	ctx context.Context,
	track TrackSpec,
) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sourceURL := strings.TrimSpace(track.URL)
	if sourceURL == "" {
		return nil, errors.New("spotdl requires a canonical track URL")
	}

	sourceID := canonicalSourceID(track.ID, sourceURL)
	return []Candidate{{
		Provider:  ProviderSpotDL,
		SourceID:  sourceID,
		SourceURL: sourceURL,
		Title:     track.Title,
		Artists:   append([]string(nil), track.Artists...),
		Duration:  track.Duration,
		Format:    p.audioFormat,
		// The compatibility adapter cannot observe or independently score the
		// media-source match spotDL chooses internally.
		Score: 0,
		Reasons: []string{
			"unscored: spotDL performs media-source matching during acquisition",
		},
	}}, nil
}

// Acquire invokes spotDL with direct argv values and a deterministic output
// path. No shell is involved.
func (p *SpotDLProvider) Acquire(
	ctx context.Context,
	_ TrackSpec,
	candidate Candidate,
) (AssetResult, error) {
	if err := validateCandidate(ProviderSpotDL, candidate); err != nil {
		return AssetResult{}, err
	}

	sourceID := canonicalSourceID(candidate.SourceID, candidate.SourceURL)
	if err := ensureOutputParent(filepath.Join(p.outputDirectory, "asset")); err != nil {
		return AssetResult{}, err
	}
	attemptDirectory, err := os.MkdirTemp(
		p.outputDirectory,
		AttemptDirectoryPrefix+safeFilenameComponent(sourceID)+"-",
	)
	if err != nil {
		return AssetResult{}, fmt.Errorf("create spotDL attempt directory: %w", err)
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

	outputTemplate := filepath.Join(
		attemptDirectory,
		safeFilenameComponent(sourceID)+".{output-ext}",
	)
	finalPath := filepath.Join(
		attemptDirectory,
		safeFilenameComponent(sourceID)+"."+p.audioFormat,
	)

	args := []string{
		candidate.SourceURL,
		"--output", outputTemplate,
		"--format", p.audioFormat,
	}
	if p.useConfig {
		args = append(args, "--config")
	}
	if p.disableCache {
		args = append(args, "--no-cache")
	}

	if _, err := runCommand(
		ctx,
		p.commandTimeout,
		ProviderSpotDL,
		p.runner,
		p.binary,
		args...,
	); err != nil {
		return AssetResult{}, err
	}

	info, err := os.Stat(finalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AssetResult{}, fmt.Errorf("%w: spotDL did not create %q", ErrMissingFinalPath, finalPath)
		}
		return AssetResult{}, fmt.Errorf("inspect spotDL output: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return AssetResult{}, fmt.Errorf("%w: spotDL output is not a non-empty regular file", ErrMissingFinalPath)
	}
	checksum, err := checksumFileIfExists(finalPath)
	if err != nil {
		return AssetResult{}, err
	}
	keepAttempt = true

	return AssetResult{
		Provider:   ProviderSpotDL,
		SourceID:   sourceID,
		SourceURL:  candidate.SourceURL,
		FinalPath:  finalPath,
		Format:     p.audioFormat,
		Checksum:   checksum,
		MatchScore: candidate.Score,
	}, nil
}

func canonicalSourceID(id, sourceURL string) string {
	if id = strings.TrimSpace(id); id != "" {
		return id
	}

	if strings.HasPrefix(sourceURL, "spotify:") {
		parts := strings.Split(sourceURL, ":")
		if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) != "" {
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}

	if parsed, err := url.Parse(sourceURL); err == nil {
		if videoID := strings.TrimSpace(parsed.Query().Get("v")); videoID != "" {
			return videoID
		}
		path := strings.Trim(parsed.Path, "/")
		if path != "" {
			parts := strings.Split(path, "/")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}

	sum := sha256.Sum256([]byte(sourceURL))
	return hex.EncodeToString(sum[:8])
}

func safeFilenameComponent(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))

	lastWasReplacement := false
	for _, r := range value {
		safe := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'
		if safe {
			builder.WriteRune(r)
			lastWasReplacement = false
			continue
		}
		if !lastWasReplacement {
			builder.WriteByte('_')
			lastWasReplacement = true
		}
	}

	result := strings.Trim(builder.String(), "_.-")
	if result != "" {
		return result
	}

	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func normalizeAudioFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), "."))
	if format == "" {
		return defaultAudioFormat, nil
	}

	switch format {
	case "mp3", "flac", "ogg", "opus", "m4a", "wav":
		return format, nil
	default:
		return "", fmt.Errorf(
			"%q is unsupported; expected mp3, flac, ogg, opus, m4a, or wav",
			format,
		)
	}
}

func markOwnedAttempt(directory string) error {
	markerPath := filepath.Join(directory, AttemptMarkerFilename)
	file, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("mark provider attempt directory: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close provider attempt marker: %w", err)
	}
	return nil
}
