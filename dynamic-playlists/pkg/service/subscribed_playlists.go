package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	spotifyapi "github.com/zmb3/spotify/v2"
	"go.uber.org/zap"
)

type SubscribedPlaylistsDB interface {
	GetActiveSubscribedPlaylists(ctx context.Context) ([]models.SubscribedPlaylist, error)
	UpdateSubscribedPlaylist(ctx context.Context, playlist models.SubscribedPlaylist) error
	FindMusicFiles(ctx context.Context, artists, titles []string) ([]models.MusicFile, error)
	CheckIfRequestAlreadySynced(ctx context.Context, url string) (bool, error)
	NewDownloadRequest(ctx context.Context, url, name string, creatorID int64, objectType spotify.SpotifyObjectType) error
}

type SpotifyService interface {
	GetPlaylistTracks(ctx context.Context, url string) ([]spotifyapi.PlaylistItem, error)
	GetObjectName(ctx context.Context, url string) (string, error)
}

type SubscribedPlaylistsProcessor struct {
	db             SubscribedPlaylistsDB
	spotifyService SpotifyService
	musicRoot      string
	outputPath     string
	log            *zap.Logger
}

func NewSubscribedPlaylistsProcessor(db SubscribedPlaylistsDB, spotifyService SpotifyService, musicRoot, outputPath string, log *zap.Logger) *SubscribedPlaylistsProcessor {
	return &SubscribedPlaylistsProcessor{
		db:             db,
		spotifyService: spotifyService,
		musicRoot:      musicRoot,
		outputPath:     outputPath,
		log:            log,
	}
}

func (s *SubscribedPlaylistsProcessor) ProcessSubscribedPlaylists(ctx context.Context) error {
	playlists, err := s.db.GetActiveSubscribedPlaylists(ctx)
	if err != nil {
		return err
	}

	s.log.Info("Processing subscribed playlists", zap.Int("count", len(playlists)))

	for _, playlist := range playlists {
		if !s.shouldRefreshSubscription(playlist) {
			s.log.Info("Skipping subscription - not due for refresh",
				zap.String("playlist_id", playlist.ID),
				zap.String("refresh_interval", playlist.RefreshInterval),
				zap.Int64("last_synced", playlist.LastSynced))
			continue
		}

		if err := s.processSubscription(ctx, playlist); err != nil {
			s.log.Error("Failed to process subscription", zap.Error(err), zap.String("playlist_id", playlist.ID))
			continue
		}
	}

	return nil
}

func (s *SubscribedPlaylistsProcessor) shouldRefreshSubscription(playlist models.SubscribedPlaylist) bool {
	if playlist.LastSynced == 0 {
		return true // Never synced, sync now
	}

	now := time.Now().Unix()
	lastSync := playlist.LastSynced

	var intervalSeconds int64
	switch playlist.RefreshInterval {
	case "hourly":
		intervalSeconds = 60 * 60
	case "daily":
		intervalSeconds = 24 * 60 * 60
	case "weekly":
		intervalSeconds = 7 * 24 * 60 * 60
	default:
		// Default to daily
		intervalSeconds = 24 * 60 * 60
	}

	return now-lastSync >= intervalSeconds
}

