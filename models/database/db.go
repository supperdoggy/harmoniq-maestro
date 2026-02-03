package database

import (
	"context"

	"github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type Database interface {
	GetActiveRequests(ctx context.Context) ([]models.DownloadQueueRequest, error)
	GetActiveRequest(ctx context.Context, url string) (models.DownloadQueueRequest, error)
	CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error)
	NewDownloadRequest(ctx context.Context, url, name string, creatorID int64) error
	UpdateActiveRequest(ctx context.Context, request models.DownloadQueueRequest) error

	GetActivePlaylists(ctx context.Context) ([]models.PlaylistRequest, error)
	UpdatePlaylistRequest(ctx context.Context, request models.PlaylistRequest) error
	NewPlaylistRequest(ctx context.Context, url string, creatorID int64) error

	FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error)
	IndexMusicFile(ctx context.Context, file models.MusicFile) error

	GetIndexStatus(ctx context.Context) (models.IndexStatus, error)
	UpdateIndexStatus(ctx context.Context, status models.IndexStatus) error
}

type db struct {
	conn *mongo.Client
	log  *zap.Logger

	cfg *DataBaseConfig
}

func NewDatabase(ctx context.Context, log *zap.Logger, cfg *DataBaseConfig) (Database, error) {
	conn, err := mongo.Connect(context.Background(), options.Client().ApplyURI(cfg.DatabaseURL))
	if err != nil {
		return nil, err
	}

	return &db{
		conn: conn,
		log:  log,

		cfg: cfg,
	}, nil
}

func (d *db) reconnectToDB() error {
	if err := d.conn.Disconnect(context.Background()); err != nil {
		d.log.Warn("error disconnecting from database", zap.Error(err))
	}

	conn, err := mongo.Connect(context.Background(), options.Client().ApplyURI(d.cfg.DatabaseURL))
	if err != nil {
		return err
	}

	d.conn = conn
	return nil
}
