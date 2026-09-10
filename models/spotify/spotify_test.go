package spotify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestMetadataFromFullTrackPreservesCanonicalIdentity(t *testing.T) {
	track := &spotifyapi.FullTrack{
		SimpleTrack: spotifyapi.SimpleTrack{
			Artists:  []spotifyapi.SimpleArtist{{Name: "Main Artist"}, {Name: "Guest"}},
			Duration: 183000,
			Explicit: true,
			ID:       "spotify-id",
			Name:     "Song (Live)",
		},
		Album:       spotifyapi.SimpleAlbum{Name: "Album"},
		ExternalIDs: map[string]string{"isrc": "USRC17607839"},
	}

	metadata := metadataFromFullTrack(track)

	if metadata.SpotifyID != "spotify-id" {
		t.Fatalf("SpotifyID = %q, want spotify-id", metadata.SpotifyID)
	}
	if metadata.ISRC != "USRC17607839" {
		t.Fatalf("ISRC = %q, want USRC17607839", metadata.ISRC)
	}
	if metadata.Artist != "main artist, guest" {
		t.Fatalf("Artist = %q, want normalized artists", metadata.Artist)
	}
	if metadata.Album != "album" || metadata.DurationMS != 183000 || !metadata.Explicit {
		t.Fatalf("metadata fields not preserved: %#v", metadata)
	}
	if metadata.Version != "live" {
		t.Fatalf("Version = %q, want live", metadata.Version)
	}
}

func TestGetPlaylistTracksUsesCurrentItemsEndpoint(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 && request.URL.Path != "/v1/playlists/playlist-id/items" {
			t.Fatalf("path = %q, want current /items endpoint", request.URL.Path)
		}

		body := `{
			"items": [{
				"added_at": "2026-01-01T00:00:00Z",
				"is_local": false,
				"item": {
					"type": "track",
					"id": "track-id",
					"name": "Track",
					"duration_ms": 123000,
					"artists": [{"name": "Artist"}],
					"album": {"name": "Album"},
					"external_ids": {"isrc": "TESTISRC"}
				}
			}],
			"next": null
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	service := &spotifyService{httpClient: client, userAuth: true}
	items, err := service.GetPlaylistTracks(context.Background(), "https://open.spotify.com/playlist/playlist-id")
	if err != nil {
		t.Fatalf("GetPlaylistTracks() error = %v", err)
	}
	if len(items) != 1 || items[0].Track.Track == nil {
		t.Fatalf("items = %#v, want one track", items)
	}
	if items[0].Track.Track.ID != "track-id" {
		t.Fatalf("track ID = %q, want track-id", items[0].Track.Track.ID)
	}
}

func TestGetPlaylistTracksFiltersLocalItems(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{
			"items": [{
				"added_at": "2026-01-01T00:00:00Z",
				"is_local": true,
				"item": {
					"type": "track",
					"id": "local-track",
					"name": "Local Track",
					"artists": [{"name": "Artist"}]
				}
			}],
			"next": null
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	service := &spotifyService{httpClient: client, userAuth: true}
	items, err := service.GetPlaylistTracks(
		context.Background(),
		"https://open.spotify.com/playlist/playlist-id",
	)
	if err != nil {
		t.Fatalf("GetPlaylistTracks() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v, want local item filtered", items)
	}
}

func TestGetPlaylistTracksExplainsDevelopmentModeAuth(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	service := &spotifyService{httpClient: client}
	_, err := service.GetPlaylistTracks(context.Background(), "https://open.spotify.com/playlist/playlist-id")
	if err == nil || !strings.Contains(err.Error(), "refresh token") {
		t.Fatalf("error = %v, want refresh-token hint", err)
	}
}

func TestGetPlaylistTracksPreservesQuotaClassification(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Retry-After", "17")
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota exhausted","reason":"QUOTA_EXCEEDED"}}`)),
			Header:     header,
			Request:    request,
		}, nil
	})}

	service := &spotifyService{httpClient: client, userAuth: true}
	_, err := service.GetPlaylistTracks(context.Background(), "https://open.spotify.com/playlist/playlist-id")

	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiError.Reason != "QUOTA_EXCEEDED" || apiError.RetryAfter != 17*time.Second {
		t.Fatalf("API error = %#v, want quota classification and retry delay", apiError)
	}
}

func TestSDKRequestPreservesRateLimitClassification(t *testing.T) {
	body := &trackingReadCloser{
		Reader: strings.NewReader(`{"error":{"message":"quota exhausted","reason":"QUOTA_EXCEEDED"}}`),
	}
	client := &http.Client{Transport: rateLimitTransport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/playlists/playlist-id" {
			t.Fatalf("path = %q, want playlist metadata endpoint", request.URL.Path)
		}
		header := make(http.Header)
		header.Set("Retry-After", "86400")
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       body,
			Header:     header,
			Request:    request,
		}, nil
	})}}
	service := &spotifyService{
		spotifyClient: spotifyapi.New(client),
		httpClient:    client,
		log:           zap.NewNop(),
	}

	_, err := service.GetObjectName(
		context.Background(),
		"https://open.spotify.com/playlist/playlist-id",
	)

	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want *APIError through SDK request", err)
	}
	if apiError.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", apiError.StatusCode, http.StatusTooManyRequests)
	}
	if apiError.Message != "quota exhausted" || apiError.Reason != "QUOTA_EXCEEDED" {
		t.Fatalf("API error = %#v, want Spotify quota details", apiError)
	}
	if apiError.RetryAfter != 24*time.Hour {
		t.Fatalf("RetryAfter = %s, want 24h", apiError.RetryAfter)
	}
	if !body.closed {
		t.Fatal("429 response body was not closed")
	}
}

func TestRateLimitTransportPassesThroughNon429Response(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("temporarily unavailable")}
	wantResponse := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       body,
		Header:     make(http.Header),
	}
	transport := rateLimitTransport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		wantResponse.Request = request
		return wantResponse, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://api.spotify.com/v1/albums/id", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	if response != wantResponse {
		t.Fatal("RoundTrip() replaced the non-429 response")
	}
	if body.closed {
		t.Fatal("RoundTrip() closed a non-429 response body")
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(contents) != "temporarily unavailable" {
		t.Fatalf("body = %q, want passthrough contents", contents)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !body.closed {
		t.Fatal("caller could not close passthrough response body")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Second).Format(http.TimeFormat)
	past := now.Add(-time.Second).Format(http.TimeFormat)

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "delta seconds", value: "17", want: 17 * time.Second},
		{name: "HTTP date", value: future, want: 90 * time.Second},
		{name: "surrounding whitespace", value: " 3 ", want: 3 * time.Second},
		{name: "zero", value: "0", want: 0},
		{name: "negative", value: "-2", want: 0},
		{name: "past HTTP date", value: past, want: 0},
		{name: "invalid", value: "later", want: 0},
		{name: "empty", value: "", want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseRetryAfter(test.value, now); got != test.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", test.value, got, test.want)
			}
		})
	}
}

func TestNormalizeSDKErrorPreservesStatus(t *testing.T) {
	err := normalizeSDKError(spotifyapi.Error{
		Status:  http.StatusForbidden,
		Message: "forbidden",
	})
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiError.StatusCode != http.StatusForbidden || apiError.Message != "forbidden" {
		t.Fatalf("API error = %#v", apiError)
	}
}
