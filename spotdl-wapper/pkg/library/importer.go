// Package library validates, tags, and atomically publishes acquired media.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
	models "github.com/supperdoggy/spot-models"
)

const (
	defaultCommandTimeout          = 5 * time.Minute
	publishedFileMode              = 0o640
	maxDirectoryComponentBytes     = 240
	maxCollisionReadyFilenameBytes = 180
	maxFilesystemComponentBytes    = 255
)

var (
	ErrInvalidAsset     = errors.New("invalid acquired asset")
	ErrChecksumMismatch = errors.New("acquired media checksum does not match provider result")
	ErrDurationMismatch = errors.New("acquired media duration does not match request")
	ErrUnsafeOutputPath = errors.New("media output path escapes the library root")
	ErrPublishCollision = errors.New("all deterministic media output paths are occupied")
)

// ImporterConfig defines the final library contract.
type ImporterConfig struct {
	LibraryRoot       string
	StagingRoot       string
	OutputTemplate    string
	FFmpegBinary      string
	FFprobeBinary     string
	CommandTimeout    time.Duration
	DurationTolerance time.Duration
}

// Importer converts a staged provider result into one canonical catalog item.
type Importer struct {
	libraryRoot       string
	stagingRoot       string
	outputTemplate    string
	ffmpegBinary      string
	ffprobeBinary     string
	commandTimeout    time.Duration
	durationTolerance time.Duration
	runner            acquisition.CommandRunner
}

// NewImporter validates paths but does not create directories until Import.
func NewImporter(config ImporterConfig, runner acquisition.CommandRunner) (*Importer, error) {
	libraryRoot, err := absoluteRequiredPath("library root", config.LibraryRoot)
	if err != nil {
		return nil, err
	}
	stagingRoot, err := absoluteRequiredPath("staging root", config.StagingRoot)
	if err != nil {
		return nil, err
	}

	outputTemplate := strings.TrimSpace(config.OutputTemplate)
	if outputTemplate == "" {
		return nil, errors.New("media output template is required")
	}
	if !filepath.IsAbs(outputTemplate) {
		outputTemplate = filepath.Join(libraryRoot, outputTemplate)
	}
	outputTemplate = filepath.Clean(outputTemplate)
	if libraryRoot == stagingRoot {
		return nil, errors.New("staging root must differ from the music library root")
	}
	if err := ensureWithinRoot(stagingRoot, outputTemplate); err == nil {
		return nil, errors.New("media output template must not be inside the staging root")
	}

	ffmpegBinary := strings.TrimSpace(config.FFmpegBinary)
	if ffmpegBinary == "" {
		ffmpegBinary = "ffmpeg"
	}
	ffprobeBinary := strings.TrimSpace(config.FFprobeBinary)
	if ffprobeBinary == "" {
		ffprobeBinary = "ffprobe"
	}
	commandTimeout := config.CommandTimeout
	if commandTimeout <= 0 {
		commandTimeout = defaultCommandTimeout
	}
	durationTolerance := config.DurationTolerance
	if durationTolerance < 0 {
		return nil, errors.New("media duration tolerance must not be negative")
	}
	if runner == nil {
		runner = acquisition.ExecCommandRunner{}
	}

	return &Importer{
		libraryRoot:       libraryRoot,
		stagingRoot:       stagingRoot,
		outputTemplate:    outputTemplate,
		ffmpegBinary:      ffmpegBinary,
		ffprobeBinary:     ffprobeBinary,
		commandTimeout:    commandTimeout,
		durationTolerance: durationTolerance,
		runner:            runner,
	}, nil
}

// Discard relinquishes a staged asset after a failed import. It removes only a
// regular file within the configured staging root and then removes its private
// attempt directory when that directory is empty.
func (i *Importer) Discard(asset acquisition.AssetResult) error {
	sourceValue := strings.TrimSpace(asset.FinalPath)
	if sourceValue == "" {
		return nil
	}
	sourcePath, err := filepath.Abs(sourceValue)
	if err != nil {
		return fmt.Errorf("resolve staged asset for cleanup: %w", err)
	}
	if err := ensureWithinRoot(i.stagingRoot, sourcePath); err != nil {
		return fmt.Errorf("%w: staged cleanup path: %v", ErrInvalidAsset, err)
	}
	attemptDirectory, err := i.validateOwnedStagedAsset(sourcePath)
	if err != nil {
		return err
	}
	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged asset: %w", err)
	}
	removeOwnedAttemptDirectory(attemptDirectory)
	return nil
}

