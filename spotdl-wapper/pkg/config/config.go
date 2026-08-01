package config

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const (
	AcquisitionBackendSpotDL = "spotdl"
	AcquisitionBackendYTDLP  = "yt-dlp"
)

type SpotifyConfig struct {
	ClientID     string `envconfig:"SPOTIFY_CLIENT_ID" required:"true"`
	ClientSecret string `envconfig:"SPOTIFY_CLIENT_SECRET" required:"true"`
	RefreshToken string `envconfig:"SPOTIFY_REFRESH_TOKEN"`
}

type LokiConfig struct {
	Enabled bool   `envconfig:"LOKI_ENABLED" default:"false"`
	URL     string `envconfig:"LOKI_URL"`
}

type Config struct {
	Spotify SpotifyConfig
	Loki    LokiConfig

	DatabaseURL  string `envconfig:"DATABASE_URL" required:"true"`
	DatabaseName string `envconfig:"DATABASE_NAME" required:"true"`

	AcquisitionBackend        string        `envconfig:"ACQUISITION_BACKEND" default:"spotdl"`
	AcquisitionAudioFormat    string        `envconfig:"ACQUISITION_AUDIO_FORMAT" default:"mp3"`
	AcquisitionCommandTimeout time.Duration `envconfig:"ACQUISITION_COMMAND_TIMEOUT" default:"30m"`
	AcquisitionStagingPath    string        `envconfig:"ACQUISITION_STAGING_PATH" default:"/music/.staging"`
	YTDLPSearchLimit          int           `envconfig:"YTDLP_SEARCH_LIMIT" default:"10"`
	YTDLPMinimumScore         float64       `envconfig:"YTDLP_MINIMUM_SCORE" default:"0.72"`
	WorkerPollInterval        time.Duration `envconfig:"WORKER_POLL_INTERVAL" default:"1m"`
	WorkerID                  string        `envconfig:"WORKER_ID"`
	WorkerLeaseDuration       time.Duration `envconfig:"WORKER_LEASE_DURATION" default:"45m"`
	WorkerRetryDelay          time.Duration `envconfig:"WORKER_RETRY_DELAY" default:"15m"`
	WorkerMaxAttempts         int           `envconfig:"WORKER_MAX_ATTEMPTS" default:"3"`

	MediaOutputTemplate string `envconfig:"MEDIA_OUTPUT_TEMPLATE"`
	PlaylistsOutputPath string `envconfig:"PLAYLISTS_OUTPUT_PATH"`
	MusicLibraryPath    string `envconfig:"MUSIC_LIBRARY_PATH" required:"true"`

	SpotDLBinary    string `envconfig:"SPOTDL_BINARY" default:"/usr/local/bin/spotdl"`
	SpotDLUseConfig bool   `envconfig:"SPOTDL_USE_CONFIG" default:"true"`
	YTDLPBinary     string `envconfig:"YTDLP_BINARY" default:"/usr/local/bin/yt-dlp"`
	FFmpegBinary    string `envconfig:"FFMPEG_BINARY" default:"ffmpeg"`
	FFprobeBinary   string `envconfig:"FFPROBE_BINARY" default:"ffprobe"`

	MediaDurationTolerance time.Duration `envconfig:"MEDIA_DURATION_TOLERANCE" default:"15s"`

	// Destination is a deprecated compatibility alias for MediaOutputTemplate.
	// New deployments should set MEDIA_OUTPUT_TEMPLATE instead.
	Destination string `envconfig:"DESTINATION"`

	// SleepInMinutes is the legacy delay between individual requests within a
	// processing pass. WorkerPollInterval controls the delay between passes.
	SleepInMinutes int `envconfig:"SLEEP_IN_MINUTES" default:"1"`

	LegacyDestinationUsed bool `ignored:"true"`
}

