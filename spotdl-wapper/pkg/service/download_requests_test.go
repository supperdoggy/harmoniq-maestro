package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
	models "github.com/supperdoggy/spot-models"
	"github.com/supperdoggy/spot-models/spotify"
	spotifyapi "github.com/zmb3/spotify/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

type fakeDatabase struct {
	claims    []models.DownloadQueueRequest
	updates   []models.DownloadQueueRequest
	releases  []models.DownloadRequestState
	upserts   []models.MusicFile
	upsertErr error
	found     []models.MusicFile
}

func (f *fakeDatabase) ClaimNextActiveRequest(
	context.Context,
	string,
	string,
	time.Duration,
) (models.DownloadQueueRequest, error) {
	if len(f.claims) == 0 {
		return models.DownloadQueueRequest{}, mongo.ErrNoDocuments
	}
	request := f.claims[0]
	f.claims = f.claims[1:]
	if request.ClaimID == "" {
		request.ClaimID = "claim-1"
	}
	return request, nil
}

func (f *fakeDatabase) RenewRequestLease(context.Context, string, string, string, time.Duration) error {
	return nil
}

func (f *fakeDatabase) UpdateClaimedRequest(
	_ context.Context,
	request models.DownloadQueueRequest,
	_, _ string,
) error {
	f.updates = append(f.updates, request)
	return nil
}

func (f *fakeDatabase) ReleaseRequestLease(
	_ context.Context,
	_, _, _ string,
	state models.DownloadRequestState,
) error {
	f.releases = append(f.releases, state)
	return nil
}

func (f *fakeDatabase) FindMusicFiles(
	context.Context,
	[]string,
	[]string,
) ([]models.MusicFile, error) {
	return append([]models.MusicFile(nil), f.found...), nil
}

func (f *fakeDatabase) FindMusicFilesForTracks(
	context.Context,
	[]spotify.TrackMetadata,
) ([]models.MusicFile, error) {
	return append([]models.MusicFile(nil), f.found...), nil
}

func (f *fakeDatabase) UpsertMusicFile(
	_ context.Context,
	file models.MusicFile,
) (models.MusicFile, error) {
	f.upserts = append(f.upserts, file)
	if f.upsertErr != nil {
		return models.MusicFile{}, f.upsertErr
	}
	if file.ID == "" {
		file.ID = "catalog-1"
	}
	return file, nil
}

func (*fakeDatabase) GetActivePlaylists(context.Context) ([]models.PlaylistRequest, error) {
	return nil, nil
}

func (*fakeDatabase) UpdatePlaylistRequest(context.Context, models.PlaylistRequest) error {
	return nil
}

func (*fakeDatabase) GetActiveRequest(
	context.Context,
	string,
) (models.DownloadQueueRequest, error) {
	return models.DownloadQueueRequest{}, mongo.ErrNoDocuments
}

func (*fakeDatabase) CheckIfRequestAlreadySynced(context.Context, string) (bool, error) {
	return false, nil
}

func (*fakeDatabase) NewDownloadRequest(
	context.Context,
	string,
	string,
	int64,
	spotify.SpotifyObjectType,
) error {
	return nil
}

type fakeSpotifyService struct {
	count    int
	metadata []spotify.TrackMetadata
	err      error
}

func (*fakeSpotifyService) GetObjectName(context.Context, string) (string, error) {
	return "playlist", nil
}

func (*fakeSpotifyService) GetObjectType(
	context.Context,
	string,
) (spotify.SpotifyObjectType, error) {
	return spotify.SpotifyObjectTypeTrack, nil
}

func (*fakeSpotifyService) GetPlaylistTracks(
	context.Context,
	string,
) ([]spotifyapi.PlaylistItem, error) {
	return nil, nil
}

func (f *fakeSpotifyService) GetTrackCount(
	context.Context,
	string,
) (int, []spotify.TrackMetadata, error) {
	return f.count, append([]spotify.TrackMetadata(nil), f.metadata...), f.err
}

type fakeProvider struct {
	resolveCandidates []acquisition.Candidate
	resolveErr        error
	acquireResult     acquisition.AssetResult
	acquireErr        error
	resolveCalls      int
	acquireCalls      int
}

func (*fakeProvider) Name() acquisition.ProviderName {
	return acquisition.ProviderYTDLP
}

func (f *fakeProvider) Resolve(
	context.Context,
	acquisition.TrackSpec,
) ([]acquisition.Candidate, error) {
	f.resolveCalls++
	return append([]acquisition.Candidate(nil), f.resolveCandidates...), f.resolveErr
}

func (f *fakeProvider) Acquire(
	context.Context,
	acquisition.TrackSpec,
	acquisition.Candidate,
) (acquisition.AssetResult, error) {
	f.acquireCalls++
	return f.acquireResult, f.acquireErr
}