// CleanupOrphans removes private provider attempt directories older than the
// cutoff. It deliberately ignores direct files and symbolic links.
func (i *Importer) CleanupOrphans(cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(i.stagingRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read staging root: %w", err)
	}

	removed := 0
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 ||
			!entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), acquisition.AttemptDirectoryPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect %q: %w", entry.Name(), err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}

		attemptPath := filepath.Join(i.stagingRoot, entry.Name())
		if err := ensureWithinRoot(i.stagingRoot, attemptPath); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("validate %q: %w", entry.Name(), err))
			continue
		}
		if err := validateAttemptMarker(attemptPath); err != nil {
			continue
		}
		if err := os.RemoveAll(attemptPath); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %q: %w", entry.Name(), err))
			continue
		}
		removed++
	}
	return removed, errors.Join(cleanupErrors...)
}

// Import tags, validates, and atomically publishes a staged asset. The source
// is removed only after the final file exists durably.
func (i *Importer) Import(
	ctx context.Context,
	track acquisition.TrackSpec,
	asset acquisition.AssetResult,
) (models.MusicFile, error) {
	sourceValue := strings.TrimSpace(asset.FinalPath)
	if sourceValue == "" {
		return models.MusicFile{}, fmt.Errorf("%w: final path is required", ErrInvalidAsset)
	}
	sourcePath, err := filepath.Abs(sourceValue)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: resolve final path: %v", ErrInvalidAsset, err)
	}
	if err := ensureWithinRoot(i.stagingRoot, sourcePath); err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: staged asset: %v", ErrInvalidAsset, err)
	}
	attemptDirectory, err := i.validateOwnedStagedAsset(sourcePath)
	if err != nil {
		return models.MusicFile{}, err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: stat staged file: %v", ErrInvalidAsset, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return models.MusicFile{}, fmt.Errorf("%w: staged file is not a non-empty regular file", ErrInvalidAsset)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: resolve staged file: %v", ErrInvalidAsset, err)
	}
	resolvedStagingRoot, err := filepath.EvalSymlinks(i.stagingRoot)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: resolve staging root: %v", ErrInvalidAsset, err)
	}
	if err := ensureWithinRoot(resolvedStagingRoot, resolvedSource); err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: resolved staged asset: %v", ErrInvalidAsset, err)
	}
	lexicalRelative, lexicalErr := filepath.Rel(i.stagingRoot, sourcePath)
	resolvedRelative, resolvedErr := filepath.Rel(resolvedStagingRoot, resolvedSource)
	if lexicalErr != nil || resolvedErr != nil || lexicalRelative != resolvedRelative {
		return models.MusicFile{}, fmt.Errorf("%w: staged path contains a symbolic link", ErrInvalidAsset)
	}
	sourceChecksum, err := checksumRegularFile(sourcePath)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: checksum staged file: %v", ErrInvalidAsset, err)
	}
	if expectedChecksum := strings.TrimSpace(asset.Checksum); expectedChecksum != "" &&
		!strings.EqualFold(expectedChecksum, sourceChecksum) {
		return models.MusicFile{}, fmt.Errorf(
			"%w: got %s, expected %s",
			ErrChecksumMismatch,
			sourceChecksum,
			expectedChecksum,
		)
	}

	finalPath, err := i.outputPath(track, asset, sourcePath)
	if err != nil {
		return models.MusicFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return models.MusicFile{}, fmt.Errorf("create media output directory: %w", err)
	}
	if err := ensureResolvedWithinRoot(i.libraryRoot, filepath.Dir(finalPath)); err != nil {
		return models.MusicFile{}, fmt.Errorf("%w: resolved output directory: %v", ErrUnsafeOutputPath, err)
	}

	taggedPath, err := i.tag(ctx, sourcePath, finalPath, track, asset)
	if err != nil {
		return models.MusicFile{}, err
	}
	defer func() { _ = os.Remove(taggedPath) }()

	media, err := i.validate(ctx, taggedPath, track.Duration)
	if err != nil {
		return models.MusicFile{}, err
	}
	checksum, err := checksumFile(taggedPath)
	if err != nil {
		return models.MusicFile{}, fmt.Errorf("checksum tagged media: %w", err)
	}
	if err := os.Chmod(taggedPath, publishedFileMode); err != nil {
		return models.MusicFile{}, fmt.Errorf("set published media permissions: %w", err)
	}
	if err := syncFile(taggedPath); err != nil {
		return models.MusicFile{}, fmt.Errorf("sync tagged media: %w", err)
	}

	finalPath, alreadyPresent, err := publishMedia(
		taggedPath,
		finalPath,
		track.ID,
		asset.SourceID,
		checksum,
	)
	if err != nil {
		return models.MusicFile{}, err
	}
	if alreadyPresent {
		if err := os.Chmod(finalPath, publishedFileMode); err != nil {
			return models.MusicFile{}, fmt.Errorf("set existing media permissions: %w", err)
		}
		if err := syncFile(finalPath); err != nil {
			return models.MusicFile{}, fmt.Errorf("sync existing media: %w", err)
		}
	}
	if err := syncDirectory(filepath.Dir(finalPath)); err != nil {
		return models.MusicFile{}, fmt.Errorf("sync media output directory: %w", err)
	}
	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return models.MusicFile{}, fmt.Errorf("remove staged media after publish: %w", err)
	}
	removeOwnedAttemptDirectory(attemptDirectory)

	now := time.Now().UTC().Unix()
	return models.MusicFile{
		Artist:         strings.ToLower(strings.Join(track.Artists, ", ")),
		Album:          strings.ToLower(track.Album),
		Title:          strings.ToLower(track.Title),
		SpotifyID:      track.ID,
		SpotifyURL:     track.URL,
		ISRC:           track.ISRC,
		DurationMS:     int(media.Duration / time.Millisecond),
		SourceProvider: string(asset.Provider),
		SourceID:       asset.SourceID,
		MatchScore:     asset.MatchScore,
		Checksum:       checksum,
		Format:         normalizedOutputFormat(finalPath, media.Format),
		Path:           finalPath,
		MetaData: map[string]any{
			"explicit":   track.Explicit,
			"version":    track.Version,
			"source_url": asset.SourceURL,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

type mediaInfo struct {
	Duration time.Duration
	Format   string
}

func (i *Importer) tag(
	ctx context.Context,
	sourcePath, finalPath string,
	track acquisition.TrackSpec,
	asset acquisition.AssetResult,
) (string, error) {
	extension := filepath.Ext(finalPath)
	temp, err := os.CreateTemp(filepath.Dir(finalPath), ".harmoniq-import-*"+extension)
	if err != nil {
		return "", fmt.Errorf("create tagging temp file: %w", err)
	}
	taggedPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(taggedPath)
		return "", fmt.Errorf("close tagging temp file: %w", err)
	}

	comment := strings.TrimSpace("Spotify: " + track.URL + "; Source: " + asset.SourceURL)
	args := []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", sourcePath,
		"-map", "0:a:0",
		"-c:a", "copy",
	}
	if supportsEmbeddedArtwork(extension) {
		args = append(args, "-map", "0:v?", "-c:v", "copy")
	} else {
		args = append(args, "-vn")
	}
	args = append(args,
		"-map_metadata", "0",
		"-metadata", "title="+track.Title,
		"-metadata", "artist="+strings.Join(track.Artists, ", "),
		"-metadata", "album="+track.Album,
		"-metadata", "isrc="+track.ISRC,
		"-metadata", "spotify_id="+track.ID,
		"-metadata", "comment="+comment,
		"-fflags", "+bitexact",
		taggedPath,
	)
	if _, err := i.run(ctx, i.ffmpegBinary, args...); err != nil {
		_ = os.Remove(taggedPath)
		return "", fmt.Errorf("tag acquired media: %w", err)
	}
	return taggedPath, nil
}

