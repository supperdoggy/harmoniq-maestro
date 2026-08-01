package service

import (
	"context"
	"errors"
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
	name   string
	tracks []spotifyapi.PlaylistItem
}

func (f *playlistSpotifyFake) GetObjectName(context.Context, string) (string, error) {
	return f.name, nil
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
	return append([]spotifyapi.PlaylistItem(nil), f.tracks...), nil
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

	err := srv.ProcessPlaylist(context.Background(), models.PlaylistRequest{
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

	err := srv.ProcessPlaylist(context.Background(), models.PlaylistRequest{
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

	err := srv.ProcessPlaylist(context.Background(), models.PlaylistRequest{
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
		database.updatedPlaylist.RetryCount != 4 {
		t.Fatalf("waiting playlist state = %#v", *database.updatedPlaylist)
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
