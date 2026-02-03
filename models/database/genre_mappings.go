package database

import (
	"context"

	"github.com/supperdoggy/SmartHomeServer/music-services/models"
	"go.uber.org/zap"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) GetAllGenreMappings(ctx context.Context) ([]models.GenreMapping, error) {
	cur, err := d.genreMappingsCollection().Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	mappings := make([]models.GenreMapping, 0)
	for cur.Next(ctx) {
		var mapping models.GenreMapping
		if err := cur.Decode(&mapping); err != nil {
			d.log.Error("failed to decode genre mapping", zap.Error(err))
			continue
		}
		mappings = append(mappings, mapping)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return mappings, nil
}

func (d *db) CreateGenreMapping(ctx context.Context, mapping models.GenreMapping) error {
	_, err := d.genreMappingsCollection().InsertOne(ctx, mapping)
	return err
}

func (d *db) GetGenreMappingBySpecific(ctx context.Context, specificGenre string) (models.GenreMapping, error) {
	var mapping models.GenreMapping
	err := d.genreMappingsCollection().FindOne(ctx, bson.M{"specific_genre": specificGenre}).Decode(&mapping)
	return mapping, err
}
