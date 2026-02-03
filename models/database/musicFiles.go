package database

import (
	"context"

	"github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error) {
	orPairs := make([]bson.M, 0, len(artists))
	for i := range artists {
		orPairs = append(orPairs, bson.M{
			"artist": artists[i],
			"title":  titles[i],
		})
	}

	d.log.Info("Finding music files", zap.Any("orPairs", orPairs))

	cur, err := d.musicFilesCollection().Find(ctx, bson.M{
		"$or": orPairs,
	}, options.Find().SetProjection(bson.M{"meta_data": 0}))
	if err != nil {
		return nil, err
	}

	defer cur.Close(ctx)

	files := make([]models.MusicFile, 0)
	for cur.Next(ctx) {
		var file models.MusicFile
		if err := cur.Decode(&file); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return files, nil
}

func (d *db) DropMusicFiles(ctx context.Context, areYouSure bool) error {
	if !areYouSure {
		return ErrNotSure
	}
	_, err := d.musicFilesCollection().DeleteMany(ctx, bson.M{})
	if err != nil {
		return err
	}

	return nil
}
