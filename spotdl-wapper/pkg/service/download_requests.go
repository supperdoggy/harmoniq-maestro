package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/library"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
)

type requestFailure struct {
	code      string
	stage     string
	retryable bool
	review    bool
	// preserveBudget is used after a validated file has already been
	// published. Catalog finalization must remain retryable without exhausting
	// the acquisition attempt budget and stranding that journaled file.
	preserveBudget bool
	retryAfter     time.Duration
	track          *spotify.TrackMetadata
	err            error
}

func (f *requestFailure) Error() string {
	return f.err.Error()
}

func (f *requestFailure) Unwrap() error {
	return f.err
}

// ProcessDownloadRequest drains all currently eligible requests. Each request
// is atomically claimed, renewed while in flight, and released into an
// explicit terminal/retry/review state.
func (s *service) ProcessDownloadRequest(ctx context.Context) error {
	var processingErrors []error
	processed := 0

	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(processingErrors, err)...)
		}

		request, err := s.database.ClaimNextActiveRequest(
			ctx,
			s.workerID,
			string(s.provider.Name()),
			s.leaseDuration,
		)
		if isNoWork(err) {
			return errors.Join(processingErrors...)
		}
		if err != nil {
			return errors.Join(append(processingErrors, fmt.Errorf("claim next download request: %w", err))...)
		}

		delay := time.Duration(0)
		if processed > 0 {
			delay = s.requestDelay
		}
		processed++

		if err := s.processClaimedRequest(ctx, &request, delay); err != nil {
			processingErrors = append(processingErrors, wrapRequestError(request.ID, err))
		}
	}
}

func (s *service) processClaimedRequest(
	ctx context.Context,
	request *models.DownloadQueueRequest,
	delay time.Duration,
) error {
	request.SyncCount++
	request.Backend = string(s.provider.Name())
	request.LastError = nil

	processingCtx, cancelProcessing := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatErr := s.renewLease(processingCtx, request.ID, request.ClaimID)
		if heartbeatErr != nil && !errors.Is(heartbeatErr, context.Canceled) {
			// A stale worker must stop its downloader/importer as soon as it no
			// longer owns the request.
			cancelProcessing()
		}
		heartbeatDone <- heartbeatErr
	}()

	var processErr error
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-processingCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			processErr = processingCtx.Err()
		case <-timer.C:
		}
	}
	if processErr == nil {
		processErr = s.acquireRequest(processingCtx, request)
	}
	cancelProcessing()
	heartbeatErr := <-heartbeatDone
	if ctx.Err() != nil {
		processErr = ctx.Err()
	} else if heartbeatErr != nil && !errors.Is(heartbeatErr, context.Canceled) {
		processErr = &requestFailure{
			code:      "lease_lost",
			stage:     "claim",
			retryable: true,
			err:       heartbeatErr,
		}
	}

	if processErr == nil {
		request.State = models.DownloadRequestStateCompleted
		request.Active = false
		request.Errored = false
		request.NextAttemptAt = 0
		request.LastError = nil
	} else {
		s.applyFailure(request, processErr)
	}

	updateCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		updateCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	}
	defer cancel()

	if err := s.database.UpdateClaimedRequest(
		updateCtx,
		*request,
		s.workerID,
		request.ClaimID,
	); err != nil {
		return errors.Join(processErr, fmt.Errorf("persist final request state: %w", err))
	}
	if err := s.database.ReleaseRequestLease(
		updateCtx,
		request.ID,
		s.workerID,
		request.ClaimID,
		request.State,
	); err != nil {
		return errors.Join(processErr, fmt.Errorf("release request lease: %w", err))
	}

	if processErr != nil {
		s.log.Warn(
			"download request did not complete",
			zap.String("request_id", request.ID),
			zap.String("state", string(request.State)),
			zap.String("backend", request.Backend),
			zap.Error(processErr),
		)
	}
	return processErr
}