func (i *Importer) validate(ctx context.Context, path string, expected time.Duration) (mediaInfo, error) {
	result, err := i.run(
		ctx,
		i.ffprobeBinary,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name:format=duration,format_name",
		"-of", "json",
		path,
	)
	if err != nil {
		return mediaInfo{}, fmt.Errorf("validate acquired media: %w", err)
	}

	var probe struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(result.Stdout, &probe); err != nil {
		return mediaInfo{}, fmt.Errorf("decode ffprobe result: %w", err)
	}
	if len(probe.Streams) == 0 || probe.Streams[0].CodecName == "" {
		return mediaInfo{}, fmt.Errorf("%w: no audio stream", ErrInvalidAsset)
	}

	durationSeconds, err := time.ParseDuration(probe.Format.Duration + "s")
	if err != nil || durationSeconds <= 0 {
		return mediaInfo{}, fmt.Errorf("%w: invalid duration %q", ErrInvalidAsset, probe.Format.Duration)
	}
	tolerance := i.durationTolerance
	if proportional := time.Duration(float64(expected) * 0.05); proportional > tolerance {
		tolerance = proportional
	}
	if expected > 0 && time.Duration(math.Abs(float64(durationSeconds-expected))) > tolerance {
		return mediaInfo{}, fmt.Errorf(
			"%w: got %s, expected %s (tolerance %s)",
			ErrDurationMismatch,
			durationSeconds,
			expected,
			tolerance,
		)
	}

	format := strings.TrimSpace(probe.Format.FormatName)
	if comma := strings.IndexByte(format, ','); comma >= 0 {
		format = format[:comma]
	}
	return mediaInfo{Duration: durationSeconds, Format: format}, nil
}

