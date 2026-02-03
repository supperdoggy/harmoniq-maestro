package database

import (
	"context"
	"errors"
	"time"

	"github.com/supperdoggy/spot-models"
	"github.com/gofrs/uuid"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) GetActivePlaylists(ctx context.Context) ([]models.PlaylistRequest, error) {
	var requests []models.PlaylistRequest
	cursor, err := d.playlistsCollection().Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var request models.PlaylistRequest
		if err := cursor.Decode(&request); err != nil {
			return nil, err
		}

		requests = append(requests, request)
	}
	return requests, nil
}

func (d *db) UpdatePlaylistRequest(ctx context.Context, request models.PlaylistRequest) error {
	info, err := d.playlistsCollection().UpdateOne(ctx, bson.M{"_id": request.ID}, bson.M{"$set": bson.M{
		"active":      request.Active,
		"errored":     request.Errored,
		"retry_count": request.RetryCount,
	}})

	if info.MatchedCount == 0 {
		return errors.New("not found")
	}

	return err
}

func (d *db) NewPlaylistRequest(ctx context.Context, url string, creatorID int64) error {
	id, _ := uuid.NewV4()
	request := models.PlaylistRequest{
		SpotifyURL: url,
		Active:     true,
		ID:         id.String(),
		CreatedAt:  time.Now().Unix(),
		CreatorID:  creatorID,
	}

	_, err := d.playlistsCollection().InsertOne(ctx, request)
	if err != nil {
		return err
	}

	return nil
}
