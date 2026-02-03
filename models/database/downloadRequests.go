package database

import (
	"context"
	"errors"
	"time"

	"github.com/supperdoggy/spot-models"
	"github.com/gofrs/uuid"
	"go.mongodb.org/mongo-driver/mongo"
	"gopkg.in/mgo.v2/bson"
)

func (d *db) GetActiveRequests(ctx context.Context) ([]models.DownloadQueueRequest, error) {
	var requests []models.DownloadQueueRequest

	cursor, err := d.downloadQueueRequestCollection().Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var request models.DownloadQueueRequest
		if err := cursor.Decode(&request); err != nil {
			return nil, err
		}

		requests = append(requests, request)
	}

	return requests, nil
}

func (d *db) GetActiveRequest(ctx context.Context, url string) (models.DownloadQueueRequest, error) {
	cur := d.downloadQueueRequestCollection().FindOne(ctx, bson.M{"spotify_url": url, "active": true})
	var req models.DownloadQueueRequest
	if err := cur.Decode(&req); err != nil {
		return models.DownloadQueueRequest{}, err
	}

	return req, nil
}

func (d *db) CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error) {
	var count int64
	count, err := d.downloadQueueRequestCollection().CountDocuments(ctx, bson.M{"spotify_url": url, "active": false})
	if err != nil && err != mongo.ErrNoDocuments {
		return false, err
	}

	return count > 0, nil
}

func (d *db) NewDownloadRequest(ctx context.Context, url, name string, creatorID int64) error {
	id, err := uuid.NewV4()
	if err != nil {
		return err
	}

	request := models.DownloadQueueRequest{
		SpotifyURL: url,
		Name:       name,
		Active:     true,
		ID:         id.String(),
		CreatedAt:  time.Now().Unix(),
		CreatorID:  creatorID,
	}

	_, err = d.downloadQueueRequestCollection().InsertOne(ctx, request)
	if err != nil {
		return err
	}

	return nil
}

func (d *db) UpdateActiveRequest(ctx context.Context, request models.DownloadQueueRequest) error {
	info, err := d.downloadQueueRequestCollection().UpdateOne(ctx, bson.M{"_id": request.ID}, bson.M{"$set": bson.M{
		"active":      request.Active,
		"sync_count":  request.SyncCount,
		"errored":     request.Errored,
		"retry_count": request.RetryCount,
	}})
	if err != nil {
		return err
	}

	if info.MatchedCount == 0 {
		return errors.New("not found")
	}
	return nil
}

func (d *db) DeactivateRequest(ctx context.Context, id string) error {
	_, err := d.downloadQueueRequestCollection().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().Unix()}})
	if err != nil {
		return err
	}

	return nil
}
