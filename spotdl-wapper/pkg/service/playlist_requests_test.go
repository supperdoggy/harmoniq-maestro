package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	models "github.com/supperdoggy/spot-models"
	modelspotify "github.com/supperdoggy/spot-models/spotify"
	spotifyapi "github.com/zmb3/spotify/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

type queuedTrack struct {
	url        string
	name       string
	objectType modelspotify.SpotifyObjectType
}

type playlistDatabaseFake struct {
	foundMusic      []models.MusicFile
	queued          []queuedTrack
	already         bool
	activePlaylists []models.PlaylistRequest
	updatedPlaylist *models.PlaylistRequest
}

func (f *playlistDatabaseFake) ClaimNextActiveRequest(
	context.Context,
	string,
	string,
	time.Duration,
) (models.DownloadQueueRequest, error) {
	return models.DownloadQueueRequest{}, mongo.ErrNoDocuments
}

func (f *playlistDatabaseFake) RenewRequestLease(
	context.Context,
	string,
	string,
	string,
	time.Duration,
) error {
	return nil
}

func (f *playlistDatabaseFake) UpdateClaimedRequest(
	context.Context,
	models.DownloadQueueRequest,
	string,
	string,
) error {
	return nil
}

func (f *playlistDatabaseFake) ReleaseRequestLease(
	context.Context,
	string,
	string,
	string,
	models.DownloadRequestState,
) error {
	return nil
}

func (f *playlistDatabaseFake) FindMusicFiles(
	context.Context,
	[]string,
	[]string,
) ([]models.MusicFile, error) {
	return append([]models.MusicFile(nil), f.foundMusic...), nil
}

func (f *playlistDatabaseFake) FindMusicFilesForTracks(
	context.Context,
	[]modelspotify.TrackMetadata,
) ([]models.MusicFile, error) {
	return append([]models.MusicFile(nil), f.foundMusic...), nil
}

func (f *playlistDatabaseFake) UpsertMusicFile(
	context.Context,
	models.MusicFile,
) (models.MusicFile, error) {
	return models.MusicFile{}, nil
}

func (f *playlistDatabaseFake) GetActivePlaylists(
	context.Context,
) ([]models.PlaylistRequest, error) {
	return append([]models.PlaylistRequest(nil), f.activePlaylists...), nil
}

func (f *playlistDatabaseFake) UpdatePlaylistRequest(_ context.Context, playlist models.PlaylistRequest) error {
	f.updatedPlaylist = &playlist
	return nil
}

func (f *playlistDatabaseFake) GetActiveRequest(
	context.Context,
	string,
) (models.DownloadQueueRequest, error) {
	return models.DownloadQueueRequest{}, nil
}

func (f *playlistDatabaseFake) CheckIfRequestAlreadySynced(
	context.Context,
	string,
) (bool, error) {
	return f.already, nil
}

func (f *playlistDatabaseFake) NewDownloadRequest(
	_ context.Context,
	url string,
	name string,
	_ int64,
	objectType modelspotify.SpotifyObjectType,
) error {
	f.queued = append(f.queued, queuedTrack{
		url:        url,
		name:       name,
		objectType: objectType,
	})
	return nil
}

type playlistSpotifyFake struct {
	name        string
	nameErr     error
	tracks      []spotifyapi.PlaylistItem
	tracksErr   error
	nameCalls   int
	tracksCalls int
}

func (f *playlistSpotifyFake) GetObjectName(context.Context, string) (string, error) {
	f.nameCalls++
	return f.name, f.nameErr
}

func (f *playlistSpotifyFake) GetObjectType(
	context.Context,
	string,
) (modelspotify.SpotifyObjectType, error) {
	return modelspotify.SpotifyObjectTypePlaylist, nil
}

func (f *playlistSpotifyFake) GetPlaylistTracks(
	context.Context,
	string,
) ([]spotifyapi.PlaylistItem, error) {
	f.tracksCalls++
	return append([]spotifyapi.PlaylistItem(nil), f.tracks...), f.tracksErr
}

func (f *playlistSpotifyFake) GetTrackCount(
	context.Context,
	string,
) (int, []modelspotify.TrackMetadata, error) {
	return len(f.tracks), nil, nil
}