func (i *Importer) run(ctx context.Context, name string, args ...string) (acquisition.CommandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, i.commandTimeout)
	defer cancel()
	result, err := i.runner.Run(commandCtx, name, args...)
	if commandCtx.Err() != nil {
		return result, commandCtx.Err()
	}
	if err != nil {
		diagnostic := strings.TrimSpace(string(result.Stderr))
		if len(diagnostic) > 4096 {
			diagnostic = diagnostic[:4096]
		}
		if diagnostic != "" {
			return result, fmt.Errorf("%w: %s", err, diagnostic)
		}
		return result, err
	}
	return result, nil
}

func (i *Importer) outputPath(
	track acquisition.TrackSpec,
	asset acquisition.AssetResult,
	sourcePath string,
) (string, error) {
	format := strings.TrimPrefix(strings.TrimSpace(asset.Format), ".")
	if format == "" {
		format = strings.TrimPrefix(filepath.Ext(sourcePath), ".")
	}
	replacer := strings.NewReplacer(
		"{artists}", safeComponent(strings.Join(track.Artists, ", ")),
		"{artist}", safeComponent(firstArtist(track.Artists)),
		"{title}", safeComponent(track.Title),
		"{album}", safeComponent(track.Album),
		"{spotify-id}", safeComponent(track.ID),
		"{output-ext}", safeComponent(format),
	)
	output := filepath.Clean(replacer.Replace(i.outputTemplate))
	if filepath.Ext(output) == "" && format != "" {
		output += "." + format
	}
	if err := ensureWithinRoot(i.libraryRoot, output); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafeOutputPath, err)
	}
	output, err := boundedOutputPath(i.libraryRoot, output)
	if err != nil {
		return "", err
	}
	if err := ensureWithinRoot(i.libraryRoot, output); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafeOutputPath, err)
	}
	return output, nil
}

func publishMedia(
	taggedPath, desiredPath, trackID, sourceID, checksum string,
) (string, bool, error) {
	shortChecksum := checksum
	if len(shortChecksum) > 12 {
		shortChecksum = shortChecksum[:12]
	}
	candidates := uniquePaths(
		desiredPath,
		appendFilenameSuffix(desiredPath, firstNonEmpty(trackID, sourceID)),
		appendFilenameSuffix(desiredPath, shortChecksum),
		appendFilenameSuffix(desiredPath, checksum),
	)

	for _, candidate := range candidates {
		for {
			existingChecksum, err := checksumRegularFile(candidate)
			switch {
			case err == nil && existingChecksum == checksum:
				return candidate, true, nil
			case err == nil:
				// This deterministic name belongs to different content.
				break
			case !errors.Is(err, os.ErrNotExist):
				return "", false, fmt.Errorf("inspect existing media %q: %w", candidate, err)
			default:
				err = os.Link(taggedPath, candidate)
				if err == nil {
					return candidate, false, nil
				}
				if errors.Is(err, os.ErrExist) {
					// Another importer won the race. Re-inspect the winner and
					// accept it when the bytes are identical.
					continue
				}
				return "", false, fmt.Errorf("atomically publish media %q: %w", candidate, err)
			}
			break
		}
	}

	return "", false, fmt.Errorf("%w: %q", ErrPublishCollision, desiredPath)
}

