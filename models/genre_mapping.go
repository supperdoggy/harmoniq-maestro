package models

type GenreMapping struct {
	ID              string `json:"id" bson:"_id"`
	SpecificGenre   string `json:"specific_genre" bson:"specific_genre"`     // e.g., "post-punk", "deep house"
	SimplifiedGenre string `json:"simplified_genre" bson:"simplified_genre"` // e.g., "rock", "electronic"
}