func (s *service) renewLease(ctx context.Context, requestID, claimID string) error {
	interval := s.leaseDuration / 3
	if interval < time.Second {
		interval = time.Second
	}
	if interval > maxHeartbeatInterval {
		interval = maxHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.database.RenewRequestLease(
				ctx,
				requestID,
				s.workerID,
				claimID,
				s.leaseDuration,
			); err != nil {
				return fmt.Errorf("renew lease: %w", err)
			}
		}
	}
}

func (s *service) acquireRequest(ctx context.Context, request *models.DownloadQueueRequest) error {
	if len(request.TrackMetadata) == 0 {
		if err := s.transition(ctx, request, models.DownloadRequestStateResolving); err != nil {
			return failure("persist_progress", "resolving", true, false, nil, err)
		}

		trackCount, metadata, err := s.spotifyService.GetTrackCount(ctx, request.SpotifyURL)
		if err != nil {
			return s.metadataFailure(err)
		}
		if len(metadata) == 0 {
			return failure(
				"empty_metadata",
				"resolving",
				false,
				true,
				nil,
				fmt.Errorf("Spotify returned %d items but no usable tracks", trackCount),
			)
		}
		request.TrackMetadata = metadata
		// Unavailable, local, or non-track playlist items are filtered during
		// metadata resolution and must not make completion mathematically
		// impossible.
		request.ExpectedTrackCount = len(metadata)
		if request.ObjectType == "" {
			if objectType, typeErr := s.spotifyService.GetObjectType(ctx, request.SpotifyURL); typeErr == nil {
				request.ObjectType = objectType
			}
		}
		if err := s.persist(ctx, request); err != nil {
			return failure("persist_metadata", "resolving", true, false, nil, err)
		}
	}

	if request.ExpectedTrackCount != len(request.TrackMetadata) {
		request.ExpectedTrackCount = len(request.TrackMetadata)
	}
	if err := s.preCheckTracksInDB(ctx, request); err != nil {
		return failure("catalog_lookup", "resolving", true, false, nil, err)
	}
	if err := s.persist(ctx, request); err != nil {
		return failure("persist_progress", "resolving", true, false, nil, err)
	}

	for index := range request.TrackMetadata {
		track := &request.TrackMetadata[index]
		if track.Found {
			continue
		}
		if track.Skipped {
			return failure(
				"legacy_skipped_track",
				"resolving",
				false,
				true,
				track,
				errors.New("legacy worker marked this unresolved track as skipped"),
			)
		}
		if strings.TrimSpace(track.SpotifyURL) == "" {
			return failure(
				"missing_track_url",
				"resolving",
				false,
				true,
				track,
				errors.New("track metadata is missing its canonical Spotify URL"),
			)
		}

		resumed, err := s.resumePendingImport(ctx, request, track)
		if err != nil {
			track.FailedAttempts++
			track.AcquisitionError = err.Error()
			request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
			_ = s.persist(ctx, request)
			return err
		}
		if resumed {
			continue
		}

		if err := s.acquireTrack(ctx, request, track); err != nil {
			track.FailedAttempts++
			track.AcquisitionError = err.Error()
			request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
			_ = s.persist(ctx, request)
			return err
		}
	}

	request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
	if request.FoundTrackCount != len(request.TrackMetadata) {
		return failure(
			"incomplete_request",
			"imported",
			true,
			false,
			nil,
			fmt.Errorf("imported %d of %d tracks", request.FoundTrackCount, len(request.TrackMetadata)),
		)
	}
	return nil
}

