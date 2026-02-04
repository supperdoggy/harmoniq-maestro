package models

type SubscribedPlaylist struct {
	ID              string `json:"id" bson:"_id"`
	CreatorID       int64  `json:"creator_id" bson:"creator_id"`
	SpotifyURL      string `json:"spotify_url" bson:"spotify_url"`
	Name            string `json:"name" bson:"name"`
	Active          bool   `json:"active" bson:"active"`
	RefreshInterval string `json:"refresh_interval" bson:"refresh_interval"` // "hourly", "daily", "weekly"
	NoPull          bool   `json:"no_pull" bson:"no_pull"`
	LastSynced      int64  `json:"last_synced" bson:"last_synced"`
	LastTrackCount  int    `json:"last_track_count" bson:"last_track_count"`
	OutputPath      string `json:"output_path" bson:"output_path"`
	CreatedAt       int64  `json:"created_at" bson:"created_at"`
	UpdatedAt       int64  `json:"updated_at" bson:"updated_at"`
}
