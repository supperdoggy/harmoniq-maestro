package config

import "github.com/kelseyhightower/envconfig"

type Config struct {
	DatabaseURL         string `envconfig:"DATABASE_URL" required:"true"`
	DatabaseName        string `envconfig:"DATABASE_NAME" required:"true"`
	OpenAIAPIKey        string `envconfig:"OPENAI_API_KEY" required:"true"`
	MusicLibraryPath    string `envconfig:"MUSIC_LIBRARY_PATH" required:"true"`
	PlaylistsOutputPath string `envconfig:"PLAYLISTS_OUTPUT_PATH" required:"true"`
	SpotifyClientID     string `envconfig:"SPOTIFY_CLIENT_ID" required:"true"`
	SpotifyClientSecret string `envconfig:"SPOTIFY_CLIENT_SECRET" required:"true"`
	DryRun              bool   `envconfig:"DRY_RUN" default:"false"`
}

func NewConfig() (*Config, error) {
	cfg := new(Config)
	err := envconfig.Process("", cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
