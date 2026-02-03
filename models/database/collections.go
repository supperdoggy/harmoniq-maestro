package database

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// downloadQueueRequestCollection returns the download queue request collection
func (d *db) downloadQueueRequestCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}
	return d.conn.Database(d.cfg.DatabaseName).Collection(d.cfg.DownloadRequestCollectionName)
}

func (d *db) playlistsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}

	return d.conn.Database(d.cfg.DatabaseName).Collection(d.cfg.PlaylistRequestCollectionName)
}

func (d *db) indexStatusCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}

	return d.conn.Database(d.cfg.DatabaseName).Collection(d.cfg.IndexStatusCollectionName)
}

// musicFilesCollection returns the music files collection
func (d *db) musicFilesCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}

	return d.conn.Database(d.cfg.DatabaseName).Collection(d.cfg.MusicFilesCollectionName)
}

func (d *db) dynamicPlaylistsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}

	return d.conn.Database(d.cfg.DatabaseName).Collection("dynamic_playlists")
}

func (d *db) genreMappingsCollection() *mongo.Collection {
	if err := d.conn.Ping(context.Background(), nil); err != nil {
		d.log.Error("failed to ping database. reconnecting.", zap.Error(err))
		if reconnectErr := d.reconnectToDB(); reconnectErr != nil {
			d.log.Error("failed to reconnect to database", zap.Error(reconnectErr))
		}
	}

	return d.conn.Database(d.cfg.DatabaseName).Collection("genre_mappings")
}
