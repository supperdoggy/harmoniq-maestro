package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type TrackMetadata struct {
	SpotifyURL     string `json:"spotify_url" bson:"spotify_url"`
	SpotifyID      string `json:"spotify_id,omitempty" bson:"spotify_id,omitempty"`
	Artist         string `json:"artist" bson:"artist"`
	Title          string `json:"title" bson:"title"`
	Album          string `json:"album,omitempty" bson:"album,omitempty"`
	ISRC           string `json:"isrc,omitempty" bson:"isrc,omitempty"`
	DurationMS     int    `json:"duration_ms,omitempty" bson:"duration_ms,omitempty"`
	Explicit       bool   `json:"explicit,omitempty" bson:"explicit,omitempty"`
	Version        string `json:"version,omitempty" bson:"version,omitempty"`
	Found          bool   `json:"found" bson:"found"`
	FailedAttempts int    `json:"failed_attempts" bson:"failed_attempts"`
	Skipped        bool   `json:"skipped" bson:"skipped"` // marked as stuck after MaxFailedAttempts

	SourceProvider   string  `json:"source_provider,omitempty" bson:"source_provider,omitempty"`
	SourceID         string  `json:"source_id,omitempty" bson:"source_id,omitempty"`
	FinalPath        string  `json:"final_path,omitempty" bson:"final_path,omitempty"`
	Format           string  `json:"format,omitempty" bson:"format,omitempty"`
	Checksum         string  `json:"checksum,omitempty" bson:"checksum,omitempty"`
	MatchScore       float64 `json:"match_score,omitempty" bson:"match_score,omitempty"`
	AcquisitionError string  `json:"acquisition_error,omitempty" bson:"acquisition_error,omitempty"`
}

const MaxFailedAttempts = 3 // after this many failed attempts, track is marked as skipped

// APIError preserves the status and quota/rate-limit classification returned
// by Spotify so callers can distinguish retryable throttling from hard errors.
type APIError struct {
	StatusCode int
	Reason     string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("Spotify API status %d (%s): %s", e.StatusCode, e.Reason, e.Message)
	}
	return fmt.Sprintf("Spotify API status %d: %s", e.StatusCode, e.Message)
}

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
	httpClient    *http.Client
	userAuth      bool
	log           *zap.Logger
}

func NewSpotifyService(ctx context.Context, clientID, clientSecret string, log *zap.Logger) SpotifyService {
	return NewSpotifyServiceWithRefreshToken(ctx, clientID, clientSecret, "", log)
}

// NewSpotifyServiceWithRefreshToken creates a Spotify client. A refresh token
// supplies user authentication, which is required to read playlist contents in
// Spotify Development Mode. When it is empty, the client-credentials flow is
// retained for Extended Quota Mode and non-user metadata endpoints.
func NewSpotifyServiceWithRefreshToken(ctx context.Context, clientID, clientSecret, refreshToken string, log *zap.Logger) SpotifyService {
	var httpClient *http.Client
	if refreshToken != "" {
		oauthConfig := oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  spotifyauth.AuthURL,
				TokenURL: spotifyauth.TokenURL,
			},
		}
		httpClient = oauth2.NewClient(ctx, oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}))
	} else {
		spotifyConfig := clientcredentials.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			TokenURL:     spotifyauth.TokenURL,
		}
		httpClient = spotifyConfig.Client(ctx)
	}
	spotifyClient := spotify.New(httpClient)

	return &spotifyService{
		spotifyClient: spotifyClient,
		httpClient:    httpClient,
		userAuth:      refreshToken != "",
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
			return "", normalizeSDKError(err)
		}
		name = playlist.Name
	case SpotifyObjectTypeAlbum:
		album, err := s.spotifyClient.GetAlbum(ctx, id)
		if err != nil {
			s.log.Error("failed to get album", zap.Error(err), zap.String("id", string(id)))
			return "", normalizeSDKError(err)
		}
		name = album.Name
	case SpotifyObjectTypeTrack:
		track, err := s.spotifyClient.GetTrack(ctx, id)
		if err != nil {
			s.log.Error("failed to get track", zap.Error(err), zap.String("id", string(id)))
			return "", normalizeSDKError(err)
		}
		name = track.Name
	default:
		return "", errors.New("unknown object type")
	}

	return name, nil
}