func (s *service) acquireTrack(
	ctx context.Context,
	request *models.DownloadQueueRequest,
	track *spotify.TrackMetadata,
) error {
	if track.SpotifyID == "" {
		track.SpotifyID = spotifyIDFromTrackURL(track.SpotifyURL)
	}
	spec := trackSpec(*track)

	if err := s.transition(ctx, request, models.DownloadRequestStateResolving); err != nil {
		return failure("persist_progress", "resolving", true, false, track, err)
	}
	candidates, err := s.provider.Resolve(ctx, spec)
	if err != nil {
		if errors.Is(err, acquisition.ErrNoCandidates) {
			return failure("no_acceptable_candidate", "resolving", false, true, track, err)
		}
		return failure("provider_resolve", "resolving", true, false, track, err)
	}
	if len(candidates) == 0 {
		return failure(
			"no_acceptable_candidate",
			"resolving",
			false,
			true,
			track,
			acquisition.ErrNoCandidates,
		)
	}

	if err := s.transition(ctx, request, models.DownloadRequestStateDownloading); err != nil {
		return failure("persist_progress", "downloading", true, false, track, err)
	}
	asset, err := s.provider.Acquire(ctx, spec, candidates[0])
	if err != nil {
		if errors.Is(err, acquisition.ErrMissingFinalPath) ||
			errors.Is(err, acquisition.ErrUnsafeFinalPath) {
			return failure("provider_contract", "downloading", false, true, track, err)
		}
		return failure("provider_acquire", "downloading", true, false, track, err)
	}
	assetOwned := true
	defer func() {
		if !assetOwned {
			return
		}
		if discardErr := s.importer.Discard(asset); discardErr != nil {
			s.log.Warn(
				"failed to discard staged provider asset",
				zap.String("request_id", request.ID),
				zap.String("path", asset.FinalPath),
				zap.Error(discardErr),
			)
		}
	}()

	if err := s.transition(ctx, request, models.DownloadRequestStateValidating); err != nil {
		return failure("persist_progress", "validating", true, false, track, err)
	}
	catalogFile, err := s.importer.Import(ctx, spec, asset)
	if err != nil {
		needsReview := errors.Is(err, library.ErrDurationMismatch) ||
			errors.Is(err, library.ErrChecksumMismatch) ||
			errors.Is(err, library.ErrInvalidAsset) ||
			errors.Is(err, library.ErrUnsafeOutputPath) ||
			errors.Is(err, library.ErrPublishCollision)
		return failure("media_validation", "validating", !needsReview, needsReview, track, err)
	}
	// A successful import has consumed the staged asset. The canonical file is
	// now owned by the library/catalog workflow.
	assetOwned = false
	if err := s.finalizeImportedTrack(
		ctx,
		request,
		track,
		catalogFile,
		asset.SourceURL,
	); err != nil {
		return err
	}

	s.log.Info(
		"imported track",
		zap.String("request_id", request.ID),
		zap.String("spotify_id", track.SpotifyID),
		zap.String("provider", catalogFile.SourceProvider),
		zap.String("source_id", catalogFile.SourceID),
		zap.String("path", catalogFile.Path),
	)
	return nil
}

func (s *service) finalizeImportedTrack(
	ctx context.Context,
	request *models.DownloadQueueRequest,
	track *spotify.TrackMetadata,
	catalogFile models.MusicFile,
	sourceURL string,
) error {
	// Persist a recovery journal before the catalog mutation. If MongoDB
	// rejects the upsert, the next attempt can validate and resume this exact
	// published file without downloading or tagging it again.
	track.Found = false
	track.Skipped = false
	track.SourceProvider = catalogFile.SourceProvider
	track.SourceID = catalogFile.SourceID
	track.FinalPath = catalogFile.Path
	track.Format = catalogFile.Format
	track.Checksum = catalogFile.Checksum
	track.MatchScore = catalogFile.MatchScore
	request.Result = &models.DownloadRequestResult{
		Provider:   catalogFile.SourceProvider,
		SourceID:   catalogFile.SourceID,
		SourceURL:  sourceURL,
		FinalPath:  catalogFile.Path,
		Format:     catalogFile.Format,
		Checksum:   catalogFile.Checksum,
		MatchScore: catalogFile.MatchScore,
		ImportedAt: time.Now().UTC().Unix(),
	}
	if err := s.transition(ctx, request, models.DownloadRequestStateImported); err != nil {
		return finalizationFailure("persist_import_journal", track, err)
	}
	if err := s.database.RenewRequestLease(
		ctx,
		request.ID,
		s.workerID,
		request.ClaimID,
		s.leaseDuration,
	); err != nil {
		return finalizationFailure("lease_lost", track, err)
	}
	catalogFile, err := s.database.UpsertMusicFile(ctx, catalogFile)
	if err != nil {
		return finalizationFailure("catalog_upsert", track, err)
	}

	track.Found = true
	track.Skipped = false
	track.FailedAttempts = 0
	track.AcquisitionError = ""
	track.SourceProvider = catalogFile.SourceProvider
	track.SourceID = catalogFile.SourceID
	track.FinalPath = catalogFile.Path
	track.Format = catalogFile.Format
	track.Checksum = catalogFile.Checksum
	track.MatchScore = catalogFile.MatchScore
	request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
	request.Result.CatalogID = catalogFile.ID
	if err := s.transition(ctx, request, models.DownloadRequestStateImported); err != nil {
		return finalizationFailure("persist_catalog_result", track, err)
	}
	return nil
}

