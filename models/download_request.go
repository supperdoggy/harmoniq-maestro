package models

import "github.com/supperdoggy/spot-models/spotify"

type DownloadQueueRequest struct {
	ID        string `json:"id" bson:"_id"`
	CreatorID int64  `json:"creator_id" bson:"creator_id"`

	SpotifyURL string                    `json:"spotify_url" bson:"spotify_url"`
	ObjectType spotify.SpotifyObjectType `json:"object_type" bson:"object_type"`
	Name       string                    `json:"name" bson:"name"`
	Active     bool                      `json:"active" bson:"active"`
	Errored    bool                      `json:"errored" bson:"errored"`

	CreatedAt  int64 `json:"created_at" bson:"created_at"`
	UpdatedAt  int64 `json:"updated_at" bson:"updated_at"`
	SyncCount  int   `json:"sync_count" bson:"sync_count"`
	RetryCount int   `json:"retry_count" bson:"retry_count"`

	// Track tracking fields
	ExpectedTrackCount int                     `json:"expected_track_count" bson:"expected_track_count"`
	FoundTrackCount    int                     `json:"found_track_count" bson:"found_track_count"`
	TrackMetadata      []spotify.TrackMetadata `json:"track_metadata" bson:"track_metadata"`
}

type PlaylistRequest struct {
	ID         string `json:"id" bson:"_id"`
	CreatorID  int64  `json:"creator_id" bson:"creator_id"`
	SpotifyURL string `json:"spotify_url" bson:"spotify_url"`

	Active     bool `json:"active" bson:"active"`
	Errored    bool `json:"errored" bson:"errored"`
	RetryCount int  `json:"retry_count" bson:"retry_count"`
	// NoPull indicates that the playlist missing songs should not be pulled from Spotify
	NoPull bool `json:"no_pull" bson:"no_pull"`

	CreatedAt int64 `json:"created_at" bson:"created_at"`
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`
}
