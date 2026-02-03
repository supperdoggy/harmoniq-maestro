package database

import (
	"context"
	"time"

	"github.com/supperdoggy/spot-models"
	"github.com/gofrs/uuid"
	"gopkg.in/mgo.v2/bson"
)

// IndexMusicFile indexes a music file in the database
func (d *db) IndexMusicFile(ctx context.Context, file models.MusicFile) error {
	file.ID = uuid.Must(uuid.NewV4()).String()
	file.CreatedAt = time.Now().Unix()
	_, err := d.musicFilesCollection().InsertOne(ctx, file)
	return err
}

func (d *db) GetIndexStatus(ctx context.Context) (models.IndexStatus, error) {
	var status models.IndexStatus
	err := d.indexStatusCollection().FindOne(ctx, bson.M{}).Decode(&status)
	if err != nil {
		return models.IndexStatus{}, err
	}

	return status, nil
}

func (d *db) UpdateIndexStatus(ctx context.Context, status models.IndexStatus) error {
	_, err := d.indexStatusCollection().UpdateOne(ctx, bson.M{}, bson.M{
		"$set": status,
	})
	if err != nil {
		return err
	}

	return nil
}
