package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrPathOutsideLibrary means a catalog path escaped the configured media
	// root while being converted to an M3U entry.
	ErrPathOutsideLibrary = errors.New("playlist path is outside the music library")
)

const maxPlaylistNameBytes = 180

type PlaylistTrack struct {
	Name          string   `json:"name"`
	Artists       []string `json:"artists"`
	Artist        string   `json:"artist"`
	Genres        []string `json:"genres"`
	DiscNumber    int      `json:"disc_number"`
	DiscCount     int      `json:"disc_count"`
	AlbumName     string   `json:"album_name"`
	AlbumArtist   string   `json:"album_artist"`
	Duration      int      `json:"duration"`
	Year          string   `json:"year"`
	Date          string   `json:"date"`
	TrackNumber   int      `json:"track_number"`
	TracksCount   int      `json:"tracks_count"`
	SongID        string   `json:"song_id"`
	Explicit      bool     `json:"explicit"`
	Publisher     string   `json:"publisher"`
	URL           string   `json:"url"`
	ISRC          string   `json:"isrc"`
	CoverURL      string   `json:"cover_url"`
	CopyrightText string   `json:"copyright_text"`
	DownloadURL   *string  `json:"download_url"` // Nullable field
	Lyrics        string   `json:"lyrics"`
	Popularity    int      `json:"popularity"`
	AlbumID       string   `json:"album_id"`
	ListName      string   `json:"list_name"`
	ListURL       string   `json:"list_url"`
	ListPosition  int      `json:"list_position"`
	ListLength    int      `json:"list_length"`
	ArtistID      string   `json:"artist_id"`
	AlbumType     string   `json:"album_type"`
}

func FindUnindexedSongs(songList []string, musicRoot, _ string) ([]string, error) {
	var matchedPaths []string
	matchedSongs := make(map[string]bool)

	for _, song := range songList {
		parts := strings.SplitN(song, " - ", 2)
		if len(parts) != 2 {
			continue
		}

		songLower := strings.ToLower(song)

		var matchedPath string

		walkErr := filepath.Walk(musicRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || strings.HasSuffix(strings.ToLower(path), ".lrc") {
				return nil
			}

			if _, ok := matchedSongs[song]; ok {
				return filepath.SkipAll
			}

			lowerPath := strings.ToLower(path)

			if strings.Contains(lowerPath, songLower+".") {
				var mapErr error
				matchedPath, mapErr = playlistEntryPath(path, musicRoot)
				if mapErr != nil {
					return mapErr
				}
				matchedSongs[song] = true
				return filepath.SkipAll
			}

			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
			return nil, fmt.Errorf("scan music library: %w", walkErr)
		}

		if matchedPath != "" {
			matchedPaths = append(matchedPaths, matchedPath)
		}
	}

	return matchedPaths, nil
}

// CreateM3UPlaylist atomically replaces an M3U playlist with catalog paths
// normalized beneath musicRoot. Relative catalog paths are interpreted as
// relative to musicRoot; absolute paths must already be inside it.
//
// The file is written beside the destination and renamed only after its
// contents have been flushed and synced, so readers observe either the prior
// complete playlist or the new complete playlist.
func CreateM3UPlaylist(matchedPaths []string, musicRoot, outputPath string) error {
	root, err := normalizedLibraryRoot(musicRoot)
	if err != nil {
		return err
	}

	entries := make([]string, 0, len(matchedPaths))
	for _, path := range matchedPaths {
		entry, err := playlistEntryPathUnderRoot(path, root)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}

	if outputPath == "" || strings.ContainsAny(outputPath, "\x00\r\n") {
		return errors.New("playlist output path is invalid")
	}

	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create playlist output directory: %w", err)
	}

	temp, err := os.CreateTemp(parent, ".harmoniq-playlist-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary playlist: %w", err)
	}
	tempPath := temp.Name()
	tempClosed := false
	defer func() {
		if !tempClosed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o640); err != nil {
		return fmt.Errorf("set playlist permissions: %w", err)
	}

	var content strings.Builder
	for _, entry := range entries {
		content.WriteString(entry)
		content.WriteByte('\n')
	}
	if _, err := temp.WriteString(content.String()); err != nil {
		return fmt.Errorf("write temporary playlist: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary playlist: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary playlist: %w", err)
	}
	tempClosed = true

	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("replace playlist atomically: %w", err)
	}
	directory, err := os.Open(parent)
	if err != nil {
		return fmt.Errorf("open playlist output directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return fmt.Errorf("sync playlist output directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close playlist output directory: %w", closeErr)
	}

	return nil
}

