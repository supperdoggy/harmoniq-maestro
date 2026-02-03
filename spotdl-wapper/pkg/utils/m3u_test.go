package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateM3UPlaylist(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test.m3u")

	paths := []string{
		"/music/Artist1 - Song1.flac",
		"/music/Artist2 - Song2.flac",
		"/music/Artist3 - Song3.flac",
	}

	err := CreateM3UPlaylist(paths, tmpDir, outputPath)
	if err != nil {
		t.Fatalf("CreateM3UPlaylist failed: %v", err)
	}

	// Verify file was created
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	// Check content contains our paths
	contentStr := string(content)
	for _, path := range paths {
		if !contains(contentStr, path) {
			t.Errorf("expected path %s in playlist, not found", path)
		}
	}
}

func TestCreateM3UPlaylist_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "existing.m3u")

	// Create existing file
	if err := os.WriteFile(outputPath, []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	paths := []string{"/music/test.flac"}
	err := CreateM3UPlaylist(paths, tmpDir, outputPath)
	if err != os.ErrExist {
		t.Errorf("expected ErrExist, got %v", err)
	}
}

func TestCreateM3UPlaylist_EmptyPaths(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "empty.m3u")

	err := CreateM3UPlaylist([]string{}, tmpDir, outputPath)
	if err != nil {
		t.Fatalf("CreateM3UPlaylist failed for empty paths: %v", err)
	}

	// File should exist but be empty
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(content))
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

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