func TestProcessPlaylistQueuesAllTracksWhenCatalogIsEmpty(t *testing.T) {
	database := &playlistDatabaseFake{}
	spotifyService := &playlistSpotifyFake{
		name: "All Missing",
		tracks: []spotifyapi.PlaylistItem{
			newPlaylistItem("track-1", "First Song", "First Artist"),
			newPlaylistItem("track-2", "Second Song", "Artist A", "Artist B"),
		},
	}
	srv := &service{
		database:            database,
		spotifyService:      spotifyService,
		log:                 zap.NewNop(),
		libraryPath:         filepath.Join(t.TempDir(), "music"),
		playlistsOutputPath: filepath.Join(t.TempDir(), "playlists"),
	}

	err := srv.ProcessPlaylist(context.Background(), &models.PlaylistRequest{
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
	})
	if !errors.Is(err, ErrMissingFiles) {
		t.Fatalf("ProcessPlaylist() error = %v, want ErrMissingFiles", err)
	}

	want := []queuedTrack{
		{
			url:        "https://open.spotify.com/track/track-1",
			name:       "First Artist - First Song",
			objectType: modelspotify.SpotifyObjectTypeTrack,
		},
		{
			url:        "https://open.spotify.com/track/track-2",
			name:       "Artist A, Artist B - Second Song",
			objectType: modelspotify.SpotifyObjectTypeTrack,
		},
	}
	if !reflect.DeepEqual(database.queued, want) {
		t.Errorf("queued tracks = %#v, want %#v", database.queued, want)
	}
}

func TestProcessPlaylistNoPullWritesSanitizedEmptyPlaylist(t *testing.T) {
	base := t.TempDir()
	playlistsOutputPath := filepath.Join(base, "nested", "playlists")
	database := &playlistDatabaseFake{}
	spotifyService := &playlistSpotifyFake{
		name: "All / Missing",
		tracks: []spotifyapi.PlaylistItem{
			newPlaylistItem("track-1", "First Song", "First Artist"),
		},
	}
	srv := &service{
		database:            database,
		spotifyService:      spotifyService,
		log:                 zap.NewNop(),
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: playlistsOutputPath,
	}

	err := srv.ProcessPlaylist(context.Background(), &models.PlaylistRequest{
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
		NoPull:     true,
	})
	if err != nil {
		t.Fatalf("ProcessPlaylist() error = %v", err)
	}
	if len(database.queued) != 0 {
		t.Fatalf("NoPull playlist queued %d tracks", len(database.queued))
	}

	outputPath := filepath.Join(playlistsOutputPath, "All-Missing.m3u")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read generated playlist: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("empty playlist content = %q, want empty", content)
	}
}

func TestProcessPlaylistWaitsWhenMissingTrackIsAlreadyQueued(t *testing.T) {
	base := t.TempDir()
	playlistsOutputPath := filepath.Join(base, "playlists")
	database := &playlistDatabaseFake{already: true}
	spotifyService := &playlistSpotifyFake{
		name: "Still Waiting",
		tracks: []spotifyapi.PlaylistItem{
			newPlaylistItem("track-1", "First Song", "First Artist"),
		},
	}
	srv := &service{
		database:            database,
		spotifyService:      spotifyService,
		log:                 zap.NewNop(),
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: playlistsOutputPath,
	}

	err := srv.ProcessPlaylist(context.Background(), &models.PlaylistRequest{
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
	})
	if !errors.Is(err, ErrMissingFiles) {
		t.Fatalf("ProcessPlaylist() error = %v, want ErrMissingFiles", err)
	}
	if len(database.queued) != 0 {
		t.Fatalf("already queued track was enqueued again: %#v", database.queued)
	}
	if _, err := os.Stat(filepath.Join(playlistsOutputPath, "Still Waiting.m3u")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial playlist was written while waiting: %v", err)
	}
}

func TestProcessPlaylistRequestDoesNotConsumeRetriesWhileWaiting(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{
		already: true,
		activePlaylists: []models.PlaylistRequest{{
			ID:         "playlist-request-1",
			SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
			Active:     true,
			RetryCount: 4,
		}},
	}
	srv := &service{
		database: database,
		spotifyService: &playlistSpotifyFake{
			name:   "Still Waiting",
			tracks: []spotifyapi.PlaylistItem{newPlaylistItem("track-1", "Song", "Artist")},
		},
		log:                 zap.NewNop(),
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}

	if err := srv.ProcessPlaylistRequest(context.Background()); err != nil {
		t.Fatalf("ProcessPlaylistRequest() error = %v", err)
	}
	if database.updatedPlaylist == nil {
		t.Fatal("playlist state was not persisted")
	}
	if !database.updatedPlaylist.Active ||
		database.updatedPlaylist.Errored ||
		database.updatedPlaylist.RetryCount != 4 ||
		database.updatedPlaylist.NextAttemptAt != 0 ||
		database.updatedPlaylist.LastError != nil {
		t.Fatalf("waiting playlist state = %#v", *database.updatedPlaylist)
	}
}