type fakeImporter struct {
	file         models.MusicFile
	err          error
	calls        int
	discardCalls int
}

func (f *fakeImporter) Import(
	context.Context,
	acquisition.TrackSpec,
	acquisition.AssetResult,
) (models.MusicFile, error) {
	f.calls++
	return f.file, f.err
}

func (f *fakeImporter) Discard(acquisition.AssetResult) error {
	f.discardCalls++
	return nil
}

func TestProcessDownloadRequestCompletesAfterValidatedCatalogImport(t *testing.T) {
	request := claimedTrackRequest()
	database := &fakeDatabase{claims: []models.DownloadQueueRequest{request}}
	provider := &fakeProvider{
		resolveCandidates: []acquisition.Candidate{{
			Provider:  acquisition.ProviderYTDLP,
			SourceID:  "video-1",
			SourceURL: "https://example.test/video-1",
			Score:     0.96,
		}},
		acquireResult: acquisition.AssetResult{
			Provider:   acquisition.ProviderYTDLP,
			SourceID:   "video-1",
			SourceURL:  "https://example.test/video-1",
			FinalPath:  "/music/.staging/video-1.mp3",
			Format:     "mp3",
			MatchScore: 0.96,
		},
	}
	importer := &fakeImporter{file: models.MusicFile{
		SpotifyID:      "spotify-1",
		SourceProvider: "yt-dlp",
		SourceID:       "video-1",
		Path:           "/music/downloads/artist/song.mp3",
		Format:         "mp3",
		Checksum:       "abc123",
		MatchScore:     0.96,
	}}
	svc := newTestService(t, database, &fakeSpotifyService{}, provider, importer, 3)

	if err := svc.ProcessDownloadRequest(context.Background()); err != nil {
		t.Fatalf("ProcessDownloadRequest() error = %v", err)
	}

	final := lastUpdate(t, database)
	if final.State != models.DownloadRequestStateCompleted || final.Active || final.Errored {
		t.Fatalf("final request lifecycle = %#v", final)
	}
	if final.FoundTrackCount != 1 || !final.TrackMetadata[0].Found {
		t.Fatalf("track progress not completed: %#v", final.TrackMetadata)
	}
	if final.TrackMetadata[0].FinalPath != "/music/downloads/artist/song.mp3" ||
		final.TrackMetadata[0].SourceProvider != "yt-dlp" {
		t.Fatalf("structured track result not persisted: %#v", final.TrackMetadata[0])
	}
	if final.Result == nil ||
		final.Result.Checksum != "abc123" ||
		final.Result.CatalogID != "catalog-1" {
		t.Fatalf("request result not persisted: %#v", final.Result)
	}
	if len(database.upserts) != 1 {
		t.Fatalf("catalog upserts = %d, want 1", len(database.upserts))
	}
	if len(database.releases) != 1 ||
		database.releases[0] != models.DownloadRequestStateCompleted {
		t.Fatalf("release states = %#v, want completed", database.releases)
	}
}

func TestProcessDownloadRequestRoutesNoCandidateToReview(t *testing.T) {
	database := &fakeDatabase{claims: []models.DownloadQueueRequest{claimedTrackRequest()}}
	provider := &fakeProvider{resolveErr: acquisition.ErrNoCandidates}
	importer := &fakeImporter{}
	svc := newTestService(t, database, &fakeSpotifyService{}, provider, importer, 3)

	err := svc.ProcessDownloadRequest(context.Background())
	if !errors.Is(err, acquisition.ErrNoCandidates) {
		t.Fatalf("ProcessDownloadRequest() error = %v, want ErrNoCandidates", err)
	}

	final := lastUpdate(t, database)
	if final.State != models.DownloadRequestStateNeedsReview {
		t.Fatalf("state = %q, want needs_review", final.State)
	}
	if final.LastError == nil ||
		final.LastError.Code != "no_acceptable_candidate" ||
		final.LastError.Retryable {
		t.Fatalf("last error = %#v", final.LastError)
	}
	if provider.acquireCalls != 0 || importer.calls != 0 || len(database.upserts) != 0 {
		t.Fatal("rejected candidate unexpectedly reached acquisition or import")
	}
}

