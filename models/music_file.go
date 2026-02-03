package models

type MusicFile struct {
	ID string `json:"id" bson:"_id"`

	Artist string `json:"artist" bson:"artist"`
	Album  string `json:"album" bson:"album"`
	Title  string `json:"title" bson:"title"`
	Genre  string `json:"genre" bson:"genre"`

	Path     string         `json:"path" bson:"path"`
	MetaData map[string]any `json:"meta_data" bson:"meta_data"`

	CreatedAt int64 `json:"created_at" bson:"created_at"`
	UpdatedAt int64 `json:"updated_at" bson:"updated_at"`
}
