package models

type MusicFile struct {
	ID string `json:"id" bson:"_id"`

	Artist string `json:"artist" bson:"artist"`
	Album  string `json:"album" bson:"album"`
	Title  string `json:"title" bson:"title"`
	Genre  string `json:"genre" bson:"genre"`

	SpotifyID  string `json:"spotify_id,omitempty" bson:"spotify_id,omitempty"`
	SpotifyURL string `json:"spotify_url,omitempty" bson:"spotify_url,omitempty"`
	ISRC       string `json:"isrc,omitempty" bson:"isrc,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty" bson:"duration_ms,omitempty"`

	SourceProvider string  `json:"source_provider,omitempty" bson:"source_provider,omitempty"`
	SourceID       string  `json:"source_id,omitempty" bson:"source_id,omitempty"`
	MatchScore     float64 `json:"match_score,omitempty" bson:"match_score,omitempty"`
	Checksum       string  `json:"checksum,omitempty" bson:"checksum,omitempty"`
	Format         string  `json:"format,omitempty" bson:"format,omitempty"`

	Path     string         `json:"path" bson:"path"`
	MetaData map[string]any `json:"meta_data" bson:"meta_data"`

	CreatedAt int64 `json:"created_at" bson:"created_at"`
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`
}