func TestProcessDownloadRequestSchedulesTransientFailureThenStopsAtAttemptLimit(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		wantState  models.DownloadRequestState
		acquireErr error
	}{
		{
			name:       "retry remains",
			retryCount: 0,
			wantState:  models.DownloadRequestStateRetryWait,
			acquireErr: errors.New("temporary downloader error"),
		},
		{
			name:       "stage timeout consumes retry budget",
			retryCount: 0,
			wantState:  models.DownloadRequestStateRetryWait,
			acquireErr: context.DeadlineExceeded,
		},
		{
			name:       "attempt limit reached",
			retryCount: 2,
			wantState:  models.DownloadRequestStateFailed,
			acquireErr: errors.New("temporary downloader error"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := claimedTrackRequest()
			request.RetryCount = test.retryCount
			database := &fakeDatabase{claims: []models.DownloadQueueRequest{request}}
			provider := &fakeProvider{
				resolveCandidates: []acquisition.Candidate{{
					Provider:  acquisition.ProviderYTDLP,
					SourceURL: "https://example.test/video",
				}},
				acquireErr: test.acquireErr,
			}
			svc := newTestService(
				t,
				database,
				&fakeSpotifyService{},
				provider,
				&fakeImporter{},
				3,
			)

			if err := svc.ProcessDownloadRequest(context.Background()); err == nil {
				t.Fatal("ProcessDownloadRequest() error = nil, want acquisition error")
			}
			final := lastUpdate(t, database)
			if final.State != test.wantState {
				t.Fatalf("state = %q, want %q", final.State, test.wantState)
			}
			if final.RetryCount != test.retryCount+1 {
				t.Fatalf("RetryCount = %d, want %d", final.RetryCount, test.retryCount+1)
			}
			if final.State == models.DownloadRequestStateRetryWait &&
				final.NextAttemptAt <= time.Now().UTC().Unix() {
				t.Fatalf("NextAttemptAt = %d, want future retry", final.NextAttemptAt)
			}
		})
	}
}

