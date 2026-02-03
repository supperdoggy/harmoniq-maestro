package main

import (
	"context"
	"log"

	"go.uber.org/zap"

	"github.com/supperdoggy/SmartHomeServer/music-services/dynamic-playlists/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/music-services/dynamic-playlists/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/music-services/dynamic-playlists/pkg/genre"
	"github.com/supperdoggy/SmartHomeServer/music-services/dynamic-playlists/pkg/openai"
	"github.com/supperdoggy/SmartHomeServer/music-services/dynamic-playlists/pkg/service"
	"github.com/supperdoggy/spot-models"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting dynamic-playlists service")

	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	if cfg.DryRun {
		logger.Info("Running in DRY_RUN mode - no changes will be made")
	}

	ctx := context.Background()

	// Initialize database
	database, err := db.NewDatabase(ctx, logger, cfg.DatabaseURL, cfg.DatabaseName)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}

	// Test database connection
	if err := database.Ping(ctx); err != nil {
		logger.Fatal("Failed to ping database", zap.Error(err))
	}

	// Load genre mappings
	genreMappings, err := database.GetAllGenreMappings(ctx)
	if err != nil {
		logger.Warn("Failed to load genre mappings, continuing without mappings", zap.Error(err))
		genreMappings = []models.GenreMapping{}
	}
	logger.Info("Loaded genre mappings", zap.Int("count", len(genreMappings)))

	// Initialize OpenAI client
	openAIClient := openai.NewClient(cfg.OpenAIAPIKey, logger)

	// Initialize genre mapper
	genreMapper := genre.NewMapper(genreMappings, openAIClient, logger)

	// Initialize genre classifier
	genreClassifier := service.NewGenreClassifier(genreMapper, logger)

	// Initialize playlist generator (database implements PlaylistDB interface)
	playlistGenerator := service.NewPlaylistGenerator(
		database,
		genreClassifier,
		cfg.MusicLibraryPath,
		cfg.PlaylistsOutputPath,
		logger,
	)

	// Initialize service (database implements ServiceDB interface)
	svc := service.NewService(database, playlistGenerator, openAIClient, logger)

	// Process playlists
	logger.Info("Processing dynamic playlists...")
	if err := svc.ProcessPlaylists(ctx); err != nil {
		logger.Fatal("Failed to process playlists", zap.Error(err))
	}

	logger.Info("Dynamic playlists processing completed successfully")

	// Close database connection
	if err := database.Close(ctx); err != nil {
		logger.Warn("Error closing database connection", zap.Error(err))
	}

	logger.Info("Service stopped")
}
