package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

type Database interface {
	Close(ctx context.Context) error

	GetActiveRequests(ctx context.Context) ([]models.DownloadQueueRequest, error)
	GetActiveRequest(ctx context.Context, url string) (models.DownloadQueueRequest, error)
	GetRequestByID(ctx context.Context, id string) (models.DownloadQueueRequest, error)
	ClaimNextActiveRequest(ctx context.Context, workerID, backend string, leaseDuration time.Duration) (models.DownloadQueueRequest, error)
	RenewRequestLease(ctx context.Context, id, workerID, claimID string, leaseDuration time.Duration) error
	ReleaseRequestLease(ctx context.Context, id, workerID, claimID string, nextState models.DownloadRequestState) error
	UpdateClaimedRequest(ctx context.Context, request models.DownloadQueueRequest, workerID, claimID string) error
	CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error)
	NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType) error
	UpdateActiveRequest(ctx context.Context, request models.DownloadQueueRequest) error

	GetActivePlaylists(ctx context.Context) ([]models.PlaylistRequest, error)
	UpdatePlaylistRequest(ctx context.Context, request models.PlaylistRequest) error

	FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error)
	FindMusicFilesForTracks(ctx context.Context, tracks []spotify.TrackMetadata) ([]models.MusicFile, error)
	IndexMusicFile(ctx context.Context, file models.MusicFile) error
	UpsertMusicFile(ctx context.Context, file models.MusicFile) (models.MusicFile, error)

	GetIndexStatus(ctx context.Context) (models.IndexStatus, error)
	UpdateIndexStatus(ctx context.Context, status models.IndexStatus) error
	EnsureIndexes(ctx context.Context) error
}

var (
	ErrRequestNotFound = errors.New("download request not found")
	ErrLeaseLost       = errors.New("download request lease is no longer owned by this worker")
)

type db struct {
	conn *mongo.Client
	log  *zap.Logger

	dbname string
}

func NewDatabase(ctx context.Context, log *zap.Logger, url, dbname string) (Database, error) {
	conn, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(ctx, nil); err != nil {
		if disconnectErr := conn.Disconnect(ctx); disconnectErr != nil {
			log.Warn("failed to disconnect after database ping failure", zap.Error(disconnectErr))
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &db{
		conn: conn,
		log:  log,

		dbname: dbname,
	}

	// Index creation is best effort during the transition. Existing duplicate
	// legacy data must not prevent the worker from starting.
	if err := database.EnsureIndexes(ctx); err != nil {
		log.Warn("failed to ensure one or more database indexes", zap.Error(err))
	}

	return database, nil
}

func (d *db) Close(ctx context.Context) error {
	return d.conn.Disconnect(ctx)
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
		State:      models.DownloadRequestStatePending,
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

func (d *db) CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error) {
	var count int64
	count, err := d.downloadQueueRequestCollection().CountDocuments(ctx, alreadySyncedFilter(url))
	if err != nil && err != mongo.ErrNoDocuments {
		return false, err
	}

	return count > 0, nil
}

func alreadySyncedFilter(url string) bson.M {
	inFlightOrSuccessfulStates := bson.A{
		models.DownloadRequestStatePending,
		models.DownloadRequestStateClaimed,
		models.DownloadRequestStateResolving,
		models.DownloadRequestStateDownloading,
		models.DownloadRequestStateValidating,
		models.DownloadRequestStateImported,
		models.DownloadRequestStateRetryWait,
		models.DownloadRequestStateNeedsReview,
		models.DownloadRequestStateCompleted,
	}
	return bson.M{
		"spotify_url": url,
		"$or": bson.A{
			// Suppress duplicate work while a request is pending, in flight,
			// awaiting retry/review, or already complete. Failed and cancelled
			// jobs remain eligible for an intentional fresh request.
			bson.M{"state": bson.M{"$in": inFlightOrSuccessfulStates}},
			bson.M{"$and": bson.A{
				bson.M{"$or": bson.A{
					bson.M{"state": bson.M{"$exists": false}},
					bson.M{"state": nil},
					bson.M{"state": ""},
				}},
				bson.M{"$or": bson.A{
					bson.M{"active": true},
					bson.M{"$and": bson.A{
						bson.M{"active": false},
						bson.M{"errored": bson.M{"$ne": true}},
					}},
				}},
			}},
		},
	}
}

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

// GetRequestByID returns a request regardless of legacy active/state flags.
func (d *db) GetRequestByID(ctx context.Context, id string) (models.DownloadQueueRequest, error) {
	var request models.DownloadQueueRequest
	if err := d.downloadQueueRequestCollection().FindOne(ctx, bson.M{"_id": id}).Decode(&request); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.DownloadQueueRequest{}, ErrRequestNotFound
		}
		return models.DownloadQueueRequest{}, err
	}

	return request, nil
}

