package service

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
)

func (s *service) ProcessDownloadRequest(ctx context.Context) error {
	active, err := s.database.GetActiveRequests(ctx)
	if err != nil {
		s.log.Error("failed to get active requests", zap.Error(err))
		return err
	}

	if len(active) == 0 {
		s.log.Info("no active requests to process")
		return nil
	}

	s.log.Info("processing active requests", zap.Any("requests", len(active)))

	// sort active by errored so errored requests are processed last
	sort.Slice(active, func(i, j int) bool {
		if active[i].Errored && !active[j].Errored {
			return false
		}
		if !active[i].Errored && active[j].Errored {
			return true
		}
		return active[i].CreatedAt < active[j].CreatedAt
	})

	s.log.Info("sorted active requests", zap.Any("requests", active))

	for _, request := range active {
		time.Sleep(time.Duration(s.sleepInMinutes) * time.Minute)

		request.SyncCount++
		if err := s.ProcessRequest(ctx, request); err != nil {
			s.log.Error("failed to process request", zap.Error(err), zap.Any("request", request))
			request.Errored = true
			request.RetryCount++
			s.log.Warn("request processing encountered an error", zap.Any("request", request))
		}

		// Re-fetch request to get updated track metadata (Found/Skipped status)
		updatedRequest, err := s.database.GetActiveRequest(ctx, request.SpotifyURL)
		if err != nil {
			s.log.Error("failed to re-fetch request", zap.Error(err))
		} else {
			request.TrackMetadata = updatedRequest.TrackMetadata
			request.FoundTrackCount = updatedRequest.FoundTrackCount
		}

		// Check if all non-skipped tracks are found (early completion)
		if s.isRequestComplete(request) {
			s.log.Info("all non-skipped tracks found, marking request as complete",
				zap.String("request_id", request.ID))
			request.Active = false
		}

		// Fallback: deactivate after max sync attempts
		if request.SyncCount >= 3 {
			request.Active = false
		}

		s.log.Info("updated request status", zap.Any("request", request))

		if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
			s.log.Error("failed to update request", zap.Error(err), zap.Any("request", request))
		}
	}

	indexStatus, err := s.database.GetIndexStatus(ctx)
	if err != nil {
		s.log.Error("failed to get index status", zap.Error(err))
		return err
	}

	indexStatus.LastUpdated = time.Now().UTC().Unix()
	if err := s.database.UpdateIndexStatus(ctx, indexStatus); err != nil {
		s.log.Error("failed to update index status", zap.Error(err))
		return err
	}

	s.log.Info("completed processing of active requests")
	return nil
}

// ProcessRequest processes the request
func (s *service) ProcessRequest(ctx context.Context, request models.DownloadQueueRequest) error {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("recovered from panic", zap.Any("panic", r))
		}
	}()

	// Fetch track count and metadata if not already set
	if request.ExpectedTrackCount == 0 || len(request.TrackMetadata) == 0 {
		s.log.Info("fetching track count and metadata", zap.String("url", request.SpotifyURL))
		trackCount, trackMetadata, err := s.spotifyService.GetTrackCount(ctx, request.SpotifyURL)
		if err != nil {
			s.log.Error("failed to get track count", zap.Error(err), zap.String("url", request.SpotifyURL))
			// Continue anyway, we'll just not have progress tracking
		} else {
			request.ExpectedTrackCount = trackCount
			request.TrackMetadata = trackMetadata
			request.UpdatedAt = time.Now().Unix()
			if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
				s.log.Error("failed to update request with track count", zap.Error(err))
			}
			s.log.Info("fetched track count", zap.Int("count", trackCount), zap.String("url", request.SpotifyURL))
		}
	}

	// Check if this is a playlist - if so, use individual track download logic
	// Use stored ObjectType if available, otherwise fetch from API (for backward compatibility)
	objectType := request.ObjectType
	if objectType == "" {
		var err error
		objectType, err = s.spotifyService.GetObjectType(ctx, request.SpotifyURL)
		if err != nil {
			s.log.Error("failed to get object type", zap.Error(err), zap.String("url", request.SpotifyURL))
			// Fall back to old behavior if we can't determine type
			objectType = ""
		} else {
			// Update the request with the fetched object type for future use
			request.ObjectType = objectType
			request.UpdatedAt = time.Now().Unix()
			if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
				s.log.Error("failed to update request with object type", zap.Error(err))
			}
		}
	}

	if objectType == spotify.SpotifyObjectTypePlaylist && len(request.TrackMetadata) > 0 {
		// For playlists: pre-check DB and download missing tracks individually
		return s.processPlaylistRequest(ctx, request)
	}

	// For albums/tracks: use the original bulk download method
	return s.processBulkDownload(ctx, request)
}

