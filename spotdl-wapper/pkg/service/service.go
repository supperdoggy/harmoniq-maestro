package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

const (
	defaultLeaseDuration = 45 * time.Minute
	defaultRetryDelay    = 15 * time.Minute
	defaultMaxAttempts   = 3
	maxHeartbeatInterval = 30 * time.Second
)

// Database is the storage contract used by the acquisition and playlist
// workers. Keeping it local to the service makes the orchestration testable
// without a MongoDB process.
type Database interface {
	ClaimNextActiveRequest(context.Context, string, string, time.Duration) (models.DownloadQueueRequest, error)
	RenewRequestLease(context.Context, string, string, string, time.Duration) error
	UpdateClaimedRequest(context.Context, models.DownloadQueueRequest, string, string) error
	ReleaseRequestLease(context.Context, string, string, string, models.DownloadRequestState) error

	FindMusicFiles(context.Context, []string, []string) ([]models.MusicFile, error)
	FindMusicFilesForTracks(context.Context, []spotify.TrackMetadata) ([]models.MusicFile, error)
	UpsertMusicFile(context.Context, models.MusicFile) (models.MusicFile, error)

	GetActivePlaylists(context.Context) ([]models.PlaylistRequest, error)
	UpdatePlaylistRequest(context.Context, models.PlaylistRequest) error
	GetActiveRequest(context.Context, string) (models.DownloadQueueRequest, error)
	CheckIfRequestAlreadySynced(context.Context, string) (bool, error)
	NewDownloadRequest(context.Context, string, string, int64, spotify.SpotifyObjectType) error
}

// AssetImporter validates, tags, and publishes one provider result.
type AssetImporter interface {
	Import(context.Context, acquisition.TrackSpec, acquisition.AssetResult) (models.MusicFile, error)
	Discard(acquisition.AssetResult) error
}

type Service interface {
	StartProcessing(ctx context.Context) error
}

// Options controls orchestration behavior independently of provider details.
type Options struct {
	WorkerID            string
	LeaseDuration       time.Duration
	RetryDelay          time.Duration
	MaxAttempts         int
	RequestDelay        time.Duration
	PlaylistsOutputPath string
	LibraryPath         string
}

type service struct {
	database       Database
	log            *zap.Logger
	spotifyService spotify.SpotifyService
	provider       acquisition.Provider
	importer       AssetImporter

	workerID            string
	leaseDuration       time.Duration
	retryDelay          time.Duration
	maxAttempts         int
	requestDelay        time.Duration
	playlistsOutputPath string
	libraryPath         string
}

func NewService(
	database Database,
	log *zap.Logger,
	spotifyService spotify.SpotifyService,
	provider acquisition.Provider,
	importer AssetImporter,
	options Options,
) (Service, error) {
	if database == nil {
		return nil, errors.New("service database is required")
	}
	if spotifyService == nil {
		return nil, errors.New("Spotify metadata service is required")
	}
	if provider == nil {
		return nil, errors.New("acquisition provider is required")
	}
	if importer == nil {
		return nil, errors.New("library importer is required")
	}
	if strings.TrimSpace(options.WorkerID) == "" {
		return nil, errors.New("worker ID is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.LeaseDuration < 3*time.Second {
		return nil, errors.New("lease duration must be at least 3s")
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = defaultRetryDelay
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = defaultMaxAttempts
	}
	if options.RequestDelay < 0 {
		return nil, errors.New("request delay must not be negative")
	}
	if strings.TrimSpace(options.PlaylistsOutputPath) == "" {
		return nil, errors.New("playlists output path is required")
	}
	if strings.TrimSpace(options.LibraryPath) == "" {
		return nil, errors.New("music library path is required")
	}

	return &service{
		database:            database,
		log:                 log,
		spotifyService:      spotifyService,
		provider:            provider,
		importer:            importer,
		workerID:            options.WorkerID,
		leaseDuration:       options.LeaseDuration,
		retryDelay:          options.RetryDelay,
		maxAttempts:         options.MaxAttempts,
		requestDelay:        options.RequestDelay,
		playlistsOutputPath: options.PlaylistsOutputPath,
		libraryPath:         options.LibraryPath,
	}, nil
}

// StartProcessing runs one drain of each queue. The process-level polling loop
// calls this repeatedly; a pass itself never sleeps after both queues are idle.
func (s *service) StartProcessing(ctx context.Context) error {
	downloadError := s.ProcessDownloadRequest(ctx)
	if ctx.Err() != nil {
		return downloadError
	}
	playlistError := s.ProcessPlaylistRequest(ctx)
	return errors.Join(downloadError, playlistError)
}

func isNoWork(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

func wrapRequestError(requestID string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("process download request %s: %w", requestID, err)
}
