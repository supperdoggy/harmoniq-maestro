package db

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	uuid "github.com/satori/go.uuid"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type Database interface {
	NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType, expectedTrackCount int, trackMetadata []spotify.TrackMetadata) error
	GetActiveRequests(ctx context.Context) ([]models.DownloadQueueRequest, error)
	GetUnresolvedFailedTracks(ctx context.Context) ([]FailedTrack, error)
	GetUnresolvedFailedTrackByURL(ctx context.Context, trackURL string) (FailedTrack, error)
	HasActiveRequestByURL(ctx context.Context, trackURL string) (bool, error)
	DeactivateRequest(ctx context.Context, id string) error
	NewPlaylistRequest(ctx context.Context, url string, creatorID int64, noPull bool) error
	GetActivePlaylists(ctx context.Context) ([]models.PlaylistRequest, error)
	FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error)
	UpdateDownloadRequest(ctx context.Context, request models.DownloadQueueRequest) error
	NewSubscribedPlaylist(ctx context.Context, url string, creatorID int64, name string, refreshInterval string, noPull bool) error
	GetSubscribedPlaylists(ctx context.Context, creatorID int64) ([]models.SubscribedPlaylist, error)
	DeleteSubscribedPlaylist(ctx context.Context, url string, creatorID int64) error
	CheckSubscriptionExists(ctx context.Context, url string, creatorID int64) (bool, error)
	Close(ctx context.Context) error
	Ping(ctx context.Context) error
	GetStats(ctx context.Context) (*Stats, error)
}

type Stats struct {
	TotalMusicFiles       int64 `json:"total_music_files"`
	ActiveDownloadQueue   int64 `json:"active_download_queue"`
	TotalDownloadRequests int64 `json:"total_download_requests"`
	ActivePlaylists       int64 `json:"active_playlists"`
}

type FailedTrack struct {
	SpotifyURL     string `json:"spotify_url"`
	Artist         string `json:"artist"`
	Title          string `json:"title"`
	FailedAttempts int    `json:"failed_attempts"`
	SourceCount    int    `json:"source_count"`
	LastSeenAt     int64  `json:"last_seen_at"`
}

type db struct {
	conn *mongo.Client
	log  *zap.Logger

	// Collections
	downloadQueueRequestCollection *mongo.Collection
	playlistRequestCollection      *mongo.Collection
	subscribedPlaylistsCollection  *mongo.Collection
	musicFilesCollection           *mongo.Collection
	dbname                         string
}

func NewDatabase(ctx context.Context, log *zap.Logger, url, dbname string) (Database, error) {
	conn, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Verify connection
	if err := conn.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	return &db{
		conn:   conn,
		log:    log,
		dbname: dbname,

		downloadQueueRequestCollection: conn.Database(dbname).Collection("download-queue-requests"),
		playlistRequestCollection:      conn.Database(dbname).Collection("playlist-requests"),
		subscribedPlaylistsCollection:  conn.Database(dbname).Collection("subscribed_playlists"),
		musicFilesCollection:           conn.Database(dbname).Collection("music-files"),
	}, nil
}

func (d *db) Close(ctx context.Context) error {
	return d.conn.Disconnect(ctx)
}

func (d *db) Ping(ctx context.Context) error {
	return d.conn.Ping(ctx, nil)
}

func (d *db) NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType, expectedTrackCount int, trackMetadata []spotify.TrackMetadata) error {
	id := uuid.NewV4()
	request := models.DownloadQueueRequest{
		SpotifyURL:         url,
		ObjectType:         objectType,
		Name:               name,
		Active:             true,
		ID:                 id.String(),
		CreatedAt:          time.Now().Unix(),
		UpdatedAt:          time.Now().Unix(),
		CreatorID:          creatorID,
		ExpectedTrackCount: expectedTrackCount,
		FoundTrackCount:    0,
		TrackMetadata:      trackMetadata,
	}

	_, err := d.downloadQueueRequestCollection.InsertOne(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to insert download request: %w", err)
	}

	return nil
}

func (d *db) NewPlaylistRequest(ctx context.Context, url string, creatorID int64, noPull bool) error {
	id := uuid.NewV4()
	request := models.PlaylistRequest{
		SpotifyURL: url,
		Active:     true,
		ID:         id.String(),
		CreatedAt:  time.Now().Unix(),
		CreatorID:  creatorID,
		NoPull:     noPull,
	}

	_, err := d.playlistRequestCollection.InsertOne(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to insert playlist request: %w", err)
	}

	return nil
}

func (d *db) GetActiveRequests(ctx context.Context) ([]models.DownloadQueueRequest, error) {
	var requests []models.DownloadQueueRequest

	cursor, err := d.downloadQueueRequestCollection.Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, fmt.Errorf("failed to find active requests: %w", err)
	}
	defer cursor.Close(ctx)

	if err := cursor.All(ctx, &requests); err != nil {
		return nil, fmt.Errorf("failed to decode requests: %w", err)
	}

	return requests, nil
}

