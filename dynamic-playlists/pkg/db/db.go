package db

import (
	"context"

	"github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type Database interface {
	GetActiveDynamicPlaylists(ctx context.Context) ([]models.DynamicPlaylist, error)
	GetAllDynamicPlaylists(ctx context.Context) ([]models.DynamicPlaylist, error)
	UpdateDynamicPlaylist(ctx context.Context, playlist models.DynamicPlaylist) error
	FindMusicFilesByQuery(ctx context.Context, query bson.M, sortBy string, limit int) ([]models.MusicFile, error)
	GetAllGenreMappings(ctx context.Context) ([]models.GenreMapping, error)
	Close(ctx context.Context) error
	Ping(ctx context.Context) error
}

type db struct {
	conn   *mongo.Client
	log    *zap.Logger
	dbname string
	url    string
}

func NewDatabase(ctx context.Context, log *zap.Logger, url, dbname string) (Database, error) {
	conn, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		return nil, err
	}

	return &db{
		conn:   conn,
		log:    log,
		dbname: dbname,
		url:    url,
	}, nil
}

func (d *db) reconnectToDB() error {
	if err := d.conn.Disconnect(context.Background()); err != nil {
		d.log.Warn("error disconnecting from database", zap.Error(err))
	}

	conn, err := mongo.Connect(context.Background(), options.Client().ApplyURI(d.url))
	if err != nil {
		return err
	}

	d.conn = conn
	return nil
}

func (d *db) dynamicPlaylistsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.dbname).Collection("dynamic_playlists")
}

func (d *db) genreMappingsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.dbname).Collection("genre_mappings")
}

func (d *db) musicFilesCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.dbname).Collection("music-files")
}

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

func (d *db) UpdateDynamicPlaylist(ctx context.Context, playlist models.DynamicPlaylist) error {
	_, err := d.dynamicPlaylistsCollection().UpdateOne(
		ctx,
		bson.M{"_id": playlist.ID},
		bson.M{"$set": playlist},
	)
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

func (d *db) Close(ctx context.Context) error {
	return d.conn.Disconnect(ctx)
}

func (d *db) Ping(ctx context.Context) error {
	return d.conn.Ping(ctx, nil)
}
