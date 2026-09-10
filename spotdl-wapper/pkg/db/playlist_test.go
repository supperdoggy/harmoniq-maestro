package db

import (
	"reflect"
	"testing"

	models "github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
)

func TestActivePlaylistFilterIncludesLegacyAndDueRequests(t *testing.T) {
	const now int64 = 1_000
	want := bson.M{
		"active": true,
		"$or": bson.A{
			bson.M{"next_attempt_at": bson.M{"$exists": false}},
			bson.M{"next_attempt_at": nil},
			bson.M{"next_attempt_at": bson.M{"$lte": now}},
		},
	}

	if got := activePlaylistFilter(now); !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist eligibility filter mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPlaylistSortIsDeterministic(t *testing.T) {
	want := bson.D{
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	}
	if got := playlistSort(); !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist sort = %#v, want %#v", got, want)
	}
}

func TestPlaylistRequestUpdateSetsRetryMetadata(t *testing.T) {
	lastError := &models.DownloadRequestError{
		Code:       "spotify_rate_limited",
		Stage:      "playlist",
		Message:    "rate limited",
		Retryable:  true,
		OccurredAt: 900,
	}
	request := models.PlaylistRequest{
		Name:          "Focus",
		Active:        true,
		Errored:       true,
		RetryCount:    2,
		NextAttemptAt: 2_000,
		LastError:     lastError,
	}

	got := playlistRequestUpdate(request, 1_000)
	want := bson.M{
		"$set": bson.M{
			"name":            "Focus",
			"active":          true,
			"errored":         true,
			"retry_count":     2,
			"updated_at":      int64(1_000),
			"next_attempt_at": int64(2_000),
			"last_error":      lastError,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist retry update mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPlaylistRequestUpdateClearsOptionalMetadata(t *testing.T) {
	request := models.PlaylistRequest{
		Active:     false,
		RetryCount: 5,
	}

	got := playlistRequestUpdate(request, 1_000)
	want := bson.M{
		"$set": bson.M{
			"active":      false,
			"errored":     false,
			"retry_count": 5,
			"updated_at":  int64(1_000),
		},
		"$unset": bson.M{
			"name":            "",
			"next_attempt_at": "",
			"last_error":      "",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("playlist clearing update mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPlaylistRetryIndexContract(t *testing.T) {
	index := playlistRetryIndex()
	wantKeys := bson.D{
		{Key: "active", Value: 1},
		{Key: "next_attempt_at", Value: 1},
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	}
	if !reflect.DeepEqual(index.Keys, wantKeys) {
		t.Fatalf("playlist retry index keys = %#v, want %#v", index.Keys, wantKeys)
	}
	if index.Options == nil || index.Options.Name == nil || *index.Options.Name != "playlist_retry_eligibility_v1" {
		t.Fatalf("playlist retry index name = %#v", index.Options)
	}
	if index.Options.Sparse != nil && *index.Options.Sparse {
		t.Fatal("playlist retry index must not be sparse because legacy rows have no schedule")
	}
	if index.Options.Unique != nil && *index.Options.Unique {
		t.Fatal("playlist retry index must not be unique")
	}
}
