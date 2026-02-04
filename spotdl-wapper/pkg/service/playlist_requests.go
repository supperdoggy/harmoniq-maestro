package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func (s *service) ProcessPlaylistRequest(ctx context.Context) error {

	indexStatus, err := s.database.GetIndexStatus(ctx)
	if err != nil {
		s.log.Error("failed to get index status", zap.Error(err))
		return err
	}

	if indexStatus.LastUpdated > indexStatus.LastIndexed {
		s.log.Info("indexing in progress, skipping playlist processing")
		return nil
	}

	playlists, err := s.database.GetActivePlaylists(ctx)
	if err != nil {
		s.log.Error("failed to get active playlists", zap.Error(err))
		return err
	}

	s.log.Info("processing active playlists", zap.Any("playlists", len(playlists)))

	for _, playlist := range playlists {
		if err := s.ProcessPlaylist(ctx, playlist); err != nil {
			s.log.Error("failed to process playlist", zap.Error(err), zap.Any("playlist", playlist))
			playlist.Errored = true
			playlist.RetryCount++
		} else {
			playlist.Active = false
		}

		if playlist.RetryCount >= 5 {
			playlist.Active = false
		}

		if err := s.database.UpdatePlaylistRequest(ctx, playlist); err != nil {
			s.log.Error("failed to update playlist", zap.Error(err), zap.Any("playlist", playlist))
		}
	}

	s.log.Info("completed processing of active playlists")
	return nil
}

func (s *service) ProcessPlaylist(ctx context.Context, playlist models.PlaylistRequest) error {
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

	playlistName, err := s.spotifyService.GetObjectName(ctx, playlist.SpotifyURL)
	if err != nil {
		s.log.Error("failed to get playlist name", zap.Error(err))
		return err
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
		s.log.Error("no indexed paths found for playlist", zap.Any("playlistName", playlistName))
		return errors.New("no indexed paths found for playlist")
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
			return ErrMissingFiles
		} else {
			s.log.Info("all missing tracks already synced or failed to create requests", zap.Int("total_missing", len(missingMusicFiles)))
		}
	}

	for i, path := range indexedPaths {
		indexedPaths[i] = strings.ReplaceAll(path, "/mnt/music", "/music")
	}

	playlistPathName := strings.ReplaceAll(playlistName, "/", `-`)
	// playlistPathName = strings.ReplaceAll(playlistPathName, " ", `\ `)

	outputPath := s.destination + "/Playlists/" + playlistPathName + ".m3u"

	if err := utils.CreateM3UPlaylist(indexedPaths, s.libraryPath, outputPath); err != nil {
		s.log.Error("failed to create m3u playlist", zap.Error(err))
		return err
	}

	s.log.Info("created m3u playlist", zap.Any("outputPath", outputPath))

	return nil
}