// ClaimNextActiveRequest atomically selects and leases one eligible request.
// Legacy documents without state or lease fields are deliberately eligible.
func (d *db) ClaimNextActiveRequest(
	ctx context.Context,
	workerID, backend string,
	leaseDuration time.Duration,
) (models.DownloadQueueRequest, error) {
	if workerID == "" {
		return models.DownloadQueueRequest{}, errors.New("worker ID is required")
	}
	backend = strings.TrimSpace(backend)
	if backend == "" {
		return models.DownloadQueueRequest{}, errors.New("backend is required")
	}
	if leaseDuration <= 0 {
		return models.DownloadQueueRequest{}, errors.New("lease duration must be positive")
	}
	claimID, err := newClaimID()
	if err != nil {
		return models.DownloadQueueRequest{}, fmt.Errorf("generate claim ID: %w", err)
	}

	now := time.Now().UTC()
	var request models.DownloadQueueRequest
	err = d.downloadQueueRequestCollection().FindOneAndUpdate(
		ctx,
		buildClaimFilter(now.Unix(), backend),
		buildClaimUpdate(workerID, claimID, backend, now.Unix(), leaseExpiryUnix(now, leaseDuration)),
		options.FindOneAndUpdate().
			SetSort(claimSort()).
			SetReturnDocument(options.After),
	).Decode(&request)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.DownloadQueueRequest{}, mongo.ErrNoDocuments
		}
		return models.DownloadQueueRequest{}, err
	}

	return request, nil
}

// RenewRequestLease extends a live lease only while the caller still owns it.
func (d *db) RenewRequestLease(
	ctx context.Context,
	id, workerID, claimID string,
	leaseDuration time.Duration,
) error {
	if id == "" {
		return errors.New("request ID is required")
	}
	if workerID == "" {
		return errors.New("worker ID is required")
	}
	if claimID == "" {
		return errors.New("claim ID is required")
	}
	if leaseDuration <= 0 {
		return errors.New("lease duration must be positive")
	}

	now := time.Now().UTC()
	result, err := d.downloadQueueRequestCollection().UpdateOne(
		ctx,
		ownedLeaseFilter(id, workerID, claimID, now.Unix()),
		bson.M{"$set": bson.M{
			"lease_expires_at": leaseExpiryUnix(now, leaseDuration),
			"updated_at":       now.Unix(),
		}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrLeaseLost
	}

	return nil
}

// ReleaseRequestLease transitions a request and relinquishes ownership in one
// atomic update. The legacy Active and Errored flags are dual-written for old
// readers during migration.
func (d *db) ReleaseRequestLease(
	ctx context.Context,
	id, workerID, claimID string,
	nextState models.DownloadRequestState,
) error {
	if id == "" {
		return errors.New("request ID is required")
	}
	if workerID == "" {
		return errors.New("worker ID is required")
	}
	if claimID == "" {
		return errors.New("claim ID is required")
	}
	if !isKnownRequestState(nextState) {
		return fmt.Errorf("unknown download request state %q", nextState)
	}

	now := time.Now().UTC().Unix()
	active, errored := legacyFlagsForState(nextState)
	update := bson.M{
		"$set": bson.M{
			"state":      nextState,
			"active":     active,
			"errored":    errored,
			"updated_at": now,
		},
		"$unset": bson.M{
			"worker_id":        "",
			"claim_id":         "",
			"lease_expires_at": "",
		},
	}
	if nextState != models.DownloadRequestStateRetryWait {
		update["$unset"].(bson.M)["next_attempt_at"] = ""
	}

	result, err := d.downloadQueueRequestCollection().UpdateOne(
		ctx,
		ownedLeaseFilter(id, workerID, claimID, now),
		update,
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrLeaseLost
	}

	return nil
}

// UpdateClaimedRequest persists worker progress only if the worker still owns
// an unexpired lease. This prevents a stale worker from overwriting a request
// after it has been reclaimed.
func (d *db) UpdateClaimedRequest(
	ctx context.Context,
	request models.DownloadQueueRequest,
	workerID, claimID string,
) error {
	if request.ID == "" {
		return errors.New("request ID is required")
	}
	if workerID == "" {
		return errors.New("worker ID is required")
	}
	if claimID == "" {
		return errors.New("claim ID is required")
	}
	if !isKnownRequestState(request.State) {
		return fmt.Errorf("unknown download request state %q", request.State)
	}

	now := time.Now().UTC().Unix()
	result, err := d.downloadQueueRequestCollection().UpdateOne(
		ctx,
		ownedLeaseFilter(request.ID, workerID, claimID, now),
		bson.M{"$set": claimedRequestUpdateFields(request, now)},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrLeaseLost
	}

	return nil
}

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
	if err != nil {
		return err
	}

	if info.MatchedCount == 0 {
		return errors.New("not found")
	}

	return nil
}