func (s *service) resumePendingImport(
	ctx context.Context,
	request *models.DownloadQueueRequest,
	track *spotify.TrackMetadata,
) (bool, error) {
	if strings.TrimSpace(track.FinalPath) == "" ||
		strings.TrimSpace(track.Checksum) == "" ||
		strings.TrimSpace(track.SourceProvider) == "" {
		return false, nil
	}

	sourceURL := ""
	if request.Result != nil && request.Result.FinalPath == track.FinalPath {
		sourceURL = request.Result.SourceURL
	}
	now := time.Now().UTC().Unix()
	pending := models.MusicFile{
		Artist:         strings.ToLower(track.Artist),
		Album:          strings.ToLower(track.Album),
		Title:          strings.ToLower(track.Title),
		SpotifyID:      track.SpotifyID,
		SpotifyURL:     track.SpotifyURL,
		ISRC:           track.ISRC,
		DurationMS:     track.DurationMS,
		SourceProvider: track.SourceProvider,
		SourceID:       track.SourceID,
		MatchScore:     track.MatchScore,
		Checksum:       track.Checksum,
		Format:         track.Format,
		Path:           track.FinalPath,
		MetaData: map[string]any{
			"explicit":   track.Explicit,
			"version":    track.Version,
			"source_url": sourceURL,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	usable, ok := s.usableCatalogFile(pending)
	if !ok {
		clearPendingImport(track)
		if request.Result != nil && request.Result.FinalPath == pending.Path {
			request.Result = nil
		}
		return false, nil
	}
	if err := s.finalizeImportedTrack(ctx, request, track, usable, sourceURL); err != nil {
		return true, err
	}
	s.log.Info(
		"resumed pending catalog import",
		zap.String("request_id", request.ID),
		zap.String("spotify_id", track.SpotifyID),
		zap.String("path", usable.Path),
	)
	return true, nil
}

func clearPendingImport(track *spotify.TrackMetadata) {
	track.SourceProvider = ""
	track.SourceID = ""
	track.FinalPath = ""
	track.Format = ""
	track.Checksum = ""
	track.MatchScore = 0
}

func (s *service) metadataFailure(err error) error {
	var apiError *spotify.APIError
	if !errors.As(err, &apiError) {
		return failure("spotify_metadata", "resolving", true, false, nil, err)
	}

	switch apiError.StatusCode {
	case http.StatusTooManyRequests:
		return &requestFailure{
			code:       "spotify_rate_limited",
			stage:      "resolving",
			retryable:  true,
			retryAfter: apiError.RetryAfter,
			err:        err,
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return failure("spotify_auth", "resolving", false, true, nil, err)
	case http.StatusNotFound, http.StatusBadRequest:
		return failure("spotify_invalid_resource", "resolving", false, true, nil, err)
	default:
		return failure("spotify_api", "resolving", apiError.StatusCode >= 500, apiError.StatusCode < 500, nil, err)
	}
}

func (s *service) applyFailure(request *models.DownloadQueueRequest, err error) {
	var classified *requestFailure
	workerInterrupted := false
	if errors.As(err, &classified) {
		// Keep the stage-specific classification supplied by the acquisition
		// pipeline, including command-specific timeouts.
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		workerInterrupted = true
		classified = &requestFailure{
			code:      "worker_interrupted",
			stage:     "processing",
			retryable: true,
			err:       err,
		}
	} else {
		classified = &requestFailure{
			code:      "unexpected",
			stage:     "processing",
			retryable: true,
			err:       err,
		}
	}

	now := time.Now().UTC()
	request.LastError = &models.DownloadRequestError{
		Code:       classified.code,
		Stage:      classified.stage,
		Message:    classified.err.Error(),
		Retryable:  classified.retryable,
		OccurredAt: now.Unix(),
	}
	if classified.track != nil {
		request.LastError.Details = map[string]string{
			"spotify_id": classified.track.SpotifyID,
			"artist":     classified.track.Artist,
			"title":      classified.track.Title,
		}
	}

	switch {
	case workerInterrupted:
		request.State = models.DownloadRequestStateRetryWait
		request.NextAttemptAt = now.Unix()
	case classified.review || !classified.retryable:
		request.State = models.DownloadRequestStateNeedsReview
		request.NextAttemptAt = 0
	case classified.preserveBudget:
		request.State = models.DownloadRequestStateRetryWait
		retryDelay := s.retryDelay
		if classified.retryAfter > retryDelay {
			retryDelay = classified.retryAfter
		}
		request.NextAttemptAt = now.Add(retryDelay).Unix()
	default:
		request.RetryCount++
		if request.RetryCount >= s.maxAttempts {
			request.State = models.DownloadRequestStateFailed
			request.NextAttemptAt = 0
		} else {
			request.State = models.DownloadRequestStateRetryWait
			retryDelay := s.retryDelay
			if classified.retryAfter > retryDelay {
				retryDelay = classified.retryAfter
			}
			request.NextAttemptAt = now.Add(retryDelay).Unix()
		}
	}
}

func finalizationFailure(
	code string,
	track *spotify.TrackMetadata,
	err error,
) error {
	return &requestFailure{
		code:           code,
		stage:          "imported",
		retryable:      true,
		preserveBudget: true,
		track:          track,
		err:            err,
	}
}

func failure(
	code, stage string,
	retryable, review bool,
	track *spotify.TrackMetadata,
	err error,
) error {
	return &requestFailure{
		code:      code,
		stage:     stage,
		retryable: retryable,
		review:    review,
		track:     track,
		err:       err,
	}
}

func (s *service) transition(
	ctx context.Context,
	request *models.DownloadQueueRequest,
	state models.DownloadRequestState,
) error {
	request.State = state
	return s.persist(ctx, request)
}

func (s *service) persist(ctx context.Context, request *models.DownloadQueueRequest) error {
	request.UpdatedAt = time.Now().UTC().Unix()
	return s.database.UpdateClaimedRequest(ctx, *request, s.workerID, request.ClaimID)
}

func (s *service) preCheckTracksInDB(
	ctx context.Context,
	request *models.DownloadQueueRequest,
) error {
	pending := make([]spotify.TrackMetadata, 0, len(request.TrackMetadata))
	for index := range request.TrackMetadata {
		track := &request.TrackMetadata[index]
		if track.SpotifyID == "" {
			track.SpotifyID = spotifyIDFromTrackURL(track.SpotifyURL)
		}
		if track.Found {
			_, usable := s.usableCatalogFile(models.MusicFile{
				Path:     track.FinalPath,
				Checksum: track.Checksum,
			})
			track.Found = false
			if !usable {
				track.AcquisitionError = "previously imported catalog path is missing or invalid"
			}
		}
		pending = append(pending, *track)
	}
	if len(pending) == 0 {
		request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
		return nil
	}

	foundMusic, err := s.database.FindMusicFilesForTracks(ctx, pending)
	if err != nil {
		return err
	}
	foundBySpotifyID := make(map[string]models.MusicFile, len(foundMusic))
	foundByISRC := make(map[string]models.MusicFile, len(foundMusic))
	legacyFoundByName := make(map[string]models.MusicFile, len(foundMusic))
	for _, music := range foundMusic {
		usable, ok := s.usableCatalogFile(music)
		if !ok {
			continue
		}
		if spotifyID := strings.TrimSpace(usable.SpotifyID); spotifyID != "" {
			foundBySpotifyID[spotifyID] = usable
		}
		if isrc := normalizedISRC(usable.ISRC); isrc != "" {
			foundByISRC[isrc] = usable
		}
		if strings.TrimSpace(usable.SpotifyID) == "" && normalizedISRC(usable.ISRC) == "" {
			legacyFoundByName[trackKey(usable.Artist, usable.Title)] = usable
		}
	}
	for index := range request.TrackMetadata {
		track := &request.TrackMetadata[index]
		if track.Found {
			continue
		}
		music, found := foundBySpotifyID[strings.TrimSpace(track.SpotifyID)]
		if !found {
			music, found = foundByISRC[normalizedISRC(track.ISRC)]
		}
		if !found {
			music, found = legacyFoundByName[trackKey(track.Artist, track.Title)]
		}
		if !found {
			continue
		}
		track.Found = true
		track.Skipped = false
		track.FailedAttempts = 0
		track.AcquisitionError = ""
		track.SourceProvider = music.SourceProvider
		track.SourceID = music.SourceID
		track.FinalPath = music.Path
		track.Format = music.Format
		track.Checksum = music.Checksum
		track.MatchScore = music.MatchScore
	}
	request.FoundTrackCount = countFoundTracks(request.TrackMetadata)
	return nil
}

func (s *service) usableCatalogFile(file models.MusicFile) (models.MusicFile, bool) {
	path := strings.TrimSpace(file.Path)
	if path == "" {
		return models.MusicFile{}, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.libraryPath, path)
	}
	root, err := filepath.Abs(s.libraryPath)
	if err != nil {
		return models.MusicFile{}, false
	}
	path, err = filepath.Abs(path)
	if err != nil || !pathWithinRoot(root, path) {
		return models.MusicFile{}, false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return models.MusicFile{}, false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return models.MusicFile{}, false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil || !pathWithinRoot(resolvedRoot, resolvedPath) {
		return models.MusicFile{}, false
	}
	if expected := strings.TrimSpace(file.Checksum); expected != "" {
		actual, err := catalogChecksum(path)
		if err != nil || !strings.EqualFold(actual, expected) {
			return models.MusicFile{}, false
		}
	}
	file.Path = path
	return file, true
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func catalogChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizedISRC(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func trackSpec(track spotify.TrackMetadata) acquisition.TrackSpec {
	artists := make([]string, 0)
	for _, artist := range strings.Split(track.Artist, ",") {
		if artist = strings.TrimSpace(artist); artist != "" {
			artists = append(artists, artist)
		}
	}
	return acquisition.TrackSpec{
		ID:       track.SpotifyID,
		ISRC:     track.ISRC,
		URL:      track.SpotifyURL,
		Title:    track.Title,
		Artists:  artists,
		Album:    track.Album,
		Duration: time.Duration(track.DurationMS) * time.Millisecond,
		Version:  track.Version,
		Explicit: track.Explicit,
	}
}

func spotifyIDFromTrackURL(value string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() != "open.spotify.com" {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 2 || segments[0] != "track" {
		return ""
	}
	return segments[1]
}

func countFoundTracks(tracks []spotify.TrackMetadata) int {
	found := 0
	for _, track := range tracks {
		if track.Found {
			found++
		}
	}
	return found
}

func trackKey(artist, title string) string {
	return strings.ToLower(strings.TrimSpace(artist)) + "\x00" +
		strings.ToLower(strings.TrimSpace(title))
}