func appendFilenameSuffix(path, suffix string) string {
	extension := filepath.Ext(path)
	base := strings.TrimSuffix(path, extension)
	candidate := base + "-" + safeComponent(suffix) + extension
	return filepath.Join(
		filepath.Dir(candidate),
		boundedFilename(filepath.Base(candidate), maxFilesystemComponentBytes),
	)
}

func checksumFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checksumRegularFile(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular non-symbolic-link file")
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", errors.New("file changed while it was opened")
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	after, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(opened, after) {
		return "", errors.New("file changed while it was read")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func uniquePaths(paths ...string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func normalizedOutputFormat(path, fallback string) string {
	if extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."); extension != "" {
		return extension
	}
	return strings.ToLower(strings.TrimSpace(fallback))
}

func supportsEmbeddedArtwork(extension string) bool {
	switch strings.ToLower(strings.TrimPrefix(extension, ".")) {
	case "mp3", "flac", "m4a":
		return true
	default:
		return false
	}
}

func boundedOutputPath(root, output string) (string, error) {
	relative, err := filepath.Rel(root, output)
	if err != nil {
		return "", fmt.Errorf("map output path beneath library: %w", err)
	}
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		limit := maxDirectoryComponentBytes
		if index == len(components)-1 {
			limit = maxCollisionReadyFilenameBytes
			component = boundedFilename(component, limit)
		} else {
			component = boundedComponent(component, limit)
		}
		components[index] = component
	}
	return filepath.Join(append([]string{root}, components...)...), nil
}

func boundedFilename(value string, maximumBytes int) string {
	extension := filepath.Ext(value)
	stem := strings.TrimSuffix(value, extension)
	if len([]byte(value)) <= maximumBytes {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "-" + hex.EncodeToString(sum[:6])
	maximumStemBytes := maximumBytes - len([]byte(extension)) - len(suffix)
	stem = truncateUTF8Bytes(stem, maximumStemBytes)
	stem = strings.TrimRight(stem, " ._-")
	if stem == "" {
		stem = "media"
	}
	return stem + suffix + extension
}

func boundedComponent(value string, maximumBytes int) string {
	if len([]byte(value)) <= maximumBytes {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "-" + hex.EncodeToString(sum[:6])
	prefix := truncateUTF8Bytes(value, maximumBytes-len(suffix))
	prefix = strings.TrimRight(prefix, " ._-")
	if prefix == "" {
		prefix = "directory"
	}
	return prefix + suffix
}

func truncateUTF8Bytes(value string, maximumBytes int) string {
	if maximumBytes <= 0 {
		return ""
	}
	if len([]byte(value)) <= maximumBytes {
		return value
	}
	var result strings.Builder
	for _, r := range value {
		if result.Len()+len(string(r)) > maximumBytes {
			break
		}
		result.WriteRune(r)
	}
	return result.String()
}

func (i *Importer) validateOwnedStagedAsset(sourcePath string) (string, error) {
	attemptDirectory := filepath.Dir(sourcePath)
	if filepath.Dir(attemptDirectory) != i.stagingRoot ||
		!strings.HasPrefix(filepath.Base(attemptDirectory), acquisition.AttemptDirectoryPrefix) {
		return "", fmt.Errorf(
			"%w: asset is not inside a private Harmoniq attempt directory",
			ErrInvalidAsset,
		)
	}
	if err := validateAttemptMarker(attemptDirectory); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidAsset, err)
	}

	resolvedStagingRoot, err := filepath.EvalSymlinks(i.stagingRoot)
	if err != nil {
		return "", fmt.Errorf("%w: resolve staging root: %v", ErrInvalidAsset, err)
	}
	resolvedAttempt, err := filepath.EvalSymlinks(attemptDirectory)
	if err != nil {
		return "", fmt.Errorf("%w: resolve attempt directory: %v", ErrInvalidAsset, err)
	}
	if err := ensureWithinRoot(resolvedStagingRoot, resolvedAttempt); err != nil {
		return "", fmt.Errorf("%w: resolved attempt directory: %v", ErrInvalidAsset, err)
	}
	lexicalRelative, lexicalErr := filepath.Rel(i.stagingRoot, attemptDirectory)
	resolvedRelative, resolvedErr := filepath.Rel(resolvedStagingRoot, resolvedAttempt)
	if lexicalErr != nil || resolvedErr != nil || lexicalRelative != resolvedRelative {
		return "", fmt.Errorf("%w: attempt path contains a symbolic link", ErrInvalidAsset)
	}

	info, err := os.Lstat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("%w: stat staged file: %v", ErrInvalidAsset, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("%w: staged file is not a non-empty regular file", ErrInvalidAsset)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve staged file: %v", ErrInvalidAsset, err)
	}
	if err := ensureWithinRoot(resolvedAttempt, resolvedSource); err != nil {
		return "", fmt.Errorf("%w: resolved staged asset: %v", ErrInvalidAsset, err)
	}
	sourceRelative, sourceRelativeErr := filepath.Rel(attemptDirectory, sourcePath)
	resolvedSourceRelative, resolvedSourceRelativeErr := filepath.Rel(resolvedAttempt, resolvedSource)
	if sourceRelativeErr != nil ||
		resolvedSourceRelativeErr != nil ||
		sourceRelative != resolvedSourceRelative {
		return "", fmt.Errorf("%w: staged path contains a symbolic link", ErrInvalidAsset)
	}
	return attemptDirectory, nil
}

func validateAttemptMarker(attemptDirectory string) error {
	markerPath := filepath.Join(attemptDirectory, acquisition.AttemptMarkerFilename)
	info, err := os.Lstat(markerPath)
	if err != nil {
		return fmt.Errorf("inspect attempt marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("attempt marker is not a regular non-symbolic-link file")
	}
	return nil
}

func removeOwnedAttemptDirectory(attemptDirectory string) {
	markerPath := filepath.Join(attemptDirectory, acquisition.AttemptMarkerFilename)
	if err := os.Remove(markerPath); err != nil {
		return
	}
	if err := os.Remove(attemptDirectory); err != nil {
		// Unexpected provider leftovers remain explicitly marked so the TTL
		// sweeper can reclaim the whole owned attempt later.
		marker, createErr := os.OpenFile(
			markerPath,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o600,
		)
		if createErr == nil {
			_ = marker.Close()
		}
	}
}

func ensureWithinRoot(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q is outside %q", path, root)
	}
	return nil
}

func ensureResolvedWithinRoot(root, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	return ensureWithinRoot(resolvedRoot, resolvedPath)
}

func absoluteRequiredPath(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("make %s absolute: %w", name, err)
	}
	return filepath.Clean(absolute), nil
}