func (s *SubscribedPlaylistsProcessor) processSubscription(ctx context.Context, playlist models.SubscribedPlaylist) error {
	s.log.Info("Processing subscription", zap.String("playlist_id", playlist.ID), zap.String("spotify_url", playlist.SpotifyURL))

	// Fetch tracks from Spotify
	songList, err := s.spotifyService.GetPlaylistTracks(ctx, playlist.SpotifyURL)
	if err != nil {
		return fmt.Errorf("failed to get playlist tracks: %w", err)
	}

	// Extract artists and titles
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

	// Find matching music files
	foundMusic, err := s.db.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		return fmt.Errorf("failed to find music files: %w", err)
	}

	if len(foundMusic) == 0 {
		s.log.Warn("no indexed paths found for playlist", zap.String("playlist_name", playlist.Name))
		// Continue anyway - we'll create download requests for all tracks
	}

	// Create map for quick lookup
	foundMusicMap := make(map[string]models.MusicFile)
	for _, music := range foundMusic {
		normalizedArtist := strings.ToLower(music.Artist)
		normalizedTitle := strings.ToLower(music.Title)
		key := normalizedArtist + " " + normalizedTitle
		foundMusicMap[key] = music
	}

	// Track missing songs and indexed paths with metadata
	missingMusicFiles := []spotifyapi.PlaylistItem{}
	indexedPaths := make([]string, 0)
	indexedFiles := make([]models.MusicFile, 0)
	for _, song := range songList {
		if song.Track.Track == nil {
			s.log.Error("skipping empty track", zap.Any("item", song))
			continue
		}
		songArtists := []string{}
		for _, artistItem := range song.Track.Track.Artists {
			songArtists = append(songArtists, strings.ToLower(artistItem.Name))
		}

		artist := strings.Join(songArtists, ", ")
		songName := strings.ToLower(song.Track.Track.Name)
		key := artist + " " + songName

		// Try single artist format (first artist) in case database stores it that way
		var singleArtistKey string
		if len(songArtists) > 0 {
			singleArtistKey = songArtists[0] + " " + songName
		}

		var foundFile models.MusicFile
		var found bool

		// Try comma-separated artist format first
		if file, ok := foundMusicMap[key]; ok {
			foundFile = file
			found = true
		} else if singleArtistKey != "" {
			// Try single artist format
			if file, ok := foundMusicMap[singleArtistKey]; ok {
				foundFile = file
				found = true
			}
		}

		if !found {
			s.log.Debug("song not found in indexed paths", zap.String("artist", artist), zap.String("songName", songName))
			missingMusicFiles = append(missingMusicFiles, song)
			continue
		}

		indexedPaths = append(indexedPaths, foundFile.Path)
		indexedFiles = append(indexedFiles, foundFile)
	}

	// Create download requests for missing songs if NoPull is false
	if len(missingMusicFiles) > 0 && !playlist.NoPull {
		s.log.Info("creating download requests for missing tracks", zap.Int("count", len(missingMusicFiles)))

		createdCount := 0
		for _, missingItem := range missingMusicFiles {
			if missingItem.Track.Track == nil {
				continue
			}

			// Build track URL
			trackURL := fmt.Sprintf("https://open.spotify.com/track/%s", missingItem.Track.Track.ID)

			// Check if this specific track is already being downloaded or was downloaded
			alreadySynced, err := s.db.CheckIfRequestAlreadySynced(ctx, trackURL)
			if err != nil {
				s.log.Error("failed to check if track request already synced", zap.Error(err), zap.String("track_url", trackURL))
				continue
			}

			if alreadySynced {
				s.log.Debug("skipping already synced track", zap.String("track_url", trackURL))
				continue
			}

			// Build track name
			trackArtists := []string{}
			for _, artist := range missingItem.Track.Track.Artists {
				trackArtists = append(trackArtists, artist.Name)
			}
			trackName := fmt.Sprintf("%s - %s", strings.Join(trackArtists, ", "), missingItem.Track.Track.Name)

			// Create download request for individual track
			objectType := spotify.SpotifyObjectTypeTrack
			if err := s.db.NewDownloadRequest(ctx, trackURL, trackName, 0, objectType); err != nil {
				s.log.Error("failed to add download request for track", zap.Error(err), zap.String("track_url", trackURL))
			} else {
				createdCount++
				s.log.Info("created download request for missing track", zap.String("track_url", trackURL), zap.String("track_name", trackName))
			}
		}

		if createdCount > 0 {
			s.log.Info("created download requests for missing tracks", zap.Int("count", createdCount), zap.Int("total_missing", len(missingMusicFiles)))
			// Don't return error - we'll still generate the M3U with available tracks
		}
	}

	// Generate M3U file
	if len(indexedFiles) == 0 {
		s.log.Warn("no tracks to include in M3U playlist", zap.String("playlist_id", playlist.ID))
		// Update playlist metadata anyway
		playlist.LastSynced = time.Now().Unix()
		playlist.LastTrackCount = 0
		playlist.UpdatedAt = time.Now().Unix()
		if err := s.db.UpdateSubscribedPlaylist(ctx, playlist); err != nil {
			return fmt.Errorf("failed to update playlist: %w", err)
		}
		return nil
	}

	playlistPathName := strings.ReplaceAll(playlist.Name, "/", `-`)
	outputPath := filepath.Join(s.outputPath, playlistPathName+".m3u")

	if err := s.createM3UPlaylist(indexedFiles, outputPath); err != nil {
		return fmt.Errorf("failed to create m3u playlist: %w", err)
	}

	s.log.Info("created m3u playlist", zap.String("outputPath", outputPath))

	// Update playlist metadata
	playlist.LastSynced = time.Now().Unix()
	playlist.LastTrackCount = len(indexedFiles)
	playlist.OutputPath = outputPath
	playlist.UpdatedAt = time.Now().Unix()

	if err := s.db.UpdateSubscribedPlaylist(ctx, playlist); err != nil {
		return fmt.Errorf("failed to update playlist: %w", err)
	}

	return nil
}

func (s *SubscribedPlaylistsProcessor) createM3UPlaylist(files []models.MusicFile, outputPath string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	// Remove existing file if it exists
	if _, err := os.Stat(outputPath); err == nil {
		if err := os.Remove(outputPath); err != nil {
			return err
		}
	}

	// Create M3U file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write M3U header
	if _, err := f.WriteString("#EXTM3U\n"); err != nil {
		return err
	}

	// Write tracks
	for _, file := range files {
		// Convert absolute path to relative path for M3U
		relPath, err := filepath.Rel(s.musicRoot, file.Path)
		if err != nil {
			// If relative path fails, use absolute path
			relPath = file.Path
		}

		// Format as /music/... path
		relPath = strings.ReplaceAll(relPath, "..", "")
		relPath = strings.TrimPrefix(relPath, "/")
		if !strings.HasPrefix(relPath, "/music/") {
			relPath = "/music/Job-downloaded/" + relPath
		}

		// Write EXTINF line
		extinf := "#EXTINF:-1," + file.Artist + " - " + file.Title + "\n"
		if _, err := f.WriteString(extinf); err != nil {
			return err
		}

		// Write path
		if _, err := f.WriteString(relPath + "\n"); err != nil {
			return err
		}
	}

	return nil
}
