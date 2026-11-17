package db

import (
	"context"
	"errors"
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
	GetUnresolvedFailedTracksPage(ctx context.Context, page, pageSize int) (FailedTracksPage, error)
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
	CreatedAt      int64  `json:"created_at"`
}

type FailedTracksPage struct {
	Tracks   []FailedTrack `json:"tracks"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
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
	now := time.Now().Unix()
	request := newDownloadQueueRequest(
		id.String(),
		url,
		name,
		creatorID,
		objectType,
		expectedTrackCount,
		trackMetadata,
		now,
	)

	_, err := d.downloadQueueRequestCollection.InsertOne(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to insert download request: %w", err)
	}

	return nil
}

func newDownloadQueueRequest(
	id, url, name string,
	creatorID int64,
	objectType spotify.SpotifyObjectType,
	expectedTrackCount int,
	trackMetadata []spotify.TrackMetadata,
	now int64,
) models.DownloadQueueRequest {
	return models.DownloadQueueRequest{
		SpotifyURL:         url,
		ObjectType:         objectType,
		Name:               name,
		Active:             true,
		State:              models.DownloadRequestStatePending,
		ID:                 id,
		CreatedAt:          now,
		UpdatedAt:          now,
		CreatorID:          creatorID,
		ExpectedTrackCount: expectedTrackCount,
		FoundTrackCount:    0,
		TrackMetadata:      trackMetadata,
	}
}

func (d *db) NewPlaylistRequest(ctx context.Context, url string, creatorID int64, noPull bool) error {
	id := uuid.NewV4()
	now := time.Now().Unix()
	request := newPlaylistRequest(id.String(), url, creatorID, noPull, now)

	_, err := d.playlistRequestCollection.InsertOne(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to insert playlist request: %w", err)
	}

	return nil
}

func newPlaylistRequest(id, url string, creatorID int64, noPull bool, now int64) models.PlaylistRequest {
	return models.PlaylistRequest{
		ID:         id,
		CreatorID:  creatorID,
		SpotifyURL: url,
		Active:     true,
		NoPull:     noPull,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
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

func (d *db) getUnresolvedFailedTracks(ctx context.Context) ([]FailedTrack, error) {
	cursor, err := d.downloadQueueRequestCollection.Find(ctx, unresolvedFailedRequestFilter())
	if err != nil {
		return nil, fmt.Errorf("failed to find failed requests with unresolved tracks: %w", err)
	}
	defer cursor.Close(ctx)

	requests := make([]models.DownloadQueueRequest, 0)
	if err := cursor.All(ctx, &requests); err != nil {
		return nil, fmt.Errorf("failed to decode requests with unresolved tracks: %w", err)
	}

	failedTracks := collectUnresolvedFailedTracks(requests)
	if len(failedTracks) == 0 {
		return []FailedTrack{}, nil
	}

	activeURLs, err := d.findRunnableActiveRequestURLs(ctx, failedTracks)
	if err != nil {
		return nil, err
	}
	indexedTracks, err := d.findIndexedTrackKeys(ctx, failedTracks)
	if err != nil {
		return nil, err
	}

	return filterResolvedFailedTracks(failedTracks, activeURLs, indexedTracks), nil
}

func (d *db) GetUnresolvedFailedTracksPage(ctx context.Context, page, pageSize int) (FailedTracksPage, error) {
	tracks, err := d.getUnresolvedFailedTracks(ctx)
	if err != nil {
		return FailedTracksPage{}, err
	}

	return paginateFailedTracks(tracks, page, pageSize)
}

func (d *db) GetUnresolvedFailedTrackByURL(ctx context.Context, trackURL string) (FailedTrack, error) {
	tracks, err := d.getUnresolvedFailedTracks(ctx)
	if err != nil {
		return FailedTrack{}, err
	}

	return selectFailedTrackByURL(tracks, trackURL)
}

func (d *db) HasActiveRequestByURL(ctx context.Context, trackURL string) (bool, error) {
	count, err := d.downloadQueueRequestCollection.CountDocuments(ctx, runnableActiveRequestFilter([]string{trackURL}))
	if err != nil {
		return false, fmt.Errorf("failed to check runnable active request by url: %w", err)
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
	now := time.Now().Unix()
	result, err := d.downloadQueueRequestCollection.UpdateOne(
		ctx,
		bson.M{"_id": id},
		deactivateRequestUpdate(now),
	)
	if err != nil {
		return fmt.Errorf("failed to deactivate request: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("request with id %s not found", id)
	}

	return nil
}

func deactivateRequestUpdate(now int64) bson.M {
	return bson.M{
		"$set": bson.M{
			"active":     false,
			"errored":    false,
			"state":      models.DownloadRequestStateCancelled,
			"updated_at": now,
		},
		"$unset": bson.M{
			"worker_id":        "",
			"claim_id":         "",
			"lease_expires_at": "",
			"next_attempt_at":  "",
		},
	}
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
		downloadProgressUpdate(request.FoundTrackCount),
	)
	if err != nil {
		return fmt.Errorf("failed to update download request: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("request with id %s not found", request.ID)
	}

	return nil
}

// downloadProgressUpdate is intentionally ownership-safe. Queue display code
// may record an observed catalog count, but it must not replace lifecycle,
// lease, error, or track metadata owned by the acquisition worker.
func downloadProgressUpdate(foundTrackCount int) bson.M {
	return bson.M{
		"$max": bson.M{
			"found_track_count": foundTrackCount,
		},
	}
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

func unresolvedFailedRequestFilter() bson.M {
	return bson.M{
		"$and": bson.A{
			bson.M{"track_metadata": bson.M{
				"$elemMatch": bson.M{"found": bson.M{"$ne": true}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"state": bson.M{"$in": bson.A{
					models.DownloadRequestStateNeedsReview,
					models.DownloadRequestStateFailed,
				}}},
				bson.M{"$and": bson.A{
					legacyRequestStateFilter(),
					bson.M{"errored": true},
				}},
			}},
		},
	}
}

func legacyRequestStateFilter() bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"state": bson.M{"$exists": false}},
		bson.M{"state": nil},
		bson.M{"state": ""},
	}}
}

func runnableActiveRequestFilter(trackURLs []string) bson.M {
	return bson.M{
		"spotify_url": bson.M{"$in": trackURLs},
		"active":      true,
		"$or": bson.A{
			bson.M{"state": bson.M{"$in": bson.A{
				models.DownloadRequestStatePending,
				models.DownloadRequestStateClaimed,
				models.DownloadRequestStateResolving,
				models.DownloadRequestStateDownloading,
				models.DownloadRequestStateValidating,
				models.DownloadRequestStateImported,
				models.DownloadRequestStateRetryWait,
			}}},
			bson.M{"$and": bson.A{
				legacyRequestStateFilter(),
				bson.M{"errored": bson.M{"$ne": true}},
			}},
		},
	}
}

func (d *db) findRunnableActiveRequestURLs(ctx context.Context, tracks []FailedTrack) (map[string]struct{}, error) {
	trackURLs := make([]string, 0, len(tracks))
	for _, track := range tracks {
		trackURLs = append(trackURLs, track.SpotifyURL)
	}

	cursor, err := d.downloadQueueRequestCollection.Find(
		ctx,
		runnableActiveRequestFilter(trackURLs),
		options.Find().SetProjection(bson.M{"_id": 0, "spotify_url": 1}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find runnable active requests: %w", err)
	}
	defer cursor.Close(ctx)

	activeURLs := make(map[string]struct{})
	for cursor.Next(ctx) {
		var request struct {
			SpotifyURL string `bson:"spotify_url"`
		}
		if err := cursor.Decode(&request); err != nil {
			return nil, fmt.Errorf("failed to decode runnable active request: %w", err)
		}
		activeURLs[request.SpotifyURL] = struct{}{}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate runnable active requests: %w", err)
	}

	return activeURLs, nil
}

func (d *db) findIndexedTrackKeys(ctx context.Context, tracks []FailedTrack) (map[string]struct{}, error) {
	orPairs := make(bson.A, 0, len(tracks))
	seenPairs := make(map[string]struct{}, len(tracks))
	for _, track := range tracks {
		artist := strings.TrimSpace(track.Artist)
		title := strings.TrimSpace(track.Title)
		if artist == "" || title == "" {
			continue
		}

		key := failedTrackKey(artist, title)
		if _, seen := seenPairs[key]; seen {
			continue
		}
		seenPairs[key] = struct{}{}
		orPairs = append(orPairs, bson.M{
			"artist": bson.M{"$regex": "^" + regexp.QuoteMeta(artist) + "$", "$options": "i"},
			"title":  bson.M{"$regex": "^" + regexp.QuoteMeta(title) + "$", "$options": "i"},
		})
	}

	indexedTracks := make(map[string]struct{})
	if len(orPairs) == 0 {
		return indexedTracks, nil
	}

	cursor, err := d.musicFilesCollection.Find(
		ctx,
		bson.M{"$or": orPairs},
		options.Find().SetProjection(bson.M{"_id": 0, "artist": 1, "title": 1}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find indexed failed tracks: %w", err)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var musicFile struct {
			Artist string `bson:"artist"`
			Title  string `bson:"title"`
		}
		if err := cursor.Decode(&musicFile); err != nil {
			return nil, fmt.Errorf("failed to decode indexed failed track: %w", err)
		}
		indexedTracks[failedTrackKey(musicFile.Artist, musicFile.Title)] = struct{}{}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate indexed failed tracks: %w", err)
	}

	return indexedTracks, nil
}

func collectUnresolvedFailedTracks(requests []models.DownloadQueueRequest) []FailedTrack {
	failedByURL := make(map[string]FailedTrack)

	for _, request := range requests {
		createdAt := request.CreatedAt
		if createdAt == 0 {
			createdAt = request.UpdatedAt
		}

		for _, track := range request.TrackMetadata {
			if track.Found || strings.TrimSpace(track.SpotifyURL) == "" {
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
					CreatedAt:      createdAt,
				}
				continue
			}

			current.SourceCount++
			if track.FailedAttempts > current.FailedAttempts {
				current.FailedAttempts = track.FailedAttempts
			}
			if createdAt >= current.CreatedAt {
				current.CreatedAt = createdAt
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

	return failedTracks
}

func filterResolvedFailedTracks(
	failedTracks []FailedTrack,
	activeURLs map[string]struct{},
	indexedTracks map[string]struct{},
) []FailedTrack {
	unresolvedTracks := make([]FailedTrack, 0, len(failedTracks))
	for _, track := range failedTracks {
		if _, active := activeURLs[track.SpotifyURL]; active {
			continue
		}
		if _, indexed := indexedTracks[failedTrackKey(track.Artist, track.Title)]; indexed {
			continue
		}
		unresolvedTracks = append(unresolvedTracks, track)
	}

	sort.Slice(unresolvedTracks, func(i, j int) bool {
		if unresolvedTracks[i].CreatedAt == unresolvedTracks[j].CreatedAt {
			return unresolvedTracks[i].SpotifyURL < unresolvedTracks[j].SpotifyURL
		}
		return unresolvedTracks[i].CreatedAt < unresolvedTracks[j].CreatedAt
	})

	return unresolvedTracks
}

func failedTrackKey(artist, title string) string {
	return strings.ToLower(strings.TrimSpace(artist)) + "\x00" + strings.ToLower(strings.TrimSpace(title))
}

func paginateFailedTracks(tracks []FailedTrack, page, pageSize int) (FailedTracksPage, error) {
	if page < 0 {
		return FailedTracksPage{}, errors.New("page must not be negative")
	}
	if pageSize <= 0 {
		return FailedTracksPage{}, errors.New("page size must be positive")
	}

	result := FailedTracksPage{
		Tracks:   []FailedTrack{},
		Page:     page,
		PageSize: pageSize,
		Total:    len(tracks),
	}
	if len(tracks) == 0 {
		if page != 0 {
			return FailedTracksPage{}, errors.New("page out of range")
		}
		return result, nil
	}

	start := page * pageSize
	if start >= len(tracks) {
		return FailedTracksPage{}, errors.New("page out of range")
	}
	end := start + pageSize
	if end > len(tracks) {
		end = len(tracks)
	}
	result.Tracks = append(result.Tracks, tracks[start:end]...)

	return result, nil
}

func selectFailedTrackByURL(tracks []FailedTrack, trackURL string) (FailedTrack, error) {
	for _, track := range tracks {
		if track.SpotifyURL == trackURL {
			return track, nil
		}
	}

	return FailedTrack{}, mongo.ErrNoDocuments
}
