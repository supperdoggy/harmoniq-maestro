package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var configEnvironmentKeys = []string{
	"SPOTIFY_CLIENT_ID",
	"SPOTIFY_CLIENT_SECRET",
	"SPOTIFY_REFRESH_TOKEN",
	"LOKI_ENABLED",
	"LOKI_URL",
	"DATABASE_URL",
	"DATABASE_NAME",
	"ACQUISITION_BACKEND",
	"ACQUISITION_AUDIO_FORMAT",
	"ACQUISITION_COMMAND_TIMEOUT",
	"ACQUISITION_STAGING_PATH",
	"YTDLP_SEARCH_LIMIT",
	"YTDLP_MINIMUM_SCORE",
	"WORKER_POLL_INTERVAL",
	"WORKER_ID",
	"WORKER_LEASE_DURATION",
	"WORKER_RETRY_DELAY",
	"WORKER_MAX_ATTEMPTS",
	"MEDIA_OUTPUT_TEMPLATE",
	"PLAYLISTS_OUTPUT_PATH",
	"MUSIC_LIBRARY_PATH",
	"SPOTDL_BINARY",
	"SPOTDL_USE_CONFIG",
	"YTDLP_BINARY",
	"FFMPEG_BINARY",
	"FFPROBE_BINARY",
	"MEDIA_DURATION_TOLERANCE",
	"DESTINATION",
	"SLEEP_IN_MINUTES",
}

func TestNewConfigDefaults(t *testing.T) {
	setBaseEnvironment(t)

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.AcquisitionBackend != AcquisitionBackendSpotDL {
		t.Errorf("AcquisitionBackend = %q, want %q", cfg.AcquisitionBackend, AcquisitionBackendSpotDL)
	}
	if cfg.AcquisitionAudioFormat != "mp3" {
		t.Errorf("AcquisitionAudioFormat = %q, want mp3", cfg.AcquisitionAudioFormat)
	}
	if cfg.AcquisitionCommandTimeout != 30*time.Minute {
		t.Errorf("AcquisitionCommandTimeout = %v, want %v", cfg.AcquisitionCommandTimeout, 30*time.Minute)
	}
	if cfg.AcquisitionStagingPath != "/music/.staging" {
		t.Errorf("AcquisitionStagingPath = %q, want /music/.staging", cfg.AcquisitionStagingPath)
	}
	if cfg.YTDLPSearchLimit != 10 {
		t.Errorf("YTDLPSearchLimit = %d, want 10", cfg.YTDLPSearchLimit)
	}
	if cfg.YTDLPMinimumScore != 0.72 {
		t.Errorf("YTDLPMinimumScore = %v, want 0.72", cfg.YTDLPMinimumScore)
	}
	if cfg.WorkerPollInterval != time.Minute {
		t.Errorf("WorkerPollInterval = %v, want %v", cfg.WorkerPollInterval, time.Minute)
	}
	if cfg.WorkerLeaseDuration != 45*time.Minute {
		t.Errorf("WorkerLeaseDuration = %v, want %v", cfg.WorkerLeaseDuration, 45*time.Minute)
	}
	if cfg.WorkerRetryDelay != 15*time.Minute {
		t.Errorf("WorkerRetryDelay = %v, want %v", cfg.WorkerRetryDelay, 15*time.Minute)
	}
	if cfg.WorkerMaxAttempts != 3 {
		t.Errorf("WorkerMaxAttempts = %d, want 3", cfg.WorkerMaxAttempts)
	}
	if cfg.PlaylistsOutputPath != filepath.Join("/music", "playlists") {
		t.Errorf("PlaylistsOutputPath = %q, want %q", cfg.PlaylistsOutputPath, filepath.Join("/music", "playlists"))
	}
	if cfg.SpotDLBinary != "/usr/local/bin/spotdl" {
		t.Errorf("SpotDLBinary = %q, want /usr/local/bin/spotdl", cfg.SpotDLBinary)
	}
	if cfg.YTDLPBinary != "/usr/local/bin/yt-dlp" {
		t.Errorf("YTDLPBinary = %q, want /usr/local/bin/yt-dlp", cfg.YTDLPBinary)
	}
	if !cfg.SpotDLUseConfig {
		t.Error("SpotDLUseConfig = false, want true")
	}
	if cfg.FFmpegBinary != "ffmpeg" {
		t.Errorf("FFmpegBinary = %q, want ffmpeg", cfg.FFmpegBinary)
	}
	if cfg.FFprobeBinary != "ffprobe" {
		t.Errorf("FFprobeBinary = %q, want ffprobe", cfg.FFprobeBinary)
	}
	if cfg.MediaDurationTolerance != 15*time.Second {
		t.Errorf("MediaDurationTolerance = %v, want %v", cfg.MediaDurationTolerance, 15*time.Second)
	}
	if cfg.SleepInMinutes != 1 {
		t.Errorf("SleepInMinutes = %d, want 1", cfg.SleepInMinutes)
	}
	if cfg.LegacyDestinationUsed {
		t.Error("LegacyDestinationUsed = true, want false")
	}
}

