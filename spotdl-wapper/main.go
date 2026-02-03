package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/supperdoggy/SmartHomeServer/music-services/spotdl-wapper/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/music-services/spotdl-wapper/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/music-services/spotdl-wapper/pkg/loki"
	"github.com/supperdoggy/SmartHomeServer/music-services/spotdl-wapper/pkg/service"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	run()
	time.Sleep(5 * time.Second)
}

func run() {
	ctx := context.Background()

	cfg, err := config.NewConfig()
	if err != nil {
		panic(err)
	}

	log := buildLogger(cfg)
	defer log.Sync()

	log.Info("loaded config", zap.Any("config", cfg))

	spotifyService := spotify.NewSpotifyService(ctx, cfg.Spotify.ClientID, cfg.Spotify.ClientSecret, log)

	database, err := db.NewDatabase(ctx, log, cfg.DatabaseURL, cfg.DatabaseName)
	if err != nil {
		log.Fatal("failed to connect to database", zap.Error(err))
	}

	log.Info("connected to database")

	srv := service.NewService(database, log, spotifyService, cfg.Destination, cfg.MusicLibraryPath, cfg.SleepInMinutes)

	if err := srv.StartProcessing(ctx); err != nil {
		log.Fatal("failed to start processing", zap.Error(err))
	}

	defer func() {
		if r := recover(); r != nil {
			log.Error("recovered from panic", zap.Any("panic", r))
			run()
		}
	}()
}

func buildLogger(cfg *config.Config) *zap.Logger {
	// Console core (always enabled)
	consoleEncoderConfig := zap.NewDevelopmentEncoderConfig()
	consoleEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleEncoderConfig),
		zapcore.AddSync(os.Stdout),
		zapcore.DebugLevel,
	)

	// If Loki is enabled, create a tee core
	if cfg.Loki.Enabled && cfg.Loki.URL != "" {
		fmt.Printf("[loki] enabled, URL: %s\n", cfg.Loki.URL)
		lokiCore := loki.NewLokiCore(cfg.Loki.URL, map[string]string{
			"service": "spotdl-wrapper",
			"job":     "music-services",
		}, zapcore.InfoLevel)

		return zap.New(zapcore.NewTee(consoleCore, lokiCore))
	}

	fmt.Println("[loki] disabled (LOKI_ENABLED=false or LOKI_URL not set)")
	return zap.New(consoleCore)
}
