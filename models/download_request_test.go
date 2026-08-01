package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
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

func TestDownloadQueueRequest_LegacyDocumentCompatibility(t *testing.T) {
	req := DownloadQueueRequest{
		ID:         "legacy",
		SpotifyURL: "https://open.spotify.com/track/legacy",
		Active:     true,
		CreatedAt:  123,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal legacy JSON: %v", err)
	}
	for _, field := range []string{
		`"state"`,
		`"worker_id"`,
		`"claim_id"`,
		`"lease_expires_at"`,
		`"next_attempt_at"`,
		`"backend"`,
		`"last_error"`,
		`"result"`,
	} {
		if strings.Contains(string(jsonData), field) {
			t.Errorf("zero-value transitional field %s unexpectedly written to legacy JSON: %s", field, jsonData)
		}
	}

	bsonData, err := bson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal legacy BSON: %v", err)
	}
	var document bson.M
	if err := bson.Unmarshal(bsonData, &document); err != nil {
		t.Fatalf("decode legacy BSON: %v", err)
	}
	for _, field := range []string{
		"state",
		"worker_id",
		"claim_id",
		"lease_expires_at",
		"next_attempt_at",
		"backend",
		"last_error",
		"result",
	} {
		if _, exists := document[field]; exists {
			t.Errorf("zero-value transitional field %q unexpectedly written to legacy BSON", field)
		}
	}

	var decoded DownloadQueueRequest
	if err := bson.Unmarshal(bsonData, &decoded); err != nil {
		t.Fatalf("unmarshal legacy BSON: %v", err)
	}
	if decoded.State != "" ||
		decoded.WorkerID != "" ||
		decoded.ClaimID != "" ||
		decoded.LeaseExpiresAt != 0 {
		t.Fatalf("legacy document acquired unexpected lease state: %+v", decoded)
	}
}

func TestDownloadQueueRequest_StateMetadataRoundTrip(t *testing.T) {
	req := DownloadQueueRequest{
		ID:             "request-with-state",
		Active:         true,
		State:          DownloadRequestStateValidating,
		WorkerID:       "worker-1",
		ClaimID:        "claim-1",
		LeaseExpiresAt: 200,
		NextAttemptAt:  150,
		Backend:        "yt-dlp",
		LastError: &DownloadRequestError{
			Code:       "validation_failed",
			Stage:      "validating",
			Message:    "duration mismatch",
			Retryable:  false,
			OccurredAt: 100,
			Details:    map[string]string{"expected_ms": "1000"},
		},
		Result: &DownloadRequestResult{
			Provider:   "youtube",
			SourceID:   "source-1",
			SourceURL:  "https://www.youtube.com/watch?v=source-1",
			FinalPath:  "/music/song.opus",
			Format:     "opus",
			Checksum:   "sha256:abc",
			MatchScore: 0.98,
			CatalogID:  "catalog-1",
			ImportedAt: 110,
		},
	}

	data, err := bson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded DownloadQueueRequest
	if err := bson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !reflect.DeepEqual(decoded, req) {
		t.Fatalf("state metadata mismatch:\n got: %#v\nwant: %#v", decoded, req)
	}
}

func TestDownloadRequestState_IsTerminal(t *testing.T) {
	tests := []struct {
		state DownloadRequestState
		want  bool
	}{
		{DownloadRequestStatePending, false},
		{DownloadRequestStateClaimed, false},
		{DownloadRequestStateNeedsReview, false},
		{DownloadRequestStateCompleted, true},
		{DownloadRequestStateFailed, true},
		{DownloadRequestStateCancelled, true},
	}

	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			if got := test.state.IsTerminal(); got != test.want {
				t.Fatalf("IsTerminal() = %v, want %v", got, test.want)
			}
		})
	}
}