func TestNewConfigUsesDeprecatedDestinationFallback(t *testing.T) {
	setBaseEnvironment(t)
	t.Setenv("MEDIA_OUTPUT_TEMPLATE", "")
	t.Setenv("DESTINATION", " /legacy/{artists}-{title}.{output-ext} ")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.MediaOutputTemplate != "/legacy/{artists}-{title}.{output-ext}" {
		t.Errorf("MediaOutputTemplate = %q, want legacy destination", cfg.MediaOutputTemplate)
	}
	if !cfg.LegacyDestinationUsed {
		t.Error("LegacyDestinationUsed = false, want true")
	}
}

func TestNewConfigPrefersMediaOutputTemplate(t *testing.T) {
	setBaseEnvironment(t)
	t.Setenv("MEDIA_OUTPUT_TEMPLATE", "/new/{title}.{output-ext}")
	t.Setenv("DESTINATION", "/legacy/{title}.{output-ext}")
	t.Setenv("PLAYLISTS_OUTPUT_PATH", "/music/custom-playlists")
	t.Setenv("ACQUISITION_BACKEND", "YT-DLP")
	t.Setenv("SPOTIFY_REFRESH_TOKEN", "refresh-token")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.MediaOutputTemplate != "/new/{title}.{output-ext}" {
		t.Errorf("MediaOutputTemplate = %q, want new template", cfg.MediaOutputTemplate)
	}
	if cfg.LegacyDestinationUsed {
		t.Error("LegacyDestinationUsed = true, want false")
	}
	if cfg.PlaylistsOutputPath != "/music/custom-playlists" {
		t.Errorf("PlaylistsOutputPath = %q, want explicit path", cfg.PlaylistsOutputPath)
	}
	if cfg.AcquisitionBackend != AcquisitionBackendYTDLP {
		t.Errorf("AcquisitionBackend = %q, want %q", cfg.AcquisitionBackend, AcquisitionBackendYTDLP)
	}
	if cfg.Spotify.RefreshToken != "refresh-token" {
		t.Errorf("Spotify.RefreshToken = %q, want configured token", cfg.Spotify.RefreshToken)
	}
}

func TestNewConfigRejectsInvalidBackend(t *testing.T) {
	setBaseEnvironment(t)
	t.Setenv("ACQUISITION_BACKEND", "other")

	if _, err := NewConfig(); err == nil {
		t.Fatal("NewConfig() error = nil, want invalid backend error")
	}
}

