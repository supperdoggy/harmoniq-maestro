package db

import (
	"errors"
	"testing"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestFilterResolvedFailedTracksExcludesIndexedTracks(t *testing.T) {
	track := FailedTrack{
		SpotifyURL: "https://open.spotify.com/track/one",
		Artist:     "Artist",
		Title:      "Title",
	}

	tracks := filterResolvedFailedTracks(
		[]FailedTrack{track},
		map[string]struct{}{},
		map[string]struct{}{failedTrackKey("artist", "title"): {}},
	)
	if len(tracks) != 0 {
		t.Fatalf("expected no unresolved tracks, got %d", len(tracks))
	}
}

func TestFilterResolvedFailedTracksExcludesRunnableDuplicates(t *testing.T) {
	trackURL := "https://open.spotify.com/track/one"
	track := FailedTrack{SpotifyURL: trackURL, Artist: "a", Title: "x"}

	tracks := filterResolvedFailedTracks(
		[]FailedTrack{track},
		map[string]struct{}{trackURL: {}},
		map[string]struct{}{},
	)
	if len(tracks) != 0 {
		t.Fatalf("expected no unresolved tracks, got %d", len(tracks))
	}
}

func TestCollectUnresolvedFailedTracksDedupesByURL(t *testing.T) {
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

	tracks := collectUnresolvedFailedTracks(requests)
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

func TestFilterResolvedFailedTracksSortsNewestFirst(t *testing.T) {
	tracks := filterResolvedFailedTracks(
		[]FailedTrack{
			{SpotifyURL: "https://open.spotify.com/track/older", LastSeenAt: 10},
			{SpotifyURL: "https://open.spotify.com/track/newer", LastSeenAt: 20},
		},
		map[string]struct{}{},
		map[string]struct{}{},
	)

	if len(tracks) != 2 || tracks[0].LastSeenAt != 20 || tracks[1].LastSeenAt != 10 {
		t.Fatalf("tracks are not sorted newest first: %+v", tracks)
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
