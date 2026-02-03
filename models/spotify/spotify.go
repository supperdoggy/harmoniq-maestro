package spotify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"go.uber.org/zap"
	"golang.org/x/oauth2/clientcredentials"
)

type TrackMetadata struct {
	SpotifyURL     string `json:"spotify_url" bson:"spotify_url"`
	Artist         string `json:"artist" bson:"artist"`
	Title          string `json:"title" bson:"title"`
	Found          bool   `json:"found" bson:"found"`
	FailedAttempts int    `json:"failed_attempts" bson:"failed_attempts"`
	Skipped        bool   `json:"skipped" bson:"skipped"` // marked as stuck after MaxFailedAttempts
}

const MaxFailedAttempts = 3 // after this many failed attempts, track is marked as skipped

type SpotifyService interface {
	GetObjectName(ctx context.Context, url string) (string, error)
	GetObjectType(ctx context.Context, url string) (SpotifyObjectType, error)
	GetPlaylistTracks(ctx context.Context, url string) ([]spotify.PlaylistItem, error)
	GetTrackCount(ctx context.Context, url string) (int, []TrackMetadata, error)
}

type spotifyService struct {
	ClientID      string
	ClientSecret  string
	spotifyClient *spotify.Client
	log           *zap.Logger
}

func NewSpotifyService(ctx context.Context, clientID, clientSecret string, log *zap.Logger) SpotifyService {
	spotifyConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     spotifyauth.TokenURL,
	}

	// Use Client() instead of Token() - this automatically refreshes expired tokens
	httpClient := spotifyConfig.Client(ctx)
	spotifyClient := spotify.New(httpClient)

	return &spotifyService{
		spotifyClient: spotifyClient,
		log:           log,
	}
}

func (s *spotifyService) GetObjectName(ctx context.Context, url string) (string, error) {

	if !s.isValidSpotifyURL(url) {
		s.log.Error("invalid spotify url", zap.String("url", url))
		return "", errors.New("invalid spotify url")
	}

	objectType, err := s.GetObjectType(ctx, url)
	if err != nil {
		s.log.Error("failed to get object type", zap.Error(err))
		return "", err
	}

	id := s.getSpotifyID(url)
	if id == "" {
		s.log.Error("failed to get spotify id", zap.String("url", url))
		return "", errors.New("invalid spotify url")
	}

	var name string
	switch objectType {
	case SpotifyObjectTypePlaylist:
		playlist, err := s.spotifyClient.GetPlaylist(ctx, id)
		if err != nil {
			s.log.Error("failed to get playlist", zap.Error(err), zap.String("id", string(id)))
			return "", err
		}
		name = playlist.Name
	case SpotifyObjectTypeAlbum:
		album, err := s.spotifyClient.GetAlbum(ctx, id)
		if err != nil {
			s.log.Error("failed to get album", zap.Error(err), zap.String("id", string(id)))
			return "", err
		}
		name = album.Name
	case SpotifyObjectTypeTrack:
		track, err := s.spotifyClient.GetTrack(ctx, id)
		if err != nil {
			s.log.Error("failed to get track", zap.Error(err), zap.String("id", string(id)))
			return "", err
		}
		name = track.Name
	default:
		return "", errors.New("unknown object type")
	}

	return name, nil
}

func (s *spotifyService) GetObjectType(ctx context.Context, url string) (SpotifyObjectType, error) {
	if strings.Contains(url, "playlist") {
		return SpotifyObjectTypePlaylist, nil
	}
	if strings.Contains(url, "album") {
		return SpotifyObjectTypeAlbum, nil
	}
	if strings.Contains(url, "track") {
		return SpotifyObjectTypeTrack, nil
	}
	if strings.Contains(url, "artist") {
		return SpotifyObjectTypeArtist, nil
	}
	return "", errors.New("unknown object type")
}

func (s *spotifyService) isValidSpotifyURL(url string) bool {
	// Check if the URL starts with "https://open.spotify.com/"
	return strings.HasPrefix(url, "https://open.spotify.com/")
}

func (s *spotifyService) getSpotifyID(url string) spotify.ID {
	id := strings.Split(strings.Split(url, "/")[4], "?")[0]
	return spotify.ID(id)
}

// getTrackURL converts a Spotify track ID to a full URL
func (s *spotifyService) getTrackURL(trackID spotify.ID) string {
	return fmt.Sprintf("https://open.spotify.com/track/%s", string(trackID))
}