func TestProcessDownloadRequestUsesSynchronousCatalogPrecheck(t *testing.T) {
	request := claimedTrackRequest()
	libraryRoot := t.TempDir()
	existingPath := filepath.Join(libraryRoot, "existing.mp3")
	if err := os.WriteFile(existingPath, []byte("catalog audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	database := &fakeDatabase{
		claims: []models.DownloadQueueRequest{request},
		found: []models.MusicFile{{
			Artist:         "artist",
			Title:          "song",
			Path:           existingPath,
			SourceProvider: "legacy-indexer",
		}},
	}
	provider := &fakeProvider{}
	importer := &fakeImporter{}
	svc := newTestService(t, database, &fakeSpotifyService{}, provider, importer, 3)
	svc.libraryPath = libraryRoot

	if err := svc.ProcessDownloadRequest(context.Background()); err != nil {
		t.Fatalf("ProcessDownloadRequest() error = %v", err)
	}
	final := lastUpdate(t, database)
	if final.State != models.DownloadRequestStateCompleted ||
		final.TrackMetadata[0].FinalPath != existingPath {
		t.Fatalf("existing catalog track was not completed: %#v", final)
	}
	if provider.resolveCalls != 0 || provider.acquireCalls != 0 || importer.calls != 0 {
		t.Fatal("existing catalog track unexpectedly invoked acquisition")
	}
}

func TestProcessDownloadRequestResumesPublishedFileAfterCatalogFailure(t *testing.T) {
	libraryRoot := t.TempDir()
	publishedPath := filepath.Join(libraryRoot, "downloads", "song.mp3")
	if err := os.MkdirAll(filepath.Dir(publishedPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publishedPath, []byte("published audio"), 0o640); err != nil {
		t.Fatal(err)
	}
	checksum, err := catalogChecksum(publishedPath)
	if err != nil {
		t.Fatal(err)
	}

	firstRequest := claimedTrackRequest()
	firstRequest.RetryCount = 2
	firstDatabase := &fakeDatabase{
		claims:    []models.DownloadQueueRequest{firstRequest},
		upsertErr: errors.New("temporary catalog failure"),
	}
	firstProvider := &fakeProvider{
		resolveCandidates: []acquisition.Candidate{{
			Provider:  acquisition.ProviderYTDLP,
			SourceID:  "video-1",
			SourceURL: "https://example.test/video-1",
			Score:     0.95,
		}},
		acquireResult: acquisition.AssetResult{
			Provider:   acquisition.ProviderYTDLP,
			SourceID:   "video-1",
			SourceURL:  "https://example.test/video-1",
			FinalPath:  "/music/.staging/source.mp3",
			Format:     "mp3",
			MatchScore: 0.95,
		},
	}
	firstImporter := &fakeImporter{file: models.MusicFile{
		SpotifyID:      "spotify-1",
		SpotifyURL:     "https://open.spotify.com/track/spotify-1",
		Artist:         "artist",
		Album:          "album",
		Title:          "song",
		SourceProvider: "yt-dlp",
		SourceID:       "video-1",
		Path:           publishedPath,
		Format:         "mp3",
		Checksum:       checksum,
		MatchScore:     0.95,
	}}
	firstService := newTestService(
		t,
		firstDatabase,
		&fakeSpotifyService{},
		firstProvider,
		firstImporter,
		3,
	)
	firstService.libraryPath = libraryRoot

	if err := firstService.ProcessDownloadRequest(context.Background()); err == nil {
		t.Fatal("first ProcessDownloadRequest() error = nil, want catalog failure")
	}
	pending := lastUpdate(t, firstDatabase)
	if pending.State != models.DownloadRequestStateRetryWait ||
		pending.RetryCount != 2 ||
		pending.Result == nil ||
		pending.Result.FinalPath != publishedPath ||
		pending.TrackMetadata[0].FinalPath != publishedPath ||
		pending.TrackMetadata[0].Found {
		t.Fatalf("pending import journal was not preserved: %#v", pending)
	}

	pending.State = models.DownloadRequestStateClaimed
	pending.ClaimID = "claim-2"
	secondDatabase := &fakeDatabase{claims: []models.DownloadQueueRequest{pending}}
	secondProvider := &fakeProvider{}
	secondImporter := &fakeImporter{}
	secondService := newTestService(
		t,
		secondDatabase,
		&fakeSpotifyService{},
		secondProvider,
		secondImporter,
		3,
	)
	secondService.libraryPath = libraryRoot

	if err := secondService.ProcessDownloadRequest(context.Background()); err != nil {
		t.Fatalf("second ProcessDownloadRequest() error = %v", err)
	}
	completed := lastUpdate(t, secondDatabase)
	if completed.State != models.DownloadRequestStateCompleted ||
		!completed.TrackMetadata[0].Found ||
		completed.Result == nil ||
		completed.Result.CatalogID != "catalog-1" {
		t.Fatalf("pending import was not completed: %#v", completed)
	}
	if secondProvider.resolveCalls != 0 ||
		secondProvider.acquireCalls != 0 ||
		secondImporter.calls != 0 {
		t.Fatal("pending catalog import unexpectedly reacquired media")
	}
}

func TestProcessDownloadRequestUsesSpotifyRetryAfter(t *testing.T) {
	request := models.DownloadQueueRequest{
		ID:         "request-1",
		SpotifyURL: "https://open.spotify.com/track/spotify-1",
		Active:     true,
		State:      models.DownloadRequestStateClaimed,
	}
	database := &fakeDatabase{claims: []models.DownloadQueueRequest{request}}
	spotifyService := &fakeSpotifyService{err: &spotify.APIError{
		StatusCode: 429,
		Message:    "quota exhausted",
		RetryAfter: 2 * time.Hour,
	}}
	svc := newTestService(
		t,
		database,
		spotifyService,
		&fakeProvider{},
		&fakeImporter{},
		3,
	)

	before := time.Now().UTC().Add(2 * time.Hour).Unix()
	if err := svc.ProcessDownloadRequest(context.Background()); err == nil {
		t.Fatal("ProcessDownloadRequest() error = nil, want Spotify rate limit")
	}
	final := lastUpdate(t, database)
	if final.State != models.DownloadRequestStateRetryWait {
		t.Fatalf("state = %q, want retry_wait", final.State)
	}
	if final.LastError == nil || final.LastError.Code != "spotify_rate_limited" {
		t.Fatalf("last error = %#v", final.LastError)
	}
	if final.NextAttemptAt < before {
		t.Fatalf("NextAttemptAt = %d, want at least %d", final.NextAttemptAt, before)
	}
}

func newTestService(
	t *testing.T,
	database *fakeDatabase,
	spotifyService spotify.SpotifyService,
	provider acquisition.Provider,
	importer AssetImporter,
	maxAttempts int,
) *service {
	t.Helper()
	result, err := NewService(
		database,
		zap.NewNop(),
		spotifyService,
		provider,
		importer,
		Options{
			WorkerID:            "worker-1",
			LeaseDuration:       time.Hour,
			RetryDelay:          10 * time.Minute,
			MaxAttempts:         maxAttempts,
			PlaylistsOutputPath: "/music/playlists",
			LibraryPath:         "/music",
		},
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return result.(*service)
}

func claimedTrackRequest() models.DownloadQueueRequest {
	return models.DownloadQueueRequest{
		ID:                 "request-1",
		SpotifyURL:         "https://open.spotify.com/track/spotify-1",
		ObjectType:         spotify.SpotifyObjectTypeTrack,
		Active:             true,
		State:              models.DownloadRequestStateClaimed,
		ExpectedTrackCount: 1,
		TrackMetadata: []spotify.TrackMetadata{{
			SpotifyURL: "https://open.spotify.com/track/spotify-1",
			SpotifyID:  "spotify-1",
			Artist:     "artist",
			Title:      "song",
			Album:      "album",
			DurationMS: 180_000,
		}},
	}
}

func lastUpdate(t *testing.T, database *fakeDatabase) models.DownloadQueueRequest {
	t.Helper()
	if len(database.updates) == 0 {
		t.Fatal("no request updates were persisted")
	}
	return database.updates[len(database.updates)-1]
}