func (d *db) GetUnresolvedFailedTracks(ctx context.Context) ([]FailedTrack, error) {
	filter := bson.M{
		"track_metadata": bson.M{
			"$elemMatch": bson.M{
				"found": bson.M{"$ne": true},
			},
		},
	}

	cursor, err := d.downloadQueueRequestCollection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to find requests with unresolved tracks: %w", err)
	}
	defer cursor.Close(ctx)

	requests := make([]models.DownloadQueueRequest, 0)
	if err := cursor.All(ctx, &requests); err != nil {
		return nil, fmt.Errorf("failed to decode requests with unresolved tracks: %w", err)
	}

	tracks, err := filterUnresolvedFailedTracks(ctx, requests, d.musicFileExistsInsensitive, d.HasActiveRequestByURL)
	if err != nil {
		return nil, err
	}

	return tracks, nil
}

func (d *db) GetUnresolvedFailedTrackByURL(ctx context.Context, trackURL string) (FailedTrack, error) {
	tracks, err := d.GetUnresolvedFailedTracks(ctx)
	if err != nil {
		return FailedTrack{}, err
	}

	return selectFailedTrackByURL(tracks, trackURL)
}

func (d *db) HasActiveRequestByURL(ctx context.Context, trackURL string) (bool, error) {
	count, err := d.downloadQueueRequestCollection.CountDocuments(ctx, bson.M{
		"spotify_url": trackURL,
		"active":      true,
	})
	if err != nil {
		return false, fmt.Errorf("failed to check active request by url: %w", err)
	}

	return count > 0, nil
}

func (d *db) GetActivePlaylists(ctx context.Context) ([]models.PlaylistRequest, error) {
	var playlists []models.PlaylistRequest

	cursor, err := d.playlistRequestCollection.Find(ctx, bson.M{"active": true})
	if err != nil {
		return nil, fmt.Errorf("failed to find active playlists: %w", err)
	}
	defer cursor.Close(ctx)

	if err := cursor.All(ctx, &playlists); err != nil {
		return nil, fmt.Errorf("failed to decode playlists: %w", err)
	}

	return playlists, nil
}

func (d *db) DeactivateRequest(ctx context.Context, id string) error {
	result, err := d.downloadQueueRequestCollection.UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().Unix()}},
	)
	if err != nil {
		return fmt.Errorf("failed to deactivate request: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("request with id %s not found", id)
	}

	return nil
}

func (d *db) FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error) {
	if len(artists) != len(titles) {
		return nil, fmt.Errorf("artists and titles must have the same length")
	}

	orPairs := make([]bson.M, 0, len(artists))
	for i := range artists {
		orPairs = append(orPairs, bson.M{
			"artist": artists[i],
			"title":  titles[i],
		})
	}

	cur, err := d.musicFilesCollection.Find(ctx, bson.M{
		"$or": orPairs,
	}, options.Find().SetProjection(bson.M{"meta_data": 0}))
	if err != nil {
		return nil, fmt.Errorf("failed to find music files: %w", err)
	}
	defer cur.Close(ctx)

	files := make([]models.MusicFile, 0)
	for cur.Next(ctx) {
		var file models.MusicFile
		if err := cur.Decode(&file); err != nil {
			return nil, fmt.Errorf("failed to decode music file: %w", err)
		}
		files = append(files, file)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return files, nil
}

func (d *db) UpdateDownloadRequest(ctx context.Context, request models.DownloadQueueRequest) error {
	result, err := d.downloadQueueRequestCollection.UpdateOne(
		ctx,
		bson.M{"_id": request.ID},
		bson.M{"$set": bson.M{
			"expected_track_count": request.ExpectedTrackCount,
			"found_track_count":    request.FoundTrackCount,
			"track_metadata":       request.TrackMetadata,
			"name":                 request.Name,
			"active":               request.Active,
			"object_type":          request.ObjectType,
			"updated_at":           request.UpdatedAt,
		}},
	)
	if err != nil {
		return fmt.Errorf("failed to update download request: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("request with id %s not found", request.ID)
	}

	return nil
}

func (d *db) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	// Count total music files
	musicCount, err := d.musicFilesCollection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to count music files: %w", err)
	}
	stats.TotalMusicFiles = musicCount

	// Count active download requests
	activeQueue, err := d.downloadQueueRequestCollection.CountDocuments(ctx, bson.M{"active": true})
	if err != nil {
		return nil, fmt.Errorf("failed to count active downloads: %w", err)
	}
	stats.ActiveDownloadQueue = activeQueue

	// Count total download requests
	totalDownloads, err := d.downloadQueueRequestCollection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to count total downloads: %w", err)
	}
	stats.TotalDownloadRequests = totalDownloads

	// Count active playlists
	activePlaylists, err := d.playlistRequestCollection.CountDocuments(ctx, bson.M{"active": true})
	if err != nil {
		return nil, fmt.Errorf("failed to count active playlists: %w", err)
	}
	stats.ActivePlaylists = activePlaylists

	return stats, nil
}

