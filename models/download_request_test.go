package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDownloadQueueRequest_JSON(t *testing.T) {
	req := DownloadQueueRequest{
		ID:         "test-id-123",
		CreatorID:  12345,
		SpotifyURL: "https://open.spotify.com/album/test",
		Name:       "Test Album",
		Active:     true,
		Errored:    false,
		CreatedAt:  time.Now().Unix(),
		UpdatedAt:  time.Now().Unix(),
		SyncCount:  0,
		RetryCount: 0,
	}

	// Test JSON marshaling
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Test JSON unmarshaling
	var decoded DownloadQueueRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != req.ID {
		t.Errorf("ID mismatch: got %s, want %s", decoded.ID, req.ID)
	}
	if decoded.SpotifyURL != req.SpotifyURL {
		t.Errorf("SpotifyURL mismatch: got %s, want %s", decoded.SpotifyURL, req.SpotifyURL)
	}
	if decoded.Active != req.Active {
		t.Errorf("Active mismatch: got %v, want %v", decoded.Active, req.Active)
	}
}

func TestPlaylistRequest_JSON(t *testing.T) {
	req := PlaylistRequest{
		ID:         "playlist-id-456",
		CreatorID:  67890,
		SpotifyURL: "https://open.spotify.com/playlist/test",
		Active:     true,
		Errored:    false,
		NoPull:     true,
		CreatedAt:  time.Now().Unix(),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded PlaylistRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.NoPull != req.NoPull {
		t.Errorf("NoPull mismatch: got %v, want %v", decoded.NoPull, req.NoPull)
	}
}
