package db

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/gofrs/uuid"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/models"
	"github.com/supperdoggy/spot-models/spotify"
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
	GetActiveSubscribedPlaylists(ctx context.Context) ([]models.SubscribedPlaylist, error)
	UpdateSubscribedPlaylist(ctx context.Context, playlist models.SubscribedPlaylist) error
	FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error)
	CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error)
	NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType) error
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

func (d *db) subscribedPlaylistsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.dbname).Collection("subscribed_playlists")
}

func (d *db) downloadQueueRequestCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.dbname).Collection("download-queue-requests")
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

func (d *db) GetActiveSubscribedPlaylists(ctx context.Context) ([]models.SubscribedPlaylist, error) {
	cur, err := d.subscribedPlaylistsCollection().Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	playlists := make([]models.SubscribedPlaylist, 0)
	for cur.Next(ctx) {
		var playlist models.SubscribedPlaylist
		if err := cur.Decode(&playlist); err != nil {
			d.log.Error("failed to decode subscribed playlist", zap.Error(err))
			continue
		}
		playlists = append(playlists, playlist)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	return playlists, nil
}

func (d *db) UpdateSubscribedPlaylist(ctx context.Context, playlist models.SubscribedPlaylist) error {
	_, err := d.subscribedPlaylistsCollection().UpdateOne(
		ctx,
		bson.M{"_id": playlist.ID},
		bson.M{"$set": playlist},
	)
	return err
}

func escapeRegex(s string) string {
	return regexp.QuoteMeta(s)
}

func (d *db) FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error) {
	if len(artists) != len(titles) {
		return nil, fmt.Errorf("artists and titles must have the same length")
	}

	orPairs := make([]bson.M, 0, len(artists))
	for i := range artists {
		// Use case-insensitive regex matching for both artist and title
		escapedArtist := escapeRegex(artists[i])
		escapedTitle := escapeRegex(titles[i])
		orPairs = append(orPairs, bson.M{
			"$and": []bson.M{
				{"artist": bson.M{"$regex": "^" + escapedArtist + "$", "$options": "i"}},
				{"title": bson.M{"$regex": "^" + escapedTitle + "$", "$options": "i"}},
			},
		})
	}

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

func (d *db) CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error) {
	count, err := d.downloadQueueRequestCollection().CountDocuments(ctx, bson.M{"spotify_url": url, "active": false})
	if err != nil && err != mongo.ErrNoDocuments {
		return false, err
	}
	return count > 0, nil
}

func (d *db) NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType) error {
	id, err := uuid.NewV4()
	if err != nil {
		return err
	}

	request := models.DownloadQueueRequest{
		SpotifyURL: url,
		ObjectType: objectType,
		Name:       name,
		Active:     true,
		ID:         id.String(),
		CreatedAt:  time.Now().Unix(),
		UpdatedAt:  time.Now().Unix(),
		CreatorID:  creatorID,
	}

	_, err = d.downloadQueueRequestCollection().InsertOne(ctx, request)
	if err != nil {
		return err
	}

	return nil
}