func (d *db) NewSubscribedPlaylist(ctx context.Context, url string, creatorID int64, name string, refreshInterval string, noPull bool) error {
	id := uuid.NewV4()
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

	_, err := d.subscribedPlaylistsCollection.InsertOne(ctx, playlist)
	if err != nil {
		return fmt.Errorf("failed to insert subscribed playlist: %w", err)
	}

	return nil
}

func (d *db) GetSubscribedPlaylists(ctx context.Context, creatorID int64) ([]models.SubscribedPlaylist, error) {
	var playlists []models.SubscribedPlaylist

	cursor, err := d.subscribedPlaylistsCollection.Find(ctx, bson.M{"creator_id": creatorID, "active": true})
	if err != nil {
		return nil, fmt.Errorf("failed to find subscribed playlists: %w", err)
	}
	defer cursor.Close(ctx)

	if err := cursor.All(ctx, &playlists); err != nil {
		return nil, fmt.Errorf("failed to decode subscribed playlists: %w", err)
	}

	return playlists, nil
}

func (d *db) DeleteSubscribedPlaylist(ctx context.Context, url string, creatorID int64) error {
	result, err := d.subscribedPlaylistsCollection.UpdateOne(
		ctx,
		bson.M{"spotify_url": url, "creator_id": creatorID},
		bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().Unix()}},
	)
	if err != nil {
		return fmt.Errorf("failed to delete subscribed playlist: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("subscription not found")
	}

	return nil
}

func (d *db) CheckSubscriptionExists(ctx context.Context, url string, creatorID int64) (bool, error) {
	count, err := d.subscribedPlaylistsCollection.CountDocuments(ctx, bson.M{
		"spotify_url": url,
		"creator_id":  creatorID,
		"active":      true,
	})
	if err != nil {
		return false, fmt.Errorf("failed to check subscription existence: %w", err)
	}
	return count > 0, nil
}

func (d *db) musicFileExistsInsensitive(ctx context.Context, artist, title string) (bool, error) {
	if strings.TrimSpace(artist) == "" || strings.TrimSpace(title) == "" {
		return false, nil
	}

	escapedArtist := regexp.QuoteMeta(artist)
	escapedTitle := regexp.QuoteMeta(title)

	count, err := d.musicFilesCollection.CountDocuments(ctx, bson.M{
		"$and": []bson.M{
			{"artist": bson.M{"$regex": "^" + escapedArtist + "$", "$options": "i"}},
			{"title": bson.M{"$regex": "^" + escapedTitle + "$", "$options": "i"}},
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check indexed track existence: %w", err)
	}

	return count > 0, nil
}

func filterUnresolvedFailedTracks(
	ctx context.Context,
	requests []models.DownloadQueueRequest,
	musicFileExists func(context.Context, string, string) (bool, error),
	hasActiveRequestByURL func(context.Context, string) (bool, error),
) ([]FailedTrack, error) {
	failedByURL := make(map[string]FailedTrack)

	for _, request := range requests {
		lastSeen := request.UpdatedAt
		if lastSeen == 0 {
			lastSeen = request.CreatedAt
		}

		for _, track := range request.TrackMetadata {
			if track.Found || strings.TrimSpace(track.SpotifyURL) == "" {
				continue
			}

			existsInLibrary, err := musicFileExists(ctx, track.Artist, track.Title)
			if err != nil {
				return nil, err
			}
			if existsInLibrary {
				continue
			}

			hasActive, err := hasActiveRequestByURL(ctx, track.SpotifyURL)
			if err != nil {
				return nil, err
			}
			if hasActive {
				continue
			}

			current, exists := failedByURL[track.SpotifyURL]
			if !exists {
				failedByURL[track.SpotifyURL] = FailedTrack{
					SpotifyURL:     track.SpotifyURL,
					Artist:         track.Artist,
					Title:          track.Title,
					FailedAttempts: track.FailedAttempts,
					SourceCount:    1,
					LastSeenAt:     lastSeen,
				}
				continue
			}

			current.SourceCount++
			if track.FailedAttempts > current.FailedAttempts {
				current.FailedAttempts = track.FailedAttempts
			}
			if lastSeen >= current.LastSeenAt {
				current.LastSeenAt = lastSeen
				current.Artist = track.Artist
				current.Title = track.Title
			}
			failedByURL[track.SpotifyURL] = current
		}
	}

	failedTracks := make([]FailedTrack, 0, len(failedByURL))
	for _, track := range failedByURL {
		failedTracks = append(failedTracks, track)
	}

	sort.Slice(failedTracks, func(i, j int) bool {
		if failedTracks[i].LastSeenAt == failedTracks[j].LastSeenAt {
			return failedTracks[i].SpotifyURL < failedTracks[j].SpotifyURL
		}
		return failedTracks[i].LastSeenAt > failedTracks[j].LastSeenAt
	})

	return failedTracks, nil
}

func selectFailedTrackByURL(tracks []FailedTrack, trackURL string) (FailedTrack, error) {
	for _, track := range tracks {
		if track.SpotifyURL == trackURL {
			return track, nil
		}
	}

	return FailedTrack{}, mongo.ErrNoDocuments
}
