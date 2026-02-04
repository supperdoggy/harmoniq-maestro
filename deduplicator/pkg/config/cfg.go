package config

import (
	"context"

	"github.com/sethvargo/go-envconfig"
	"github.com/supperdoggy/spot-models/database"
)

type Config struct {
	PlaylistsRoot     string `env:"PLAYLISTS_ROOT, default=./"`
	DestinationFolder string `env:"DESTINATION_FOLDER, default=./"`
	DuplicatesFolder  string `env:"DUPLICATES_FOLDER, default=./duplicates"`

	DatabaseConfig *database.DataBaseConfig `envconfig:"DATABASE_CONFIG"`
}

func NewConfig(ctx context.Context) (*Config, error) {
	cfg := Config{}
	err := envconfig.Process(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
