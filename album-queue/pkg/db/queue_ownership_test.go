package db

import (
	"reflect"
	"testing"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/bson"
)

func TestNewDownloadQueueRequest_StartsPending(t *testing.T) {
	metadata := []spotify.TrackMetadata{{
		SpotifyURL: "https://open.spotify.com/track/one",
		Artist:     "Artist",
		Title:      "Title",
	}}
	request := newDownloadQueueRequest(
		"request-1",
		"https://open.spotify.com/album/one",
		"Album",
		42,
		spotify.SpotifyObjectTypeAlbum,
		1,
		metadata,
		100,
	)

	if request.State != models.DownloadRequestStatePending {
		t.Fatalf("state = %q, want pending", request.State)
	}
	if !request.Active || request.Errored {
		t.Fatalf("legacy flags = active:%v errored:%v, want true/false", request.Active, request.Errored)
	}
	if request.WorkerID != "" || request.ClaimID != "" || request.LeaseExpiresAt != 0 {
		t.Fatalf("new request unexpectedly has lease ownership: %+v", request)
	}
	if request.CreatedAt != 100 || request.UpdatedAt != 100 {
		t.Fatalf("timestamps = %d/%d, want 100/100", request.CreatedAt, request.UpdatedAt)
	}
	if !reflect.DeepEqual(request.TrackMetadata, metadata) {
		t.Fatalf("track metadata = %#v, want %#v", request.TrackMetadata, metadata)
	}
}

func TestDeactivateRequestUpdate_CancelsAndRevokesLease(t *testing.T) {
	got := deactivateRequestUpdate(100)
	want := bson.M{
		"$set": bson.M{
			"active":     false,
			"errored":    false,
			"state":      models.DownloadRequestStateCancelled,
			"updated_at": int64(100),
		},
		"$unset": bson.M{
			"worker_id":        "",
			"claim_id":         "",
			"lease_expires_at": "",
			"next_attempt_at":  "",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deactivate update mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestDownloadProgressUpdate_DoesNotTouchWorkerOwnership(t *testing.T) {
	got := downloadProgressUpdate(7)
	want := bson.M{"$max": bson.M{"found_track_count": 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progress update mismatch:\n got: %#v\nwant: %#v", got, want)
	}

	encoded, err := bson.Marshal(got)
	if err != nil {
		t.Fatalf("marshal progress update: %v", err)
	}
	var update bson.M
	if err := bson.Unmarshal(encoded, &update); err != nil {
		t.Fatalf("unmarshal progress update: %v", err)
	}
	for _, field := range []string{
		"active",
		"errored",
		"state",
		"worker_id",
		"claim_id",
		"lease_expires_at",
		"next_attempt_at",
		"track_metadata",
		"updated_at",
	} {
		if containsDocumentKey(update, field) {
			t.Errorf("progress update unexpectedly writes worker-owned field %q", field)
		}
	}
}

func containsDocumentKey(value any, key string) bool {
	switch typed := value.(type) {
	case bson.M:
		for childKey, child := range typed {
			if childKey == key || containsDocumentKey(child, key) {
				return true
			}
		}
	case bson.A:
		for _, child := range typed {
			if containsDocumentKey(child, key) {
				return true
			}
		}
	}
	return false
}