func TestProcessPlaylistUsesCachedName(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{}
	spotifyService := &playlistSpotifyFake{nameErr: errors.New("name lookup must not run")}
	srv := &service{
		database:            database,
		spotifyService:      spotifyService,
		log:                 zap.NewNop(),
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}
	playlist := &models.PlaylistRequest{
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
		Name:       "Cached Focus",
		NoPull:     true,
	}

	if err := srv.ProcessPlaylist(context.Background(), playlist); err != nil {
		t.Fatalf("ProcessPlaylist() error = %v", err)
	}
	if spotifyService.nameCalls != 0 {
		t.Fatalf("cached playlist caused %d Spotify name calls", spotifyService.nameCalls)
	}
	if _, err := os.Stat(filepath.Join(base, "playlists", "Cached Focus.m3u")); err != nil {
		t.Fatalf("cached-name playlist was not created: %v", err)
	}
}

func TestProcessPlaylistRequestSchedulesSpotifyRateLimit(t *testing.T) {
	tests := []struct {
		name           string
		playlistName   string
		nameErr        error
		tracksErr      error
		retryAfter     time.Duration
		wantNameCalls  int
		wantTrackCalls int
	}{
		{
			name: "name lookup falls back to configured delay when no header is available",
			nameErr: fmt.Errorf("get name: %w", &modelspotify.APIError{
				StatusCode: http.StatusTooManyRequests,
				Message:    "rate limited",
			}),
			wantNameCalls: 1,
		},
		{
			name:         "items lookup honors a longer Retry-After",
			playlistName: "Cached Focus",
			retryAfter:   2 * time.Hour,
			tracksErr: fmt.Errorf("get items: %w", &modelspotify.APIError{
				StatusCode: http.StatusTooManyRequests,
				Message:    "quota exhausted",
				Reason:     "QUOTA_EXCEEDED",
				RetryAfter: 2 * time.Hour,
			}),
			wantTrackCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			database := &playlistDatabaseFake{activePlaylists: []models.PlaylistRequest{{
				ID:         "playlist-request-1",
				SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
				Name:       test.playlistName,
				Active:     true,
				NoPull:     true,
			}}}
			spotifyService := &playlistSpotifyFake{
				name:      "Fetched Focus",
				nameErr:   test.nameErr,
				tracksErr: test.tracksErr,
			}
			const retryFloor = 30 * time.Minute
			srv := &service{
				database:            database,
				spotifyService:      spotifyService,
				log:                 zap.NewNop(),
				retryDelay:          retryFloor,
				libraryPath:         filepath.Join(base, "music"),
				playlistsOutputPath: filepath.Join(base, "playlists"),
			}

			before := time.Now().UTC()
			err := srv.ProcessPlaylistRequest(context.Background())
			if err == nil {
				t.Fatal("ProcessPlaylistRequest() error = nil, want Spotify rate limit")
			}
			if database.updatedPlaylist == nil {
				t.Fatal("rate-limited playlist state was not persisted")
			}
			updated := *database.updatedPlaylist
			if !updated.Active || !updated.Errored || updated.RetryCount != 1 {
				t.Fatalf("rate-limited playlist state = %#v", updated)
			}
			if updated.LastError == nil || updated.LastError.Code != "spotify_rate_limited" ||
				updated.LastError.Stage != "playlist" || !updated.LastError.Retryable {
				t.Fatalf("rate-limit error = %#v", updated.LastError)
			}
			wantDelay := retryFloor
			if test.retryAfter > wantDelay {
				wantDelay = test.retryAfter
			}
			wantEarliest := before.Add(wantDelay).Unix()
			if updated.NextAttemptAt < wantEarliest || updated.NextAttemptAt > wantEarliest+2 {
				t.Fatalf("next attempt = %d, want approximately %d", updated.NextAttemptAt, wantEarliest)
			}
			if spotifyService.nameCalls != test.wantNameCalls || spotifyService.tracksCalls != test.wantTrackCalls {
				t.Fatalf(
					"Spotify calls = name:%d tracks:%d, want name:%d tracks:%d",
					spotifyService.nameCalls,
					spotifyService.tracksCalls,
					test.wantNameCalls,
					test.wantTrackCalls,
				)
			}
		})
	}
}

func TestProcessPlaylistRequestUsesRetryFloorForGenericErrors(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{activePlaylists: []models.PlaylistRequest{{
		ID:         "playlist-request-1",
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
		Active:     true,
	}}}
	srv := &service{
		database: database,
		spotifyService: &playlistSpotifyFake{
			nameErr: errors.New("temporary Spotify failure"),
		},
		log:                 zap.NewNop(),
		retryDelay:          20 * time.Minute,
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}

	before := time.Now().UTC().Add(20 * time.Minute).Unix()
	if err := srv.ProcessPlaylistRequest(context.Background()); err == nil {
		t.Fatal("ProcessPlaylistRequest() error = nil, want generic failure")
	}
	updated := database.updatedPlaylist
	if updated == nil || updated.LastError == nil || updated.LastError.Code != "playlist_processing" {
		t.Fatalf("generic playlist error = %#v", updated)
	}
	if updated.NextAttemptAt < before || updated.NextAttemptAt > before+2 {
		t.Fatalf("next attempt = %d, want approximately %d", updated.NextAttemptAt, before)
	}
}

