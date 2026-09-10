package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/utils"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	spotifyapi "github.com/zmb3/spotify/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

var (
	ErrMissingFiles = errors.New("missing files")
)

const maxPlaylistAttempts = 5

func (s *service) ProcessPlaylistRequest(ctx context.Context) error {
	playlists, err := s.database.GetActivePlaylists(ctx)
	if err != nil {
		s.log.Error("failed to get active playlists", zap.Error(err))
		return err
	}

	s.log.Info("processing active playlists", zap.Any("playlists", len(playlists)))

	var processingErrors []error
	for index := range playlists {
		playlist := &playlists[index]
		processErr := s.ProcessPlaylist(ctx, playlist)
		if processErr != nil && ctx.Err() != nil &&
			(errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded)) {
			// A shutdown must not consume the playlist's finite retry budget. The
			// still-active row remains immediately eligible after the next start.
			processingErrors = append(
				processingErrors,
				fmt.Errorf("process playlist %s: %w", playlist.ID, processErr),
			)
			break
		}

		switch {
		case processErr == nil:
			playlist.Active = false
			playlist.Errored = false
			playlist.NextAttemptAt = 0
			playlist.LastError = nil
		case errors.Is(processErr, ErrMissingFiles):
			// Waiting for already-queued downloads is expected coordination,
			// not a failed playlist attempt.
			playlist.Active = true
			playlist.Errored = false
			playlist.NextAttemptAt = 0
			playlist.LastError = nil
			s.log.Info("playlist is waiting for missing tracks", zap.String("playlist_id", playlist.ID))
		default:
			s.log.Error("failed to process playlist", zap.Error(processErr), zap.Any("playlist", playlist))
			s.applyPlaylistFailure(playlist, processErr)
			processingErrors = append(
				processingErrors,
				fmt.Errorf("process playlist %s: %w", playlist.ID, processErr),
			)
		}

		playlist.UpdatedAt = time.Now().UTC().Unix()
		if err := s.database.UpdatePlaylistRequest(ctx, *playlist); err != nil {
			s.log.Error("failed to update playlist", zap.Error(err), zap.Any("playlist", playlist))
			processingErrors = append(
				processingErrors,
				fmt.Errorf("update playlist %s: %w", playlist.ID, err),
			)
		}
	}

	s.log.Info("completed processing of active playlists")
	return errors.Join(processingErrors...)
}

func (s *service) applyPlaylistFailure(playlist *models.PlaylistRequest, processErr error) {
	now := time.Now().UTC()
	retryDelay := s.retryDelay
	if retryDelay <= 0 {
		retryDelay = defaultRetryDelay
	}

	code := "playlist_processing"
	details := map[string]string{}
	var apiError *spotify.APIError
	if errors.As(processErr, &apiError) && apiError.StatusCode == http.StatusTooManyRequests {
		code = "spotify_rate_limited"
		if apiError.RetryAfter > retryDelay {
			retryDelay = apiError.RetryAfter
		}
		details["status_code"] = strconv.Itoa(apiError.StatusCode)
		if reason := strings.TrimSpace(apiError.Reason); reason != "" {
			details["reason"] = reason
		}
		if apiError.RetryAfter > 0 {
			details["retry_after_seconds"] = strconv.FormatInt(int64(apiError.RetryAfter/time.Second), 10)
		}
	}
	if len(details) == 0 {
		details = nil
	}

	playlist.Errored = true
	playlist.RetryCount++
	playlist.LastError = &models.DownloadRequestError{
		Code:       code,
		Stage:      "playlist",
		Message:    processErr.Error(),
		Retryable:  true,
		OccurredAt: now.Unix(),
		Details:    details,
	}

	if playlist.RetryCount >= maxPlaylistAttempts {
		playlist.Active = false
		playlist.NextAttemptAt = 0
		s.log.Error(
			"playlist retry budget exhausted",
			zap.String("playlist_id", playlist.ID),
			zap.String("error_code", code),
			zap.Int("retry_count", playlist.RetryCount),
		)
		return
	}

	playlist.Active = true
	playlist.NextAttemptAt = now.Add(retryDelay).Unix()
	s.log.Warn(
		"playlist retry scheduled",
		zap.String("playlist_id", playlist.ID),
		zap.String("error_code", code),
		zap.Int("retry_count", playlist.RetryCount),
		zap.Duration("retry_after", retryDelay),
		zap.Int64("next_attempt_at", playlist.NextAttemptAt),
	)
}

