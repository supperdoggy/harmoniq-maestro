package database

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/models"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) GetActiveSubscribedPlaylists(ctx context.Context) ([]models.SubscribedPlaylist, error) {
	var playlists []models.SubscribedPlaylist
	cursor, err := d.subscribedPlaylistsCollection().Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var playlist models.SubscribedPlaylist
		if err := cursor.Decode(&playlist); err != nil {
			return nil, err
		}
		playlists = append(playlists, playlist)
	}
	return playlists, nil
}

func (d *db) GetSubscribedPlaylistsByCreator(ctx context.Context, creatorID int64) ([]models.SubscribedPlaylist, error) {
	var playlists []models.SubscribedPlaylist
	cursor, err := d.subscribedPlaylistsCollection().Find(ctx, bson.M{"creator_id": creatorID, "active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var playlist models.SubscribedPlaylist
		if err := cursor.Decode(&playlist); err != nil {
			return nil, err
		}
		playlists = append(playlists, playlist)
	}
	return playlists, nil
}

func (d *db) NewSubscribedPlaylist(ctx context.Context, url string, creatorID int64, name string, refreshInterval string, noPull bool) error {
	id, _ := uuid.NewV4()
	playlist := models.SubscribedPlaylist{
		ID:              id.String(),
		CreatorID:       creatorID,
		SpotifyURL:      url,
		Name:            name,
		Active:          true,
		RefreshInterval: refreshInterval,
		NoPull:          noPull,
		LastSynced:      0,
		LastTrackCount:  0,
		CreatedAt:       time.Now().Unix(),
		UpdatedAt:       time.Now().Unix(),
	}

	_, err := d.subscribedPlaylistsCollection().InsertOne(ctx, playlist)
	if err != nil {
		return err
	}

	return nil
}

func (d *db) UpdateSubscribedPlaylist(ctx context.Context, playlist models.SubscribedPlaylist) error {
	info, err := d.subscribedPlaylistsCollection().UpdateOne(ctx, bson.M{"_id": playlist.ID}, bson.M{"$set": bson.M{
		"active":           playlist.Active,
		"last_synced":      playlist.LastSynced,
		"last_track_count": playlist.LastTrackCount,
		"output_path":      playlist.OutputPath,
		"updated_at":       playlist.UpdatedAt,
	}})

	if info.MatchedCount == 0 {
		return errors.New("not found")
	}

	return err
}

func (d *db) DeleteSubscribedPlaylist(ctx context.Context, url string, creatorID int64) error {
	result, err := d.subscribedPlaylistsCollection().UpdateOne(
		ctx,
		bson.M{"spotify_url": url, "creator_id": creatorID},
		bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().Unix()}},
	)
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		return errors.New("subscription not found")
	}

	return nil
}

func (d *db) CheckSubscriptionExists(ctx context.Context, url string, creatorID int64) (bool, error) {
	count, err := d.subscribedPlaylistsCollection().CountDocuments(ctx, bson.M{
		"spotify_url": url,
		"creator_id":  creatorID,
		"active":      true,
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