// processPlaylistRequest handles playlist downloads with individual track checking
func (s *service) processPlaylistRequest(ctx context.Context, request models.DownloadQueueRequest) error {
	s.log.Info("processing playlist request with individual track downloads", zap.String("url", request.SpotifyURL))

	// Pre-check: which tracks already exist in the database?
	if err := s.preCheckTracksInDB(ctx, &request); err != nil {
		s.log.Error("failed to pre-check tracks in database", zap.Error(err))
		// Continue anyway, we'll just download everything
	} else {
		// Update database with pre-check results
		request.UpdatedAt = time.Now().Unix()
		if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
			s.log.Error("failed to update request after pre-check", zap.Error(err))
		}
	}

	// Download missing tracks individually
	for i := range request.TrackMetadata {
		track := &request.TrackMetadata[i]

		// Skip tracks that are already found or skipped
		if track.Found || track.Skipped {
			continue
		}

		// Skip tracks without SpotifyURL (shouldn't happen, but be safe)
		if track.SpotifyURL == "" {
			s.log.Warn("track missing SpotifyURL, skipping", zap.String("artist", track.Artist), zap.String("title", track.Title))
			track.Skipped = true
			continue
		}

		s.log.Info("downloading individual track", zap.String("url", track.SpotifyURL), zap.String("artist", track.Artist), zap.String("title", track.Title))

		// Download the track
		if err := s.DownloadSingleTrack(ctx, track.SpotifyURL); err != nil {
			s.log.Error("failed to download track", zap.Error(err), zap.String("url", track.SpotifyURL))
			track.FailedAttempts++
			if track.FailedAttempts >= spotify.MaxFailedAttempts {
				track.Skipped = true
				s.log.Warn("marking track as skipped after max failed attempts",
					zap.String("artist", track.Artist),
					zap.String("title", track.Title),
					zap.Int("failed_attempts", track.FailedAttempts))
			}
		} else {
			// After download, check if track now exists in DB
			if err := s.checkSingleTrackInDB(ctx, track); err != nil {
				s.log.Error("failed to check track in database after download", zap.Error(err))
				// Don't mark as found if check fails, will retry next sync
			}
		}

		// Update request after each track to persist progress
		request.UpdatedAt = time.Now().Unix()
		if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
			s.log.Error("failed to update request after track download", zap.Error(err))
		}
	}

	// Final update of found track count
	if err := s.UpdateFoundTrackCount(ctx, request); err != nil {
		s.log.Error("failed to update found track count", zap.Error(err))
	}

	return nil
}

// processBulkDownload handles album/track downloads using the original bulk method
func (s *service) processBulkDownload(ctx context.Context, request models.DownloadQueueRequest) error {
	s.log.Info("processing bulk download request", zap.String("url", request.SpotifyURL))

	// Build output format with destination path
	args := []string{
		request.SpotifyURL,
		"--output", s.destination,
		"--config",
		"--no-cache",
		"--sync-without-deleting",
	}

	// Run the "spotdl --sync {url}" command
	cmd := exec.Command("spotdl", args...)
	// Kill child process when parent dies
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	s.log.Info("executing command", zap.String("command", cmd.String()))

	// Capture stdout and stderr through the logger
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Stream stdout and stderr to logger
	go s.streamOutput(stdout, "stdout")
	go s.streamOutput(stderr, "stderr")

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return err
	}

	// After download completes, compare with indexed files
	if request.ExpectedTrackCount > 0 && len(request.TrackMetadata) > 0 {
		if err := s.UpdateFoundTrackCount(ctx, request); err != nil {
			s.log.Error("failed to update found track count", zap.Error(err))
			// Don't fail the request, just log the error
		}
	}

	return nil
}

// preCheckTracksInDB checks which tracks already exist in the database and marks them as Found
func (s *service) preCheckTracksInDB(ctx context.Context, request *models.DownloadQueueRequest) error {
	if len(request.TrackMetadata) == 0 {
		return nil
	}

	// Extract artists and titles from all tracks
	artists := make([]string, 0, len(request.TrackMetadata))
	titles := make([]string, 0, len(request.TrackMetadata))
	for _, track := range request.TrackMetadata {
		artists = append(artists, track.Artist)
		titles = append(titles, track.Title)
	}

	// Find matching music files in the database
	foundMusic, err := s.database.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		return err
	}

	// Create a map for quick lookup
	foundMap := make(map[string]bool)
	for _, music := range foundMusic {
		key := strings.ToLower(music.Artist) + " " + strings.ToLower(music.Title)
		foundMap[key] = true
	}

	// Mark tracks that already exist as Found
	foundCount := 0
	for i := range request.TrackMetadata {
		track := &request.TrackMetadata[i]
		key := strings.ToLower(track.Artist) + " " + strings.ToLower(track.Title)
		if foundMap[key] {
			track.Found = true
			track.FailedAttempts = 0
			foundCount++
		}
	}

	request.FoundTrackCount = foundCount
	s.log.Info("pre-checked tracks in database",
		zap.String("request_id", request.ID),
		zap.Int("total_tracks", len(request.TrackMetadata)),
		zap.Int("already_downloaded", foundCount),
		zap.Int("need_download", len(request.TrackMetadata)-foundCount))

	return nil
}

