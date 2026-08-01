package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateM3UPlaylist(t *testing.T) {
	libraryRoot := filepath.Join(t.TempDir(), "music")
	outputPath := filepath.Join(libraryRoot, "playlists", "test.m3u")

	paths := []string{
		filepath.Join(libraryRoot, "downloads", "Artist1 - Song1.flac"),
		filepath.Join("downloads", "Artist2 - Song2.flac"),
		filepath.Join(libraryRoot, "Artist3 - Song3.flac"),
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(libraryRoot, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	err := CreateM3UPlaylist(paths, libraryRoot, outputPath)
	if err != nil {
		t.Fatalf("CreateM3UPlaylist failed: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	want := strings.Join([]string{
		filepath.ToSlash(filepath.Join(libraryRoot, "downloads", "Artist1 - Song1.flac")),
		filepath.ToSlash(filepath.Join(libraryRoot, "downloads", "Artist2 - Song2.flac")),
		filepath.ToSlash(filepath.Join(libraryRoot, "Artist3 - Song3.flac")),
		"",
	}, "\n")
	if got := string(content); got != want {
		t.Errorf("playlist content = %q, want %q", got, want)
	}

	parentInfo, err := os.Stat(filepath.Dir(outputPath))
	if err != nil {
		t.Fatalf("stat playlist directory: %v", err)
	}
	if parentInfo.Mode().Perm()&0o007 != 0 {
		t.Errorf("playlist directory permissions = %o, want no world access", parentInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat playlist: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o640 {
		t.Errorf("playlist permissions = %o, want 640", got)
	}
}

func TestCreateM3UPlaylistAtomicallyReplacesExistingContent(t *testing.T) {
	libraryRoot := filepath.Join(t.TempDir(), "music")
	outputPath := filepath.Join(libraryRoot, "playlists", "existing.m3u")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		t.Fatalf("create playlist directory: %v", err)
	}

	if err := os.WriteFile(outputPath, []byte("stale\ncontent\n"), 0o640); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	path := filepath.Join(libraryRoot, "downloads", "current.flac")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := CreateM3UPlaylist(
			[]string{path},
			libraryRoot,
			outputPath,
		); err != nil {
			t.Fatalf("CreateM3UPlaylist attempt %d failed: %v", attempt+1, err)
		}
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read replaced playlist: %v", err)
	}
	want := filepath.ToSlash(path) + "\n"
	if got := string(content); got != want {
		t.Errorf("playlist content = %q, want %q", got, want)
	}

	entries, err := os.ReadDir(filepath.Dir(outputPath))
	if err != nil {
		t.Fatalf("read playlist directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(outputPath) {
		t.Errorf("playlist directory contains temporary files: %#v", entries)
	}
}

func TestCreateM3UPlaylistEmptyPathsReplacesWithEmptyFile(t *testing.T) {
	libraryRoot := filepath.Join(t.TempDir(), "music")
	outputPath := filepath.Join(libraryRoot, "playlists", "empty.m3u")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		t.Fatalf("create playlist directory: %v", err)
	}
	if err := os.WriteFile(outputPath, []byte("old content\n"), 0o640); err != nil {
		t.Fatalf("create old playlist: %v", err)
	}

	err := CreateM3UPlaylist([]string{}, libraryRoot, outputPath)
	if err != nil {
		t.Fatalf("CreateM3UPlaylist failed for empty paths: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(content))
	}
}

func TestCreateM3UPlaylistRejectsEscapingPathBeforeReplacing(t *testing.T) {
	base := t.TempDir()
	libraryRoot := filepath.Join(base, "music")
	outputPath := filepath.Join(libraryRoot, "playlists", "safe.m3u")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		t.Fatalf("create playlist directory: %v", err)
	}
	const original = "known-good\n"
	if err := os.WriteFile(outputPath, []byte(original), 0o640); err != nil {
		t.Fatalf("create existing playlist: %v", err)
	}

	unsafePaths := []string{
		filepath.Join(base, "music-other", "song.flac"),
		filepath.Join("..", "outside.flac"),
	}
	for _, unsafePath := range unsafePaths {
		err := CreateM3UPlaylist(
			[]string{unsafePath},
			libraryRoot,
			outputPath,
		)
		if !errors.Is(err, ErrPathOutsideLibrary) {
			t.Errorf(
				"CreateM3UPlaylist(%q) error = %v, want ErrPathOutsideLibrary",
				unsafePath,
				err,
			)
		}
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read original playlist: %v", err)
	}
	if got := string(content); got != original {
		t.Errorf("unsafe update changed playlist to %q", got)
	}
}

func TestCreateM3UPlaylistRejectsLineInjection(t *testing.T) {
	libraryRoot := filepath.Join(t.TempDir(), "music")
	outputPath := filepath.Join(libraryRoot, "playlists", "safe.m3u")
	err := CreateM3UPlaylist(
		[]string{"downloads/song.flac\n/malicious.flac"},
		libraryRoot,
		outputPath,
	)
	if err == nil {
		t.Fatal("CreateM3UPlaylist accepted a path containing a newline")
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("unsafe update created output; stat error = %v", statErr)
	}
}

func TestCreateM3UPlaylistRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	libraryRoot := filepath.Join(base, "music")
	outsideRoot := filepath.Join(base, "outside")
	if err := os.MkdirAll(libraryRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideRoot, "song.flac")
	if err := os.WriteFile(outsideFile, []byte("audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRoot, filepath.Join(libraryRoot, "escaped")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	err := CreateM3UPlaylist(
		[]string{filepath.Join(libraryRoot, "escaped", "song.flac")},
		libraryRoot,
		filepath.Join(libraryRoot, "playlists", "safe.m3u"),
	)
	if !errors.Is(err, ErrPathOutsideLibrary) {
		t.Fatalf("CreateM3UPlaylist() error = %v, want ErrPathOutsideLibrary", err)
	}
}

func TestSanitizePlaylistName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "Road Trip", want: "Road Trip"},
		{name: "separators", input: `AC/DC \ Live`, want: "AC-DC-Live"},
		{name: "traversal", input: "../../private", want: "private"},
		{name: "control", input: "Morning\nMix\t2026", want: "Morning-Mix-2026"},
		{name: "reserved", input: "CON", want: "_CON"},
		{name: "empty", input: `<>:"/\|?*`, want: "playlist"},
		{name: "unicode", input: "Česká hudba 🎵", want: "Česká hudba 🎵"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SanitizePlaylistName(test.input); got != test.want {
				t.Errorf("SanitizePlaylistName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestSanitizePlaylistNameLimitsUTF8Length(t *testing.T) {
	name := strings.Repeat("🎵", 100)
	got := SanitizePlaylistName(name)
	if len(got) > maxPlaylistNameBytes {
		t.Fatalf("sanitized name has %d bytes, want at most %d", len(got), maxPlaylistNameBytes)
	}
	if !strings.HasPrefix(name, got) {
		t.Errorf("sanitized Unicode name was corrupted: %q", got)
	}
}

func TestFindUnindexedSongs(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	musicDir := filepath.Join(tmpDir, "music")
	if err := os.MkdirAll(musicDir, 0755); err != nil {
		t.Fatalf("failed to create music dir: %v", err)
	}

	// Create test music files
	testFiles := []string{
		"artist1 - song1.flac",
		"artist2 - song2.flac",
	}
	for _, f := range testFiles {
		path := filepath.Join(musicDir, f)
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	outputPath := filepath.Join(tmpDir, "output.m3u")
	songList := []string{"Artist1 - Song1", "Artist2 - Song2", "Artist3 - Song3"}

	matched, err := FindUnindexedSongs(songList, musicDir, outputPath)
	if err != nil {
		t.Fatalf("FindUnindexedSongs failed: %v", err)
	}

	// Should find 2 of 3 songs
	if len(matched) != 2 {
		t.Errorf("expected 2 matched songs, got %d", len(matched))
	}
	want := []string{
		filepath.ToSlash(filepath.Join(musicDir, testFiles[0])),
		filepath.ToSlash(filepath.Join(musicDir, testFiles[1])),
	}
	for i := range want {
		if matched[i] != want[i] {
			t.Errorf("matched[%d] = %q, want %q", i, matched[i], want[i])
		}
	}
}

func TestFindUnindexedSongs_InvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "output.m3u")

	// Songs without proper format should be skipped
	songList := []string{"InvalidSongWithoutDash", "Another Invalid"}

	matched, err := FindUnindexedSongs(songList, tmpDir, outputPath)
	if err != nil {
		t.Fatalf("FindUnindexedSongs failed: %v", err)
	}

	if len(matched) != 0 {
		t.Errorf("expected 0 matched songs for invalid format, got %d", len(matched))
	}
}

func TestPlaylistTrack_Fields(t *testing.T) {
	track := PlaylistTrack{
		Name:      "Test Song",
		Artist:    "Test Artist",
		AlbumName: "Test Album",
		Duration:  180,
	}

	if track.Name != "Test Song" {
		t.Errorf("expected Name 'Test Song', got '%s'", track.Name)
	}
	if track.Duration != 180 {
		t.Errorf("expected Duration 180, got %d", track.Duration)
	}
}
