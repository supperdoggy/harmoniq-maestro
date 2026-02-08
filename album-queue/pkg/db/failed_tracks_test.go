package db

import (
	"context"
	"errors"
	"testing"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestFilterUnresolvedFailedTracksExcludesIndexedTracks(t *testing.T) {
	requests := []models.DownloadQueueRequest{
		{
			ID:        "r1",
			UpdatedAt: 10,
			TrackMetadata: []spotify.TrackMetadata{
				{SpotifyURL: "https://open.spotify.com/track/one", Artist: "a", Title: "x", Found: false, FailedAttempts: 3},
			},
		},
	}

	tracks, err := filterUnresolvedFailedTracks(
		context.Background(),
		requests,
		func(context.Context, string, string) (bool, error) { return true, nil },
		func(context.Context, string) (bool, error) { return false, nil },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("expected no unresolved tracks, got %d", len(tracks))
	}
}

func TestFilterUnresolvedFailedTracksExcludesActiveDuplicates(t *testing.T) {
	requests := []models.DownloadQueueRequest{
		{
			ID:        "r1",
			UpdatedAt: 10,
			TrackMetadata: []spotify.TrackMetadata{
				{SpotifyURL: "https://open.spotify.com/track/one", Artist: "a", Title: "x", Found: false, FailedAttempts: 3},
			},
		},
	}

	tracks, err := filterUnresolvedFailedTracks(
		context.Background(),
		requests,
		func(context.Context, string, string) (bool, error) { return false, nil },
		func(context.Context, string) (bool, error) { return true, nil },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("expected no unresolved tracks, got %d", len(tracks))
	}
}

func TestFilterUnresolvedFailedTracksDedupesByURL(t *testing.T) {
	url := "https://open.spotify.com/track/retry"
	requests := []models.DownloadQueueRequest{
		{
			ID:        "r1",
			UpdatedAt: 10,
			TrackMetadata: []spotify.TrackMetadata{
				{SpotifyURL: url, Artist: "a1", Title: "x1", Found: false, FailedAttempts: 3},
			},
		},
		{
			ID:        "r2",
			UpdatedAt: 20,
			TrackMetadata: []spotify.TrackMetadata{
				{SpotifyURL: url, Artist: "a2", Title: "x2", Found: false, FailedAttempts: 5},
			},
		},
	}

	tracks, err := filterUnresolvedFailedTracks(
		context.Background(),
		requests,
		func(context.Context, string, string) (bool, error) { return false, nil },
		func(context.Context, string) (bool, error) { return false, nil },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected one deduped track, got %d", len(tracks))
	}
	if tracks[0].SourceCount != 2 {
		t.Fatalf("expected source_count=2, got %d", tracks[0].SourceCount)
	}
	if tracks[0].FailedAttempts != 5 {
		t.Fatalf("expected max failed_attempts=5, got %d", tracks[0].FailedAttempts)
	}
	if tracks[0].LastSeenAt != 20 {
		t.Fatalf("expected last_seen_at=20, got %d", tracks[0].LastSeenAt)
	}
	if tracks[0].Artist != "a2" || tracks[0].Title != "x2" {
		t.Fatalf("expected latest artist/title from most recent request, got %+v", tracks[0])
	}
}

func TestSelectFailedTrackByURLReturnsNotFoundForResolvedOrMissing(t *testing.T) {
	tracks := []FailedTrack{{SpotifyURL: "https://open.spotify.com/track/exists"}}

	_, err := selectFailedTrackByURL(tracks, "https://open.spotify.com/track/missing")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("expected mongo.ErrNoDocuments, got %v", err)
	}
}