func (s *service) ProcessPlaylist(ctx context.Context, playlist *models.PlaylistRequest) error {
	s.log.Info("processing playlist", zap.Any("playlist", playlist))

	// checking if playlist is ready to be processed
	// by checking if we have active request for playlist download
	downloadRequest, err := s.database.GetActiveRequest(ctx, playlist.SpotifyURL)
	if err != nil && err != mongo.ErrNoDocuments {
		s.log.Error("failed to get active request", zap.Error(err))
		return err
	}

	if downloadRequest.Active {
		s.log.Info("download request is still active, will continue to process playlist once done", zap.Any("playlist", playlist))
		return ErrMissingFiles
	}

	playlistName := strings.TrimSpace(playlist.Name)
	if playlistName == "" {
		playlistName, err = s.spotifyService.GetObjectName(ctx, playlist.SpotifyURL)
		if err != nil {
			s.log.Error("failed to get playlist name", zap.Error(err))
			return err
		}
		playlist.Name = playlistName
	}

	songList, err := s.spotifyService.GetPlaylistTracks(ctx, playlist.SpotifyURL)
	if err != nil {
		s.log.Error("failed to get playlist data", zap.Error(err))
		return err
	}

	artists := []string{}
	titles := []string{}
	for _, item := range songList {
		if item.Track.Track == nil {
			s.log.Error("skipping empty track", zap.Any("item", item))
			continue
		}
		artist := []string{}
		for _, artistItem := range item.Track.Track.Artists {
			artist = append(artist, strings.ToLower(artistItem.Name))
		}

		artists = append(artists, strings.Join(artist, ", "))
		titles = append(titles, strings.ToLower(item.Track.Track.Name))
	}

	foundMusic, err := s.database.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		s.log.Error("failed to find music file paths", zap.Error(err))
		return err
	}

	if len(foundMusic) == 0 {
		s.log.Warn(
			"no catalog paths found for playlist; treating all tracks as missing",
			zap.String("playlist_name", playlistName),
		)
	}

	foundMusicMap := make(map[string]models.MusicFile)
	for _, music := range foundMusic {
		// Normalize to lowercase for consistent matching
		// Handle both single artist and comma-separated formats
		normalizedArtist := strings.ToLower(music.Artist)
		normalizedTitle := strings.ToLower(music.Title)
		key := normalizedArtist + " " + normalizedTitle
		foundMusicMap[key] = music
	}

	missingMusicFiles := []spotifyapi.PlaylistItem{}
	indexedPaths := make([]string, 0)
	for _, song := range songList {
		if song.Track.Track == nil {
			s.log.Error("skipping empty track", zap.Any("item", song))
			continue
		}
		artists := []string{}
		for _, artistItem := range song.Track.Track.Artists {
			artists = append(artists, strings.ToLower(artistItem.Name))
		}

		artist := strings.Join(artists, ", ")

		songName := strings.ToLower(song.Track.Track.Name)

		key := artist + " " + songName

		// Also try with single artist format (first artist) in case database stores it that way
		var singleArtistKey string
		if len(artists) > 0 {
			singleArtistKey = artists[0] + " " + songName
		}

		var foundFile models.MusicFile
		var found bool

		// Try comma-separated artist format first
		if file, ok := foundMusicMap[key]; ok {
			foundFile = file
			found = true
		} else if singleArtistKey != "" {
			// Try single artist format (database might store only first artist)
			if file, ok := foundMusicMap[singleArtistKey]; ok {
				foundFile = file
				found = true
			}
		}

		if !found {
			s.log.Error("song not found in indexed paths", zap.Any("artist", artist), zap.Any("songName", songName), zap.Any("singleArtist", singleArtistKey))
			missingMusicFiles = append(missingMusicFiles, song)
			// return errors.New("song not found in indexed paths")
			continue
		}

		indexedPaths = append(indexedPaths, foundFile.Path)
	}

	// if we tried to download the playlist but it failed then whatever
	if len(missingMusicFiles) > 0 && !playlist.NoPull {
		s.log.Error("missing music files", zap.Any("missingMusicFiles", missingMusicFiles))

		// Create individual download requests for each missing track
		createdCount := 0
		for _, missingItem := range missingMusicFiles {
			if missingItem.Track.Track == nil {
				continue
			}

			// Build track URL
			trackURL := fmt.Sprintf("https://open.spotify.com/track/%s", missingItem.Track.Track.ID)

			// Check if this specific track is already being downloaded or was downloaded
			alreadySynced, err := s.database.CheckIfRequestAlreadySynced(ctx, trackURL)
			if err != nil {
				s.log.Error("failed to check if track request already synced", zap.Error(err), zap.String("track_url", trackURL))
				continue
			}

			if alreadySynced {
				s.log.Info("skipping already synced track", zap.String("track_url", trackURL))
				continue
			}

			// Build track name
			artists := []string{}
			for _, artist := range missingItem.Track.Track.Artists {
				artists = append(artists, artist.Name)
			}
			trackName := fmt.Sprintf("%s - %s", strings.Join(artists, ", "), missingItem.Track.Track.Name)

			// Create download request for individual track
			objectType := spotify.SpotifyObjectTypeTrack
			if err := s.database.NewDownloadRequest(ctx, trackURL, trackName, 0, objectType); err != nil {
				s.log.Error("failed to add download request for track", zap.Error(err), zap.String("track_url", trackURL))
			} else {
				createdCount++
				s.log.Info("created download request for missing track", zap.String("track_url", trackURL), zap.String("track_name", trackName))
			}
		}

		if createdCount > 0 {
			s.log.Info("created download requests for missing tracks", zap.Int("count", createdCount), zap.Int("total_missing", len(missingMusicFiles)))
		} else {
			s.log.Info("missing tracks are already queued or could not be queued", zap.Int("total_missing", len(missingMusicFiles)))
		}
		return ErrMissingFiles
	}

	playlistPathName := utils.SanitizePlaylistName(playlistName)
	outputPath := filepath.Join(s.playlistsOutputPath, playlistPathName+".m3u")

	if err := utils.CreateM3UPlaylist(indexedPaths, s.libraryPath, outputPath); err != nil {
		s.log.Error("failed to create m3u playlist", zap.Error(err))
		return err
	}

	s.log.Info("created m3u playlist", zap.Any("outputPath", outputPath))

	return nil
}
