package models

// IndexStatus represents the status of an indexation process
type IndexStatus struct {
	ID          string `bson:"_id"`
	LastIndexed int64  `bson:"last_indexed"`
	LastUpdated int64  `bson:"last_updated"`
}