func (s *spotifyService) GetObjectType(ctx context.Context, url string) (SpotifyObjectType, error) {
	parsed, err := neturl.Parse(url)
	if err != nil || parsed.Hostname() != "open.spotify.com" {
		return "", errors.New("invalid spotify url")
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 {
		return "", errors.New("invalid spotify url")
	}

	switch segments[0] {
	case "playlist":
		return SpotifyObjectTypePlaylist, nil
	case "album":
		return SpotifyObjectTypeAlbum, nil
	case "track":
		return SpotifyObjectTypeTrack, nil
	case "artist":
		return SpotifyObjectTypeArtist, nil
	default:
		return "", errors.New("unknown object type")
	}
}

func (s *spotifyService) isValidSpotifyURL(url string) bool {
	parsed, err := neturl.Parse(url)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() == "open.spotify.com"
}

func (s *spotifyService) getSpotifyID(url string) spotify.ID {
	parsed, err := neturl.Parse(url)
	if err != nil || parsed.Hostname() != "open.spotify.com" {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 {
		return ""
	}
	return spotify.ID(segments[1])
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

	endpoint := fmt.Sprintf(
		"https://api.spotify.com/v1/playlists/%s/items?limit=100&additional_types=track",
		neturl.PathEscape(string(id)),
	)
	playlistItems := make([]spotify.PlaylistItem, 0)

	for endpoint != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}

		response, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("get current Spotify playlist items: %w", err)
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNotFound && !s.userAuth && len(playlistItems) == 0 {
				return s.getLegacyPlaylistTracks(ctx, id)
			}

			apiError := &APIError{
				StatusCode: response.StatusCode,
				Message:    strings.TrimSpace(string(body)),
			}
			var errorBody struct {
				Reason string `json:"reason"`
				Error  struct {
					Message string `json:"message"`
					Reason  string `json:"reason"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &errorBody) == nil {
				if errorBody.Error.Message != "" {
					apiError.Message = errorBody.Error.Message
				}
				apiError.Reason = errorBody.Reason
				if apiError.Reason == "" {
					apiError.Reason = errorBody.Error.Reason
				}
			}
			if retryAfterSeconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil {
				apiError.RetryAfter = time.Duration(retryAfterSeconds) * time.Second
			}
			if response.StatusCode == http.StatusForbidden && !s.userAuth {
				apiError.Message += "; configure a user refresh token for Development Mode playlists"
			}
			return nil, apiError
		}

		var page currentPlaylistItemsPage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		closeErr := response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode current Spotify playlist items: %w", decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close current Spotify playlist response: %w", closeErr)
		}

		for _, item := range page.Items {
			if item.IsLocal || len(item.Item) == 0 || bytes.Equal(item.Item, []byte("null")) {
				continue
			}
			var itemType struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(item.Item, &itemType); err != nil || itemType.Type != "track" {
				continue
			}
			var track spotify.FullTrack
			if err := json.Unmarshal(item.Item, &track); err != nil {
				return nil, fmt.Errorf("decode Spotify playlist track: %w", err)
			}
			playlistItems = append(playlistItems, spotify.PlaylistItem{
				AddedAt: item.AddedAt,
				IsLocal: item.IsLocal,
				Track: spotify.PlaylistItemTrack{
					Track: &track,
				},
			})
		}
		endpoint = page.Next
	}

	return playlistItems, nil
}

func (s *spotifyService) getLegacyPlaylistTracks(ctx context.Context, id spotify.ID) ([]spotify.PlaylistItem, error) {
	const pageSize = 100
	offset := 0
	items := make([]spotify.PlaylistItem, 0)

	for {
		page, err := s.spotifyClient.GetPlaylistItems(
			ctx,
			id,
			spotify.Limit(pageSize),
			spotify.Offset(offset),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"get legacy Spotify playlist items: %w",
				normalizeSDKError(err),
			)
		}
		items = append(items, page.Items...)
		if page.Next == "" || len(page.Items) == 0 {
			break
		}
		offset += len(page.Items)
	}
	return items, nil
}

// currentPlaylistItemsPage models Spotify's February 2026 /items response.
// The upstream zmb3 v2.4.3 package still models the legacy /tracks route.
type currentPlaylistItemsPage struct {
	Items []currentPlaylistItem `json:"items"`
	Next  string                `json:"next"`
}

type currentPlaylistItem struct {
	AddedAt string          `json:"added_at"`
	IsLocal bool            `json:"is_local"`
	Item    json.RawMessage `json:"item"`
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
			if item.IsLocal || item.Track.Track == nil ||
				item.Track.Track.ID == "" ||
				(item.Track.Track.IsPlayable != nil && !*item.Track.Track.IsPlayable) {
				continue
			}
			tracks = append(tracks, metadataFromFullTrack(item.Track.Track))
		}

	case SpotifyObjectTypeAlbum:
		album, err := s.spotifyClient.GetAlbum(ctx, id)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get album: %w", normalizeSDKError(err))
		}
		count = int(album.Tracks.Total)

		// Get all album tracks (handle pagination)
		var allTracks []spotify.SimpleTrack
		offset := 0
		limit := 50
		for {
			albumTracks, err := s.spotifyClient.GetAlbumTracks(ctx, id, spotify.Limit(limit), spotify.Offset(offset))
			if err != nil {
				return 0, nil, fmt.Errorf(
					"failed to get album tracks: %w",
					normalizeSDKError(err),
				)
			}
			allTracks = append(allTracks, albumTracks.Tracks...)
			if len(albumTracks.Tracks) < limit {
				break
			}
			offset += limit
		}

		for _, track := range allTracks {
			tracks = append(tracks, metadataFromSimpleTrack(track, album.Name))
		}

	case SpotifyObjectTypeTrack:
		track, err := s.spotifyClient.GetTrack(ctx, id)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get track: %w", normalizeSDKError(err))
		}
		count = 1
		tracks = append(tracks, metadataFromFullTrack(track))

	default:
		return 0, nil, fmt.Errorf("unsupported object type: %s", objectType)
	}

	return count, tracks, nil
}

func normalizeSDKError(err error) error {
	var apiError spotify.Error
	if !errors.As(err, &apiError) {
		return err
	}
	return &APIError{
		StatusCode: apiError.Status,
		Message:    apiError.Message,
	}
}

func metadataFromFullTrack(track *spotify.FullTrack) TrackMetadata {
	isrc := track.ExternalIDs["isrc"]
	if isrc == "" {
		isrc = track.SimpleTrack.ExternalIDs.ISRC
	}
	return TrackMetadata{
		SpotifyURL: sGetTrackURL(track.ID),
		SpotifyID:  string(track.ID),
		Artist:     normalizedArtists(track.Artists),
		Title:      strings.ToLower(track.Name),
		Album:      strings.ToLower(track.Album.Name),
		ISRC:       isrc,
		DurationMS: int(track.Duration),
		Explicit:   track.Explicit,
		Version:    versionMarker(track.Name),
	}
}

func metadataFromSimpleTrack(track spotify.SimpleTrack, album string) TrackMetadata {
	return TrackMetadata{
		SpotifyURL: sGetTrackURL(track.ID),
		SpotifyID:  string(track.ID),
		Artist:     normalizedArtists(track.Artists),
		Title:      strings.ToLower(track.Name),
		Album:      strings.ToLower(album),
		ISRC:       track.ExternalIDs.ISRC,
		DurationMS: int(track.Duration),
		Explicit:   track.Explicit,
		Version:    versionMarker(track.Name),
	}
}

func normalizedArtists(artists []spotify.SimpleArtist) string {
	names := make([]string, 0, len(artists))
	for _, artist := range artists {
		names = append(names, strings.ToLower(artist.Name))
	}
	return strings.Join(names, ", ")
}

func sGetTrackURL(trackID spotify.ID) string {
	return fmt.Sprintf("https://open.spotify.com/track/%s", string(trackID))
}

func versionMarker(title string) string {
	lowerTitle := strings.ToLower(title)
	for _, marker := range []string{
		"live",
		"remix",
		"remaster",
		"acoustic",
		"instrumental",
		"karaoke",
		"cover",
		"sped up",
		"slowed",
		"nightcore",
		"radio edit",
	} {
		if strings.Contains(lowerTitle, marker) {
			return marker
		}
	}
	return ""
}
