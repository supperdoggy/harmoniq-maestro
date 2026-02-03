package utils

import "testing"

func TestIsValidSpotifyURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "valid album URL",
			url:      "https://open.spotify.com/album/1234567890",
			expected: true,
		},
		{
			name:     "valid playlist URL",
			url:      "https://open.spotify.com/playlist/abcdef",
			expected: true,
		},
		{
			name:     "valid track URL",
			url:      "https://open.spotify.com/track/xyz123",
			expected: true,
		},
		{
			name:     "invalid URL - wrong domain",
			url:      "https://spotify.com/album/123",
			expected: false,
		},
		{
			name:     "invalid URL - random website",
			url:      "https://google.com",
			expected: false,
		},
		{
			name:     "invalid URL - empty",
			url:      "",
			expected: false,
		},
		{
			name:     "invalid URL - not https",
			url:      "http://open.spotify.com/album/123",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidSpotifyURL(tt.url)
			if result != tt.expected {
				t.Errorf("IsValidSpotifyURL(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestInWhiteList(t *testing.T) {
	whitelist := []int64{123, 456, 789}

	tests := []struct {
		name     string
		userID   int64
		expected bool
	}{
		{
			name:     "user in whitelist",
			userID:   456,
			expected: true,
		},
		{
			name:     "user not in whitelist",
			userID:   999,
			expected: false,
		},
		{
			name:     "first user in whitelist",
			userID:   123,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InWhiteList(tt.userID, whitelist)
			if result != tt.expected {
				t.Errorf("InWhiteList(%d, %v) = %v, want %v", tt.userID, whitelist, result, tt.expected)
			}
		})
	}
}

func TestInWhiteList_EmptyList(t *testing.T) {
	result := InWhiteList(123, []int64{})
	if result {
		t.Error("InWhiteList should return false for empty whitelist")
	}
}
