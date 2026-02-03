package service

import (
	"context"
	"errors"

	"github.com/supperdoggy/SmartHomeServer/music-services/spotdl-wapper/pkg/db"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
)

type Service interface {
	StartProcessing(ctx context.Context) error
}

type service struct {
	database       db.Database
	log            *zap.Logger
	spotifyService spotify.SpotifyService

	destination    string
	sleepInMinutes int
	libraryPath    string
}

func NewService(database db.Database, log *zap.Logger, spotifyService spotify.SpotifyService, destination, libraryPath string, sleepInMinutes int) Service {
	return &service{
		database:       database,
		log:            log,
		spotifyService: spotifyService,
		destination:    destination,
		sleepInMinutes: sleepInMinutes,
		libraryPath:    libraryPath,
	}
}

// StartProcessing starts the processing of the requests
func (s *service) StartProcessing(ctx context.Context) error {
	downloadError := s.ProcessDownloadRequest(ctx)

	playlistError := s.ProcessPlaylistRequest(ctx)

	return errors.Join(downloadError, playlistError)
}