func TestNewConfigParsesAcquisitionAndRetrySettings(t *testing.T) {
	setBaseEnvironment(t)
	t.Setenv("ACQUISITION_AUDIO_FORMAT", " FLAC ")
	t.Setenv("YTDLP_SEARCH_LIMIT", "50")
	t.Setenv("YTDLP_MINIMUM_SCORE", "1")
	t.Setenv("WORKER_RETRY_DELAY", "45s")
	t.Setenv("WORKER_MAX_ATTEMPTS", "1")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.AcquisitionAudioFormat != "flac" {
		t.Errorf("AcquisitionAudioFormat = %q, want flac", cfg.AcquisitionAudioFormat)
	}
	if cfg.YTDLPSearchLimit != 50 {
		t.Errorf("YTDLPSearchLimit = %d, want 50", cfg.YTDLPSearchLimit)
	}
	if cfg.YTDLPMinimumScore != 1 {
		t.Errorf("YTDLPMinimumScore = %v, want 1", cfg.YTDLPMinimumScore)
	}
	if cfg.WorkerRetryDelay != 45*time.Second {
		t.Errorf("WorkerRetryDelay = %v, want %v", cfg.WorkerRetryDelay, 45*time.Second)
	}
	if cfg.WorkerMaxAttempts != 1 {
		t.Errorf("WorkerMaxAttempts = %d, want 1", cfg.WorkerMaxAttempts)
	}
}

func TestNewConfigRejectsMissingOutputTemplate(t *testing.T) {
	setBaseEnvironment(t)
	t.Setenv("MEDIA_OUTPUT_TEMPLATE", "")
	t.Setenv("DESTINATION", "")

	if _, err := NewConfig(); err == nil {
		t.Fatal("NewConfig() error = nil, want missing output template error")
	}
}

func TestNewConfigRejectsInvalidDurationsAndDelay(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "zero command timeout", key: "ACQUISITION_COMMAND_TIMEOUT", value: "0s"},
		{name: "negative poll interval", key: "WORKER_POLL_INTERVAL", value: "-1s"},
		{name: "short lease duration", key: "WORKER_LEASE_DURATION", value: "2s"},
		{name: "zero retry delay", key: "WORKER_RETRY_DELAY", value: "0s"},
		{name: "negative media duration tolerance", key: "MEDIA_DURATION_TOLERANCE", value: "-1s"},
		{name: "negative request delay", key: "SLEEP_IN_MINUTES", value: "-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setBaseEnvironment(t)
			t.Setenv(test.key, test.value)

			if _, err := NewConfig(); err == nil {
				t.Fatalf("NewConfig() error = nil for %s=%s", test.key, test.value)
			}
		})
	}
}

func TestNewConfigRejectsInvalidAcquisitionAndRetrySettings(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "empty audio format", key: "ACQUISITION_AUDIO_FORMAT", value: ""},
		{name: "search limit below range", key: "YTDLP_SEARCH_LIMIT", value: "0"},
		{name: "search limit above range", key: "YTDLP_SEARCH_LIMIT", value: "51"},
		{name: "score below range", key: "YTDLP_MINIMUM_SCORE", value: "-0.01"},
		{name: "score above range", key: "YTDLP_MINIMUM_SCORE", value: "1.01"},
		{name: "score not a number", key: "YTDLP_MINIMUM_SCORE", value: "NaN"},
		{name: "attempts below range", key: "WORKER_MAX_ATTEMPTS", value: "0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setBaseEnvironment(t)
			t.Setenv(test.key, test.value)

			if _, err := NewConfig(); err == nil {
				t.Fatalf("NewConfig() error = nil for %s=%s", test.key, test.value)
			}
		})
	}
}

func setBaseEnvironment(t *testing.T) {
	t.Helper()

	for _, key := range configEnvironmentKeys {
		unsetEnvironment(t, key)
	}

	t.Setenv("SPOTIFY_CLIENT_ID", "client-id")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "client-secret")
	t.Setenv("DATABASE_URL", "mongodb://localhost:27017")
	t.Setenv("DATABASE_NAME", "test")
	t.Setenv("MUSIC_LIBRARY_PATH", "/music")
	t.Setenv("MEDIA_OUTPUT_TEMPLATE", "/music/downloads/{artists}-{title}.{output-ext}")
}

func unsetEnvironment(t *testing.T, key string) {
	t.Helper()

	value, present := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, value)
			return
		}
		_ = os.Unsetenv(key)
	})
}