func (d *db) UpdateActiveRequest(ctx context.Context, request models.DownloadQueueRequest) error {
	info, err := d.downloadQueueRequestCollection().UpdateOne(ctx, bson.M{"_id": request.ID}, bson.M{"$set": bson.M{
		"active":               request.Active,
		"sync_count":           request.SyncCount,
		"errored":              request.Errored,
		"retry_count":          request.RetryCount,
		"expected_track_count": request.ExpectedTrackCount,
		"found_track_count":    request.FoundTrackCount,
		"track_metadata":       request.TrackMetadata,
		"object_type":          request.ObjectType,
		"updated_at":           request.UpdatedAt,
	}})
	if err != nil {
		return err
	}

	if info.MatchedCount == 0 {
		return errors.New("not found")
	}
	return nil
}

func buildClaimFilter(now int64, backend string) bson.M {
	claimableStates := bson.A{
		models.DownloadRequestStatePending,
		models.DownloadRequestStateClaimed,
		models.DownloadRequestStateResolving,
		models.DownloadRequestStateDownloading,
		models.DownloadRequestStateValidating,
		models.DownloadRequestStateImported,
		models.DownloadRequestStateRetryWait,
	}

	return bson.M{
		"active": true,
		"$and": bson.A{
			// Missing, null, and empty state values are legacy pending jobs.
			bson.M{"$or": bson.A{
				bson.M{"state": bson.M{"$exists": false}},
				bson.M{"state": nil},
				bson.M{"state": ""},
				bson.M{"state": bson.M{"$in": claimableStates}},
			}},
			// A missing lease is unowned; an expired lease can be reclaimed.
			bson.M{"$or": bson.A{
				bson.M{"lease_expires_at": bson.M{"$exists": false}},
				bson.M{"lease_expires_at": nil},
				bson.M{"lease_expires_at": bson.M{"$lte": now}},
			}},
			// Legacy and non-retry states are immediately eligible. Retry jobs
			// wait until their scheduled time, if one was supplied.
			bson.M{"$or": bson.A{
				bson.M{"state": bson.M{"$ne": models.DownloadRequestStateRetryWait}},
				bson.M{"next_attempt_at": bson.M{"$exists": false}},
				bson.M{"next_attempt_at": nil},
				bson.M{"next_attempt_at": bson.M{"$lte": now}},
			}},
			// Unassigned requests are atomically pinned to the claiming
			// backend. Retried or reclaimed requests remain on that backend.
			bson.M{"$or": bson.A{
				bson.M{"backend": bson.M{"$exists": false}},
				bson.M{"backend": nil},
				bson.M{"backend": ""},
				bson.M{"backend": backend},
			}},
		},
	}
}

