package config

import "github.com/kelseyhightower/envconfig"

type SpotifyConfig struct {
	ClientID     string `envconfig:"SPOTIFY_CLIENT_ID" required:"true"`
	ClientSecret string `envconfig:"SPOTIFY_CLIENT_SECRET" required:"true"`
}

type LokiConfig struct {
	Enabled bool   `envconfig:"LOKI_ENABLED" default:"false"`
	URL     string `envconfig:"LOKI_URL"`
}

type Config struct {
	Spotify SpotifyConfig
	Loki    LokiConfig

	DatabaseURL      string `envconfig:"DATABASE_URL" required:"true"`
	DatabaseName     string `envconfig:"DATABASE_NAME" required:"true"`
	Destination      string `envconfig:"DESTINATION" required:"true"`
	MusicLibraryPath string `envconfig:"MUSIC_LIBRARY_PATH" required:"true"`
	SleepInMinutes   int    `envconfig:"SLEEP_IN_MINUTES" required:"true"`
}

func NewConfig() (*Config, error) {
	cfg := new(Config)
	err := envconfig.Process("", cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
