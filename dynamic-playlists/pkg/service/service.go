package service

import (
	"context"
	"time"

	models "github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"
)

type Service struct {
	db                ServiceDB
	playlistGenerator *PlaylistGenerator
	openAI            OpenAIClient
	log               *zap.Logger
}

type ServiceDB interface {
	GetActiveDynamicPlaylists(ctx context.Context) ([]models.DynamicPlaylist, error)
	GetAllGenreMappings(ctx context.Context) ([]models.GenreMapping, error)
}

type OpenAIClient interface {
	GeneratePlaylistDescription(ctx context.Context, query bson.M, sampleTracks []models.MusicFile) (string, string, error)
}

func NewService(db ServiceDB, playlistGenerator *PlaylistGenerator, openAI OpenAIClient, log *zap.Logger) *Service {
	return &Service{
		db:                db,
		playlistGenerator: playlistGenerator,
		openAI:            openAI,
		log:               log,
	}
}

// ProcessPlaylists processes all active dynamic playlists
func (s *Service) ProcessPlaylists(ctx context.Context) error {
	playlists, err := s.db.GetActiveDynamicPlaylists(ctx)
	if err != nil {
		return err
	}

	s.log.Info("Processing dynamic playlists", zap.Int("count", len(playlists)))

	for _, playlist := range playlists {
		// Check if refresh is needed
		if !s.shouldRefresh(playlist) {
			s.log.Info("Skipping playlist - not due for refresh",
				zap.String("playlist_id", playlist.ID),
				zap.String("refresh_frequency", playlist.RefreshFrequency),
				zap.Int64("last_refreshed", playlist.LastRefreshed))
			continue
		}

		// Generate description if missing
		if playlist.Description == "" {
			if err := s.generatePlaylistDescription(ctx, &playlist); err != nil {
				s.log.Error("Failed to generate playlist description", zap.Error(err), zap.String("playlist_id", playlist.ID))
				// Continue anyway
			}
		}

		// Generate playlist
		if err := s.playlistGenerator.GeneratePlaylist(ctx, playlist); err != nil {
			s.log.Error("Failed to generate playlist", zap.Error(err), zap.String("playlist_id", playlist.ID))
			continue
		}
	}

	return nil
}

func (s *Service) shouldRefresh(playlist models.DynamicPlaylist) bool {
	if playlist.LastRefreshed == 0 {
		return true // Never refreshed, refresh now
	}

	now := time.Now().Unix()
	lastRefresh := time.Unix(playlist.LastRefreshed, 0)

	switch playlist.RefreshFrequency {
	case "daily":
		return now-lastRefresh.Unix() >= 24*60*60
	case "weekly":
		return now-lastRefresh.Unix() >= 7*24*60*60
	default:
		// Default to daily
		return now-lastRefresh.Unix() >= 24*60*60
	}
}

func (s *Service) generatePlaylistDescription(ctx context.Context, playlist *models.DynamicPlaylist) error {
	// Get a few sample tracks to help OpenAI generate description
	// We need to access the database through the playlist generator
	sampleTracks, err := s.playlistGenerator.GetSampleTracks(ctx, playlist.Query, 5)
	if err != nil {
		return err
	}

	name, description, err := s.openAI.GeneratePlaylistDescription(ctx, playlist.Query, sampleTracks)
	if err != nil {
		return err
	}

	if name != "" {
		playlist.Name = name
	}
	playlist.Description = description

	return nil
}