func NewConfig() (*Config, error) {
	cfg := new(Config)
	if err := envconfig.Process("", cfg); err != nil {
		return nil, err
	}

	cfg.AcquisitionBackend = strings.ToLower(strings.TrimSpace(cfg.AcquisitionBackend))
	cfg.AcquisitionAudioFormat = strings.ToLower(strings.TrimSpace(cfg.AcquisitionAudioFormat))
	cfg.AcquisitionStagingPath = strings.TrimSpace(cfg.AcquisitionStagingPath)
	cfg.MediaOutputTemplate = strings.TrimSpace(cfg.MediaOutputTemplate)
	cfg.Destination = strings.TrimSpace(cfg.Destination)
	cfg.PlaylistsOutputPath = strings.TrimSpace(cfg.PlaylistsOutputPath)
	cfg.MusicLibraryPath = strings.TrimSpace(cfg.MusicLibraryPath)
	cfg.SpotDLBinary = strings.TrimSpace(cfg.SpotDLBinary)
	cfg.YTDLPBinary = strings.TrimSpace(cfg.YTDLPBinary)
	cfg.FFmpegBinary = strings.TrimSpace(cfg.FFmpegBinary)
	cfg.FFprobeBinary = strings.TrimSpace(cfg.FFprobeBinary)
	cfg.WorkerID = strings.TrimSpace(cfg.WorkerID)

	if cfg.MediaOutputTemplate == "" && cfg.Destination != "" {
		cfg.MediaOutputTemplate = cfg.Destination
		cfg.LegacyDestinationUsed = true
	}
	if cfg.PlaylistsOutputPath == "" && cfg.MusicLibraryPath != "" {
		cfg.PlaylistsOutputPath = filepath.Join(cfg.MusicLibraryPath, "playlists")
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) validate() error {
	requiredValues := []struct {
		name  string
		value string
	}{
		{name: "DATABASE_URL", value: cfg.DatabaseURL},
		{name: "DATABASE_NAME", value: cfg.DatabaseName},
		{name: "SPOTIFY_CLIENT_ID", value: cfg.Spotify.ClientID},
		{name: "SPOTIFY_CLIENT_SECRET", value: cfg.Spotify.ClientSecret},
		{name: "MUSIC_LIBRARY_PATH", value: cfg.MusicLibraryPath},
		{name: "MEDIA_OUTPUT_TEMPLATE (or deprecated DESTINATION)", value: cfg.MediaOutputTemplate},
		{name: "PLAYLISTS_OUTPUT_PATH", value: cfg.PlaylistsOutputPath},
		{name: "ACQUISITION_STAGING_PATH", value: cfg.AcquisitionStagingPath},
		{name: "ACQUISITION_AUDIO_FORMAT", value: cfg.AcquisitionAudioFormat},
		{name: "FFMPEG_BINARY", value: cfg.FFmpegBinary},
		{name: "FFPROBE_BINARY", value: cfg.FFprobeBinary},
	}
	for _, required := range requiredValues {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("%s must not be empty", required.name)
		}
	}

	switch cfg.AcquisitionBackend {
	case AcquisitionBackendSpotDL:
		if cfg.SpotDLBinary == "" {
			return fmt.Errorf("SPOTDL_BINARY must not be empty when ACQUISITION_BACKEND=%s", AcquisitionBackendSpotDL)
		}
	case AcquisitionBackendYTDLP:
		if cfg.YTDLPBinary == "" {
			return fmt.Errorf("YTDLP_BINARY must not be empty when ACQUISITION_BACKEND=%s", AcquisitionBackendYTDLP)
		}
	default:
		return fmt.Errorf(
			"ACQUISITION_BACKEND must be %q or %q",
			AcquisitionBackendSpotDL,
			AcquisitionBackendYTDLP,
		)
	}

	if cfg.AcquisitionCommandTimeout <= 0 {
		return fmt.Errorf("ACQUISITION_COMMAND_TIMEOUT must be greater than zero")
	}
	if cfg.YTDLPSearchLimit < 1 || cfg.YTDLPSearchLimit > 50 {
		return fmt.Errorf("YTDLP_SEARCH_LIMIT must be between 1 and 50")
	}
	if math.IsNaN(cfg.YTDLPMinimumScore) || cfg.YTDLPMinimumScore < 0 || cfg.YTDLPMinimumScore > 1 {
		return fmt.Errorf("YTDLP_MINIMUM_SCORE must be between 0 and 1")
	}
	if cfg.WorkerPollInterval <= 0 {
		return fmt.Errorf("WORKER_POLL_INTERVAL must be greater than zero")
	}
	if cfg.WorkerLeaseDuration < 3*time.Second {
		return fmt.Errorf("WORKER_LEASE_DURATION must be at least 3s")
	}
	if cfg.WorkerRetryDelay <= 0 {
		return fmt.Errorf("WORKER_RETRY_DELAY must be greater than zero")
	}
	if cfg.WorkerMaxAttempts < 1 {
		return fmt.Errorf("WORKER_MAX_ATTEMPTS must be at least 1")
	}
	if cfg.MediaDurationTolerance < 0 {
		return fmt.Errorf("MEDIA_DURATION_TOLERANCE must not be negative")
	}
	if cfg.SleepInMinutes < 0 {
		return fmt.Errorf("SLEEP_IN_MINUTES must not be negative")
	}

	return nil
}