func TestProcessPlaylistRequestFifthFailureIsTerminal(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{activePlaylists: []models.PlaylistRequest{{
		ID:         "playlist-request-1",
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
		Active:     true,
		RetryCount: maxPlaylistAttempts - 1,
	}}}
	srv := &service{
		database: database,
		spotifyService: &playlistSpotifyFake{nameErr: &modelspotify.APIError{
			StatusCode: http.StatusTooManyRequests,
			Message:    "rate limited",
			RetryAfter: 24 * time.Hour,
		}},
		log:                 zap.NewNop(),
		retryDelay:          time.Minute,
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}

	if err := srv.ProcessPlaylistRequest(context.Background()); err == nil {
		t.Fatal("ProcessPlaylistRequest() error = nil, want terminal failure")
	}
	updated := database.updatedPlaylist
	if updated == nil || updated.Active || !updated.Errored ||
		updated.RetryCount != maxPlaylistAttempts || updated.NextAttemptAt != 0 {
		t.Fatalf("terminal playlist state = %#v", updated)
	}
	if updated.LastError == nil || updated.LastError.Code != "spotify_rate_limited" {
		t.Fatalf("terminal playlist error = %#v", updated.LastError)
	}
}

func TestProcessPlaylistRequestSuccessClearsRetryMetadata(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{activePlaylists: []models.PlaylistRequest{{
		ID:            "playlist-request-1",
		SpotifyURL:    "https://open.spotify.com/playlist/playlist-id",
		Active:        true,
		NoPull:        true,
		RetryCount:    2,
		NextAttemptAt: time.Now().Add(-time.Minute).Unix(),
		LastError:     &models.DownloadRequestError{Code: "spotify_rate_limited"},
	}}}
	srv := &service{
		database:            database,
		spotifyService:      &playlistSpotifyFake{name: "Recovered"},
		log:                 zap.NewNop(),
		retryDelay:          time.Minute,
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}

	if err := srv.ProcessPlaylistRequest(context.Background()); err != nil {
		t.Fatalf("ProcessPlaylistRequest() error = %v", err)
	}
	updated := database.updatedPlaylist
	if updated == nil || updated.Active || updated.Errored ||
		updated.NextAttemptAt != 0 || updated.LastError != nil || updated.Name != "Recovered" {
		t.Fatalf("recovered playlist state = %#v", updated)
	}
	if updated.RetryCount != 2 {
		t.Fatalf("successful playlist retry count = %d, want retained audit count 2", updated.RetryCount)
	}
}

func TestProcessPlaylistRequestCancellationDoesNotConsumeAttempt(t *testing.T) {
	base := t.TempDir()
	database := &playlistDatabaseFake{activePlaylists: []models.PlaylistRequest{{
		ID:         "playlist-request-1",
		SpotifyURL: "https://open.spotify.com/playlist/playlist-id",
		Active:     true,
		RetryCount: 2,
	}}}
	srv := &service{
		database: database,
		spotifyService: &playlistSpotifyFake{
			nameErr: fmt.Errorf("stopping: %w", context.Canceled),
		},
		log:                 zap.NewNop(),
		retryDelay:          time.Minute,
		libraryPath:         filepath.Join(base, "music"),
		playlistsOutputPath: filepath.Join(base, "playlists"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := srv.ProcessPlaylistRequest(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessPlaylistRequest() error = %v, want context cancellation", err)
	}
	if database.updatedPlaylist != nil {
		t.Fatalf("cancellation persisted retry state: %#v", *database.updatedPlaylist)
	}
}

func newPlaylistItem(
	id string,
	title string,
	artists ...string,
) spotifyapi.PlaylistItem {
	simpleArtists := make([]spotifyapi.SimpleArtist, 0, len(artists))
	for _, artist := range artists {
		simpleArtists = append(simpleArtists, spotifyapi.SimpleArtist{Name: artist})
	}

	return spotifyapi.PlaylistItem{
		Track: spotifyapi.PlaylistItemTrack{
			Track: &spotifyapi.FullTrack{
				SimpleTrack: spotifyapi.SimpleTrack{
					ID:      spotifyapi.ID(id),
					Name:    title,
					Artists: simpleArtists,
				},
			},
		},
	}
}
