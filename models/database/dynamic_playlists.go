package database

import (
	"context"

	"github.com/supperdoggy/SmartHomeServer/music-services/models"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) GetActiveDynamicPlaylists(ctx context.Context) ([]models.DynamicPlaylist, error) {
	cur, err := d.dynamicPlaylistsCollection().Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	playlists := make([]models.DynamicPlaylist, 0)
	for cur.Next(ctx) {
		var playlist models.DynamicPlaylist
		if err := cur.Decode(&playlist); err != nil {
			d.log.Error("failed to decode dynamic playlist", zap.Error(err))
			continue
		}
		playlists = append(playlists, playlist)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return playlists, nil
}

func (d *db) GetAllDynamicPlaylists(ctx context.Context) ([]models.DynamicPlaylist, error) {
	cur, err := d.dynamicPlaylistsCollection().Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	playlists := make([]models.DynamicPlaylist, 0)
	for cur.Next(ctx) {
		var playlist models.DynamicPlaylist
		if err := cur.Decode(&playlist); err != nil {
			d.log.Error("failed to decode dynamic playlist", zap.Error(err))
			continue
		}
		playlists = append(playlists, playlist)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return playlists, nil
}

func (d *db) GetDynamicPlaylist(ctx context.Context, id string) (models.DynamicPlaylist, error) {
	var playlist models.DynamicPlaylist
	err := d.dynamicPlaylistsCollection().FindOne(ctx, bson.M{"_id": id}).Decode(&playlist)
	return playlist, err
}

func (d *db) UpdateDynamicPlaylist(ctx context.Context, playlist models.DynamicPlaylist) error {
	_, err := d.dynamicPlaylistsCollection().UpdateOne(
		ctx,
		bson.M{"_id": playlist.ID},
		bson.M{"$set": playlist},
	)
	return err
}

func (d *db) CreateDynamicPlaylist(ctx context.Context, playlist models.DynamicPlaylist) error {
	_, err := d.dynamicPlaylistsCollection().InsertOne(ctx, playlist)
	return err
}

func (d *db) FindMusicFilesByQuery(ctx context.Context, query bson.M, sortBy string, limit int) ([]models.MusicFile, error) {
	findOptions := options.Find()
	findOptions.SetProjection(bson.M{"meta_data": 0})

	if limit > 0 {
		findOptions.SetLimit(int64(limit))
	}

	// Apply sorting
	if sortBy == "created_at" {
		findOptions.SetSort(bson.M{"created_at": -1})
	} else if sortBy == "random" {
		// For random, we'll need to handle it differently - fetch all and shuffle in Go
		// For now, just sort by _id which gives some randomness
		findOptions.SetSort(bson.M{"_id": 1})
	}

	cur, err := d.musicFilesCollection().Find(ctx, query, findOptions)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	files := make([]models.MusicFile, 0)
	for cur.Next(ctx) {
		var file models.MusicFile
		if err := cur.Decode(&file); err != nil {
			d.log.Error("failed to decode music file", zap.Error(err))
			continue
		}
		files = append(files, file)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return files, nil
}
