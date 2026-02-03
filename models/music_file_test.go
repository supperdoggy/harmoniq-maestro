package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMusicFile_JSON(t *testing.T) {
	file := MusicFile{
		ID:     "file-id-789",
		Artist: "Test Artist",
		Album:  "Test Album",
		Title:  "Test Song",
		Genre:  "Electronic",
		Path:   "/music/test.flac",
		MetaData: map[string]any{
			"bitrate":     320,
			"duration":    180,
			"sample_rate": 44100,
		},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded MusicFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Artist != file.Artist {
		t.Errorf("Artist mismatch: got %s, want %s", decoded.Artist, file.Artist)
	}
	if decoded.Path != file.Path {
		t.Errorf("Path mismatch: got %s, want %s", decoded.Path, file.Path)
	}
	if decoded.MetaData["bitrate"] != float64(320) { // JSON numbers are float64
		t.Errorf("MetaData bitrate mismatch: got %v", decoded.MetaData["bitrate"])
	}
}

func TestIndexStatus_Fields(t *testing.T) {
	status := IndexStatus{
		ID:          "index-1",
		LastIndexed: time.Now().Unix(),
		LastUpdated: time.Now().Unix(),
	}

	if status.ID == "" {
		t.Error("ID should not be empty")
	}
	if status.LastIndexed == 0 {
		t.Error("LastIndexed should not be zero")
	}
}