func playlistEntryPath(catalogPath, musicRoot string) (string, error) {
	root, err := normalizedLibraryRoot(musicRoot)
	if err != nil {
		return "", err
	}
	return playlistEntryPathUnderRoot(catalogPath, root)
}

func normalizedLibraryRoot(musicRoot string) (string, error) {
	if musicRoot == "" || strings.ContainsAny(musicRoot, "\x00\r\n") {
		return "", errors.New("music library path is invalid")
	}

	root, err := filepath.Abs(musicRoot)
	if err != nil {
		return "", fmt.Errorf("resolve music library path: %w", err)
	}
	return filepath.Clean(root), nil
}

func playlistEntryPathUnderRoot(catalogPath, root string) (string, error) {
	if catalogPath == "" || strings.ContainsAny(catalogPath, "\x00\r\n") {
		return "", errors.New("catalog path is invalid")
	}

	candidate := catalogPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve catalog path: %w", err)
	}
	candidate = filepath.Clean(candidate)

	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("map catalog path to music library: %w", err)
	}
	if relative == "." ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"%w: %q is not beneath %q",
			ErrPathOutsideLibrary,
			catalogPath,
			root,
		)
	}

	info, err := os.Lstat(candidate)
	if err != nil {
		return "", fmt.Errorf("inspect catalog path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("catalog path is not a regular non-symbolic-link file")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve music library root: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve catalog path: %w", err)
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return "", fmt.Errorf("map resolved catalog path to music library: %w", err)
	}
	if resolvedRelative == "." ||
		resolvedRelative == ".." ||
		strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"%w: resolved %q is not beneath %q",
			ErrPathOutsideLibrary,
			catalogPath,
			root,
		)
	}

	return filepath.ToSlash(filepath.Join(root, relative)), nil
}

// SanitizePlaylistName converts an external playlist title into one portable
// filename component. It preserves Unicode while removing traversal,
// separators, control characters, and common cross-platform reserved names.
func SanitizePlaylistName(name string) string {
	var sanitized strings.Builder
	sanitized.Grow(len(name))

	lastWasDash := false
	lastWasSpace := false
	for _, r := range strings.TrimSpace(name) {
		if isInvalidFilenameRune(r) {
			if sanitized.Len() > 0 && !lastWasDash {
				sanitized.WriteByte('-')
				lastWasDash = true
			}
			lastWasSpace = false
			continue
		}

		if unicode.IsSpace(r) {
			if sanitized.Len() > 0 && !lastWasSpace {
				sanitized.WriteByte(' ')
				lastWasSpace = true
			}
			lastWasDash = false
			continue
		}

		sanitized.WriteRune(r)
		lastWasDash = r == '-'
		lastWasSpace = false
	}

	result := strings.Trim(sanitized.String(), " .-")
	for {
		collapsed := strings.NewReplacer(
			" -", "-",
			"- ", "-",
			"--", "-",
		).Replace(result)
		if collapsed == result {
			break
		}
		result = collapsed
	}
	result = truncateUTF8(result, maxPlaylistNameBytes)
	result = strings.Trim(result, " .-")
	if result == "" {
		return "playlist"
	}

	base := result
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if isReservedFilename(base) {
		result = "_" + result
	}
	return result
}

func isInvalidFilenameRune(r rune) bool {
	if unicode.IsControl(r) {
		return true
	}
	return strings.ContainsRune(`<>:"/\|?*`, r)
}

func truncateUTF8(value string, maximumBytes int) string {
	if len(value) <= maximumBytes {
		return value
	}

	for offset, r := range value {
		if offset+utf8.RuneLen(r) > maximumBytes {
			return value[:offset]
		}
	}
	return value
}

func isReservedFilename(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