// checkSingleTrackInDB checks if a single track exists in the database after download
func (s *service) checkSingleTrackInDB(ctx context.Context, track *spotify.TrackMetadata) error {
	artists := []string{track.Artist}
	titles := []string{track.Title}

	foundMusic, err := s.database.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		return err
	}

	// Check if we found a match
	for _, music := range foundMusic {
		key := strings.ToLower(music.Artist) + " " + strings.ToLower(music.Title)
		trackKey := strings.ToLower(track.Artist) + " " + strings.ToLower(track.Title)
		if key == trackKey {
			track.Found = true
			track.FailedAttempts = 0
			return nil
		}
	}

	// Track not found yet - might need indexing, will be checked on next sync
	return nil
}

// UpdateFoundTrackCount compares indexed files with expected tracks and updates individual track status
func (s *service) UpdateFoundTrackCount(ctx context.Context, request models.DownloadQueueRequest) error {
	if len(request.TrackMetadata) == 0 {
		return nil
	}

	// Extract artists and titles from non-skipped track metadata
	artists := make([]string, 0, len(request.TrackMetadata))
	titles := make([]string, 0, len(request.TrackMetadata))
	for _, track := range request.TrackMetadata {
		if !track.Skipped {
			artists = append(artists, track.Artist)
			titles = append(titles, track.Title)
		}
	}

	// Find matching music files in the database
	foundMusic, err := s.database.FindMusicFiles(ctx, artists, titles)
	if err != nil {
		return err
	}

	// Create a map for quick lookup
	foundMap := make(map[string]bool)
	for _, music := range foundMusic {
		key := strings.ToLower(music.Artist) + " " + strings.ToLower(music.Title)
		foundMap[key] = true
	}

	// Update individual track status
	foundCount := 0
	skippedCount := 0
	for i := range request.TrackMetadata {
		track := &request.TrackMetadata[i]

		// Skip already skipped tracks
		if track.Skipped {
			skippedCount++
			continue
		}

		key := strings.ToLower(track.Artist) + " " + strings.ToLower(track.Title)
		if foundMap[key] {
			track.Found = true
			track.FailedAttempts = 0 // reset on success
			foundCount++
		} else {
			track.Found = false
			track.FailedAttempts++

			// Mark as skipped (stuck) after max failed attempts
			if track.FailedAttempts >= spotify.MaxFailedAttempts {
				track.Skipped = true
				skippedCount++
				s.log.Warn("marking track as skipped (stuck)",
					zap.String("artist", track.Artist),
					zap.String("title", track.Title),
					zap.Int("failed_attempts", track.FailedAttempts))
			}
		}
	}

	// Update the request with found count
	request.FoundTrackCount = foundCount
	request.UpdatedAt = time.Now().Unix()

	if err := s.database.UpdateActiveRequest(ctx, request); err != nil {
		return err
	}

	// Calculate effective expected count (excluding skipped)
	effectiveExpected := request.ExpectedTrackCount - skippedCount

	s.log.Info("updated found track count",
		zap.String("request_id", request.ID),
		zap.Int("expected", request.ExpectedTrackCount),
		zap.Int("effective_expected", effectiveExpected),
		zap.Int("found", foundCount),
		zap.Int("skipped", skippedCount),
		zap.Float64("percentage", float64(foundCount)/float64(max(effectiveExpected, 1))*100))

	return nil
}

// isRequestComplete checks if all non-skipped tracks have been found
func (s *service) isRequestComplete(request models.DownloadQueueRequest) bool {
	if len(request.TrackMetadata) == 0 {
		return false
	}

	for _, track := range request.TrackMetadata {
		// If a track is not found and not skipped, request is not complete
		if !track.Found && !track.Skipped {
			return false
		}
	}

	return true
}

// streamOutput reads from a pipe and logs each line
func (s *service) streamOutput(pipe io.ReadCloser, stream string) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			s.log.Info("spotdl", zap.String("stream", stream), zap.String("output", line))
		}
	}
}

// DownloadSingleTrack downloads a single track using spotdl
func (s *service) DownloadSingleTrack(ctx context.Context, trackURL string) error {
	args := []string{
		trackURL,
		"--output", s.destination,
		"--config",
		"--no-cache",
	}

	cmd := exec.Command("spotdl", args...)
	// Kill child process when parent dies
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	s.log.Info("executing spotdl for single track", zap.String("url", trackURL))

	// Capture stdout and stderr through the logger
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Stream stdout and stderr to logger
	go s.streamOutput(stdout, "stdout")
	go s.streamOutput(stderr, "stderr")

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}