func safeComponent(value string) string {
	original := value
	var builder strings.Builder
	lastSeparator := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			if !lastSeparator {
				builder.WriteByte('_')
				lastSeparator = true
			}
			continue
		}
		builder.WriteRune(r)
		lastSeparator = false
	}
	value = strings.Trim(builder.String(), " ._-")
	if value == "" {
		return "unknown"
	}
	if isPortableReservedName(value) {
		value = "_" + value
	}
	const maximumComponentBytes = 180
	if len([]byte(value)) > maximumComponentBytes {
		sum := sha256.Sum256([]byte(original))
		suffix := "-" + hex.EncodeToString(sum[:6])
		maximumPrefixBytes := maximumComponentBytes - len(suffix)
		var prefix strings.Builder
		for _, r := range value {
			if prefix.Len()+len(string(r)) > maximumPrefixBytes {
				break
			}
			prefix.WriteRune(r)
		}
		value = strings.TrimRight(prefix.String(), " ._-") + suffix
	}
	return value
}

func isPortableReservedName(value string) bool {
	base := strings.ToUpper(value)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(base) == 4 &&
		(base[:3] == "COM" || base[:3] == "LPT") &&
		base[3] >= '1' && base[3] <= '9'
}

func firstArtist(artists []string) string {
	if len(artists) == 0 {
		return ""
	}
	return artists[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "alternate"
}