func (s *spotifyService) GetPlaylistTracks(ctx context.Context, url string) ([]spotify.PlaylistItem, error) {

	if !s.isValidSpotifyURL(url) {
		return nil, errors.New("invalid spotify url")
	}

	id := s.getSpotifyID(url)
	if id == "" {
		return nil, errors.New("invalid spotify url")
	}

	var playlistItems []spotify.PlaylistItem
	itemsPage, err := s.spotifyClient.GetPlaylistItems(context.Background(), id)
	if err != nil {
		fmt.Println("Error getting playlist items:", err)
		return nil, err
	}

	if itemsPage.Total > spotify.Numeric(len(itemsPage.Items)) {
		total := int(itemsPage.Total)
		for i := 0; i < total; i += int(itemsPage.Limit) {
			items, err := s.spotifyClient.GetPlaylistItems(context.Background(), id, spotify.Limit(int(itemsPage.Limit)), spotify.Offset(i))
			if err != nil {
				fmt.Println("Error getting playlist items:", err)
				return nil, err
			}
			playlistItems = append(playlistItems, items.Items...)
		}
	} else {
		playlistItems = itemsPage.Items
	}

	return playlistItems, nil
}

// GetTrackCount returns the total track count and metadata for a Spotify URL (album, playlist, or track)
func (s *spotifyService) GetTrackCount(ctx context.Context, url string) (int, []TrackMetadata, error) {
	if !s.isValidSpotifyURL(url) {
		return 0, nil, errors.New("invalid spotify url")
	}

	objectType, err := s.GetObjectType(ctx, url)
	if err != nil {
		return 0, nil, err
	}

	id := s.getSpotifyID(url)
	if id == "" {
		return 0, nil, errors.New("failed to get spotify id")
	}

	var count int
	var tracks []TrackMetadata

	switch objectType {
	case SpotifyObjectTypePlaylist:
		playlistItems, err := s.GetPlaylistTracks(ctx, url)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get playlist tracks: %w", err)
		}
		count = len(playlistItems)
		for _, item := range playlistItems {
			if item.Track.Track == nil {
				continue
			}
			artists := []string{}
			for _, artist := range item.Track.Track.Artists {
				artists = append(artists, strings.ToLower(artist.Name))
			}
			tracks = append(tracks, TrackMetadata{
				SpotifyURL: s.getTrackURL(item.Track.Track.ID),
				Artist:     strings.Join(artists, ", "),
				Title:      strings.ToLower(item.Track.Track.Name),
			})
		}

	case SpotifyObjectTypeAlbum:
		album, err := s.spotifyClient.GetAlbum(ctx, id)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get album: %w", err)
		}
		count = int(album.Tracks.Total)

		// Get all album tracks (handle pagination)
		var allTracks []spotify.SimpleTrack
		offset := 0
		limit := 50
		for {
			albumTracks, err := s.spotifyClient.GetAlbumTracks(ctx, id, spotify.Limit(limit), spotify.Offset(offset))
			if err != nil {
				return 0, nil, fmt.Errorf("failed to get album tracks: %w", err)
			}
			allTracks = append(allTracks, albumTracks.Tracks...)
			if len(albumTracks.Tracks) < limit {
				break
			}
			offset += limit
		}

		for _, track := range allTracks {
			artists := []string{}
			for _, artist := range track.Artists {
				artists = append(artists, strings.ToLower(artist.Name))
			}
			tracks = append(tracks, TrackMetadata{
				SpotifyURL: s.getTrackURL(track.ID),
				Artist:     strings.Join(artists, ", "),
				Title:      strings.ToLower(track.Name),
			})
		}

	case SpotifyObjectTypeTrack:
		track, err := s.spotifyClient.GetTrack(ctx, id)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get track: %w", err)
		}
		count = 1
		artists := []string{}
		for _, artist := range track.Artists {
			artists = append(artists, strings.ToLower(artist.Name))
		}
		tracks = append(tracks, TrackMetadata{
			SpotifyURL: s.getTrackURL(track.ID),
			Artist:     strings.Join(artists, ", "),
			Title:      strings.ToLower(track.Name),
		})

	default:
		return 0, nil, fmt.Errorf("unsupported object type: %s", objectType)
	}

	return count, tracks, nil
}