func buildClaimUpdate(workerID, claimID, backend string, now, leaseExpiresAt int64) bson.M {
	return bson.M{
		"$set": bson.M{
			"active":           true,
			"state":            models.DownloadRequestStateClaimed,
			"worker_id":        workerID,
			"claim_id":         claimID,
			"lease_expires_at": leaseExpiresAt,
			"backend":          backend,
			"updated_at":       now,
		},
		"$unset": bson.M{
			"next_attempt_at": "",
		},
	}
}

func claimSort() bson.D {
	return bson.D{
		{Key: "errored", Value: 1},
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	}
}

func ownedLeaseFilter(id, workerID, claimID string, now int64) bson.M {
	return bson.M{
		"_id":              id,
		"worker_id":        workerID,
		"claim_id":         claimID,
		"lease_expires_at": bson.M{"$gt": now},
	}
}

func newClaimID() (string, error) {
	const claimIDBytes = 32

	random := make([]byte, claimIDBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

func leaseExpiryUnix(now time.Time, leaseDuration time.Duration) int64 {
	expiresAt := now.Add(leaseDuration).Unix()
	if expiresAt <= now.Unix() {
		return now.Unix() + 1
	}
	return expiresAt
}

func claimedRequestUpdateFields(request models.DownloadQueueRequest, now int64) bson.M {
	active, errored := legacyFlagsForState(request.State)
	if request.UpdatedAt == 0 {
		request.UpdatedAt = now
	}

	return bson.M{
		"active":               active,
		"sync_count":           request.SyncCount,
		"errored":              errored,
		"retry_count":          request.RetryCount,
		"expected_track_count": request.ExpectedTrackCount,
		"found_track_count":    request.FoundTrackCount,
		"track_metadata":       request.TrackMetadata,
		"object_type":          request.ObjectType,
		"updated_at":           request.UpdatedAt,
		"state":                request.State,
		"next_attempt_at":      request.NextAttemptAt,
		"last_error":           request.LastError,
		"result":               request.Result,
	}
}

func legacyFlagsForState(state models.DownloadRequestState) (active, errored bool) {
	switch state {
	case models.DownloadRequestStateCompleted:
		return false, false
	case models.DownloadRequestStateFailed:
		return false, true
	case models.DownloadRequestStateCancelled:
		return false, false
	case models.DownloadRequestStateRetryWait, models.DownloadRequestStateNeedsReview:
		return true, true
	default:
		return true, false
	}
}

func isKnownRequestState(state models.DownloadRequestState) bool {
	switch state {
	case models.DownloadRequestStatePending,
		models.DownloadRequestStateClaimed,
		models.DownloadRequestStateResolving,
		models.DownloadRequestStateDownloading,
		models.DownloadRequestStateValidating,
		models.DownloadRequestStateImported,
		models.DownloadRequestStateCompleted,
		models.DownloadRequestStateRetryWait,
		models.DownloadRequestStateNeedsReview,
		models.DownloadRequestStateFailed,
		models.DownloadRequestStateCancelled:
		return true
	default:
		return false
	}
}

// IndexMusicFile indexes a music file in the database
func (d *db) IndexMusicFile(ctx context.Context, file models.MusicFile) error {
	file.ID = uuid.Must(uuid.NewV4()).String()
	file.CreatedAt = time.Now().Unix()
	_, err := d.musicFilesCollection().InsertOne(ctx, file)
	return err
}

// UpsertMusicFile atomically imports mutable catalog metadata using the
// strongest available identity: Spotify ID, then ISRC, then final path.
func (d *db) UpsertMusicFile(
	ctx context.Context,
	file models.MusicFile,
) (models.MusicFile, error) {
	filter, err := musicFileIdentityFilter(file)
	if err != nil {
		return models.MusicFile{}, err
	}

	now := time.Now().UTC().Unix()
	id := file.ID
	if id == "" {
		id = uuid.Must(uuid.NewV4()).String()
	}

	var stored models.MusicFile
	err = d.musicFilesCollection().FindOneAndUpdate(
		ctx,
		filter,
		musicFileUpsertUpdate(file, id, now),
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	).Decode(&stored)
	if err != nil {
		return models.MusicFile{}, err
	}
	return stored, nil
}

func musicFileIdentityFilter(file models.MusicFile) (bson.M, error) {
	switch {
	case file.SpotifyID != "":
		return bson.M{"spotify_id": file.SpotifyID}, nil
	case file.ISRC != "":
		return bson.M{"isrc": file.ISRC}, nil
	case file.Path != "":
		return bson.M{"path": file.Path}, nil
	default:
		return nil, errors.New("music file requires spotify ID, ISRC, or path")
	}
}

func musicFileUpsertUpdate(file models.MusicFile, id string, now int64) bson.M {
	createdAt := file.CreatedAt
	if createdAt == 0 {
		createdAt = now
	}
	updatedAt := file.UpdatedAt
	if updatedAt == 0 {
		updatedAt = now
	}

	set := bson.M{
		"artist":      file.Artist,
		"album":       file.Album,
		"title":       file.Title,
		"genre":       file.Genre,
		"path":        file.Path,
		"meta_data":   file.MetaData,
		"match_score": file.MatchScore,
		"updated_at":  updatedAt,
	}
	if file.SpotifyID != "" {
		set["spotify_id"] = file.SpotifyID
	}
	if file.SpotifyURL != "" {
		set["spotify_url"] = file.SpotifyURL
	}
	if file.ISRC != "" {
		set["isrc"] = file.ISRC
	}
	if file.DurationMS != 0 {
		set["duration_ms"] = file.DurationMS
	}
	if file.SourceProvider != "" {
		set["source_provider"] = file.SourceProvider
	}
	if file.SourceID != "" {
		set["source_id"] = file.SourceID
	}
	if file.Checksum != "" {
		set["checksum"] = file.Checksum
	}
	if file.Format != "" {
		set["format"] = file.Format
	}

	return bson.M{
		"$setOnInsert": bson.M{
			"_id":        id,
			"created_at": createdAt,
		},
		"$set": set,
	}
}

// MusicFileExist checks if a music file exists in the database
func (d *db) MusicFileExist(ctx context.Context, title string) (bool, error) {
	var count int64
	count, err := d.musicFilesCollection().CountDocuments(ctx, bson.M{"title": title})
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// Collections

// downloadQueueRequestCollection returns the download queue request collection
func (d *db) downloadQueueRequestCollection() *mongo.Collection {
	return d.conn.Database(d.dbname).Collection("download-queue-requests")
}

func (d *db) playlistsCollection() *mongo.Collection {
	return d.conn.Database(d.dbname).Collection("playlist-requests")
}

func (d *db) indexStatusCollection() *mongo.Collection {
	return d.conn.Database(d.dbname).Collection("index-status")
}

// musicFilesCollection returns the music files collection
func (d *db) musicFilesCollection() *mongo.Collection {
	return d.conn.Database(d.dbname).Collection("music-files")
}

// EnsureIndexes creates indexes used by lease claims and canonical catalog
// identity. Callers may retry this method after cleaning duplicate legacy
// records. NewDatabase logs and ignores failures so old data cannot prevent
// startup.
func (d *db) EnsureIndexes(ctx context.Context) error {
	var indexErrors []error
	database := d.conn.Database(d.dbname)

	_, err := database.Collection("download-queue-requests").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "active", Value: 1},
			{Key: "backend", Value: 1},
			{Key: "state", Value: 1},
			{Key: "next_attempt_at", Value: 1},
			{Key: "lease_expires_at", Value: 1},
			{Key: "errored", Value: 1},
			{Key: "created_at", Value: 1},
		},
		Options: options.Index().SetName("queue_claim_eligibility_v2"),
	})
	if err != nil {
		indexErrors = append(indexErrors, fmt.Errorf("create queue claim index: %w", err))
	}

	musicIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "spotify_id", Value: 1}},
			Options: options.Index().
				SetName("music_spotify_id_unique_sparse").
				SetUnique(true).
				SetSparse(true),
		},
		{
			Keys: bson.D{{Key: "checksum", Value: 1}},
			Options: options.Index().
				SetName("music_checksum_sparse").
				SetSparse(true),
		},
	}
	if _, err := database.Collection("music-files").Indexes().CreateMany(ctx, musicIndexes); err != nil {
		indexErrors = append(indexErrors, fmt.Errorf("create music identity indexes: %w", err))
	}

	return errors.Join(indexErrors...)
}

