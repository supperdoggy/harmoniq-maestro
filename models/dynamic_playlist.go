package models

import "go.mongodb.org/mongo-driver/bson"

type DynamicPlaylist struct {
	ID               string `json:"id" bson:"_id"`
	Name             string `json:"name" bson:"name"`                           // Generated or manual
	Description      string `json:"description" bson:"description"`             // OpenAI-generated
	Query            bson.M `json:"query" bson:"query"`                         // MongoDB filter query
	RefreshFrequency string `json:"refresh_frequency" bson:"refresh_frequency"` // "daily", "weekly"
	MaxTracks        int    `json:"max_tracks" bson:"max_tracks"`               // Limit tracks
	SortBy           string `json:"sort_by" bson:"sort_by"`                     // e.g., "created_at", "random"
	Active           bool   `json:"active" bson:"active"`
	OutputPath       string `json:"output_path" bson:"output_path"` // M3U file path
	LastRefreshed    int64  `json:"last_refreshed" bson:"last_refreshed"`
	TrackCount       int    `json:"track_count" bson:"track_count"` // Current track count
	CreatedAt        int64  `json:"created_at" bson:"created_at"`
	UpdatedAt        int64  `json:"updated_at" bson:"updated_at"`
}