// escapeRegex escapes special regex characters in a string
func escapeRegex(s string) string {
	return regexp.QuoteMeta(s)
}

func (d *db) FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error) {
	if len(artists) != len(titles) {
		return nil, errors.New("artists and titles must have the same length")
	}
	if len(artists) == 0 {
		return []models.MusicFile{}, nil
	}

	orPairs := make([]bson.M, 0, len(artists))
	for i := range artists {
		// Use case-insensitive regex matching for both artist and title
		// This handles cases where database stores original case but we query with lowercase
		// Escape special regex characters to ensure exact matching
		escapedArtist := escapeRegex(artists[i])
		escapedTitle := escapeRegex(titles[i])
		orPairs = append(orPairs, bson.M{
			"$and": []bson.M{
				{"artist": bson.M{"$regex": "^" + escapedArtist + "$", "$options": "i"}},
				{"title": bson.M{"$regex": "^" + escapedTitle + "$", "$options": "i"}},
			},
		})
	}

	d.log.Info("Finding music files", zap.Any("orPairs", orPairs))

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

// FindMusicFilesForTracks prefers canonical Spotify/ISRC identity while
// retaining name lookup only for catalog rows that predate identity fields.
func (d *db) FindMusicFilesForTracks(
	ctx context.Context,
	tracks []spotify.TrackMetadata,
) ([]models.MusicFile, error) {
	if len(tracks) == 0 {
		return []models.MusicFile{}, nil
	}

	conditions := make(bson.A, 0, len(tracks)*3)
	for _, track := range tracks {
		if spotifyID := strings.TrimSpace(track.SpotifyID); spotifyID != "" {
			conditions = append(conditions, bson.M{"spotify_id": spotifyID})
		}
		if isrc := strings.TrimSpace(track.ISRC); isrc != "" {
			conditions = append(conditions, bson.M{"isrc": isrc})
		}
		artist, title := strings.TrimSpace(track.Artist), strings.TrimSpace(track.Title)
		if artist == "" || title == "" {
			continue
		}
		conditions = append(conditions, bson.M{
			"$and": bson.A{
				bson.M{"artist": bson.M{
					"$regex":   "^" + escapeRegex(artist) + "$",
					"$options": "i",
				}},
				bson.M{"title": bson.M{
					"$regex":   "^" + escapeRegex(title) + "$",
					"$options": "i",
				}},
				bson.M{"$or": bson.A{
					bson.M{"spotify_id": bson.M{"$exists": false}},
					bson.M{"spotify_id": nil},
					bson.M{"spotify_id": ""},
				}},
				bson.M{"$or": bson.A{
					bson.M{"isrc": bson.M{"$exists": false}},
					bson.M{"isrc": nil},
					bson.M{"isrc": ""},
				}},
			},
		})
	}
	if len(conditions) == 0 {
		return []models.MusicFile{}, nil
	}

	cursor, err := d.musicFilesCollection().Find(ctx, bson.M{"$or": conditions})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	files := make([]models.MusicFile, 0)
	for cursor.Next(ctx) {
		var file models.MusicFile
		if err := cursor.Decode(&file); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func (d *db) GetActiveRequest(ctx context.Context, url string) (models.DownloadQueueRequest, error) {
	cur := d.downloadQueueRequestCollection().FindOne(ctx, bson.M{"spotify_url": url, "active": true})
	var req models.DownloadQueueRequest
	if err := cur.Decode(&req); err != nil {
		return models.DownloadQueueRequest{}, err
	}

	return req, nil
}

func (d *db) GetIndexStatus(ctx context.Context) (models.IndexStatus, error) {
	var status models.IndexStatus
	err := d.indexStatusCollection().FindOne(ctx, bson.M{}).Decode(&status)
	if err != nil {
		return models.IndexStatus{}, err
	}

	return status, nil
}

func (d *db) UpdateIndexStatus(ctx context.Context, status models.IndexStatus) error {
	_, err := d.indexStatusCollection().UpdateOne(ctx, bson.M{}, bson.M{
		"$set": status,
	})
	if err != nil {
		return err
	}

	return nil
}
