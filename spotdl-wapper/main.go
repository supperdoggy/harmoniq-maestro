package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/acquisition"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/library"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/loki"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/spotdl-wapper/pkg/service"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "spotdl-wapper failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.NewConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	log, closeLogger := buildLogger(cfg)
	defer closeLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerID := resolveWorkerID(cfg.WorkerID)
	log.Info(
		"loaded runtime configuration",
		zap.String("acquisition_backend", cfg.AcquisitionBackend),
		zap.String("worker_id", workerID),
		zap.Duration("worker_poll_interval", cfg.WorkerPollInterval),
		zap.Duration("worker_lease_duration", cfg.WorkerLeaseDuration),
		zap.Duration("acquisition_command_timeout", cfg.AcquisitionCommandTimeout),
	)
	if cfg.LegacyDestinationUsed {
		log.Warn("DESTINATION is deprecated; set MEDIA_OUTPUT_TEMPLATE instead")
	}

	spotifyService := spotify.NewSpotifyServiceWithRefreshToken(
		ctx,
		cfg.Spotify.ClientID,
		cfg.Spotify.ClientSecret,
		cfg.Spotify.RefreshToken,
		log,
	)

	database, err := db.NewDatabase(ctx, log, cfg.DatabaseURL, cfg.DatabaseName)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer closeDatabase(log, database)

	log.Info("connected to database")

	runner := acquisition.ExecCommandRunner{}
	provider, err := buildAcquisitionProvider(cfg, runner)
	if err != nil {
		return fmt.Errorf("configure acquisition provider: %w", err)
	}
	importer, err := library.NewImporter(library.ImporterConfig{
		LibraryRoot:       cfg.MusicLibraryPath,
		StagingRoot:       cfg.AcquisitionStagingPath,
		OutputTemplate:    cfg.MediaOutputTemplate,
		FFmpegBinary:      cfg.FFmpegBinary,
		FFprobeBinary:     cfg.FFprobeBinary,
		CommandTimeout:    cfg.AcquisitionCommandTimeout,
		DurationTolerance: cfg.MediaDurationTolerance,
	}, runner)
	if err != nil {
		return fmt.Errorf("configure media importer: %w", err)
	}

	srv, err := service.NewService(
		database,
		log,
		spotifyService,
		provider,
		importer,
		service.Options{
			WorkerID:            workerID,
			LeaseDuration:       cfg.WorkerLeaseDuration,
			RetryDelay:          cfg.WorkerRetryDelay,
			MaxAttempts:         cfg.WorkerMaxAttempts,
			RequestDelay:        time.Duration(cfg.SleepInMinutes) * time.Minute,
			PlaylistsOutputPath: cfg.PlaylistsOutputPath,
			LibraryPath:         cfg.MusicLibraryPath,
		},
	)
	if err != nil {
		return fmt.Errorf("configure worker service: %w", err)
	}

	ticker := time.NewTicker(cfg.WorkerPollInterval)
	defer ticker.Stop()

	log.Info("worker started")
	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopping", zap.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		cleanupOrphanAttempts(log, importer, cfg)
		if err := srv.StartProcessing(ctx); err != nil {
			if ctx.Err() != nil {
				log.Info("processing interrupted by shutdown")
				return nil
			}
			log.Error("processing pass failed", zap.Error(err))
		}

		select {
		case <-ctx.Done():
			log.Info("worker stopping", zap.String("reason", ctx.Err().Error()))
			return nil
		case <-ticker.C:
		}
	}
}

func cleanupOrphanAttempts(log *zap.Logger, importer *library.Importer, cfg *config.Config) {
	orphanTTL := cfg.AcquisitionCommandTimeout
	if cfg.WorkerLeaseDuration > orphanTTL {
		orphanTTL = cfg.WorkerLeaseDuration
	}
	orphanTTL *= 2
	removed, err := importer.CleanupOrphans(time.Now().Add(-orphanTTL))
	if err != nil {
		log.Warn("failed to clean orphaned acquisition attempts", zap.Error(err))
		return
	}
	if removed > 0 {
		log.Info(
			"cleaned orphaned acquisition attempts",
			zap.Int("removed_directories", removed),
			zap.Duration("minimum_age", orphanTTL),
		)
	}
}

func buildAcquisitionProvider(
	cfg *config.Config,
	runner acquisition.CommandRunner,
) (acquisition.Provider, error) {
	switch cfg.AcquisitionBackend {
	case config.AcquisitionBackendSpotDL:
		return acquisition.NewSpotDLProvider(acquisition.SpotDLConfig{
			Binary:          cfg.SpotDLBinary,
			OutputDirectory: cfg.AcquisitionStagingPath,
			AudioFormat:     cfg.AcquisitionAudioFormat,
			UseConfig:       cfg.SpotDLUseConfig,
			DisableCache:    true,
			CommandTimeout:  cfg.AcquisitionCommandTimeout,
		}, runner)
	case config.AcquisitionBackendYTDLP:
		return acquisition.NewYTDLPProvider(acquisition.YTDLPConfig{
			Binary: cfg.YTDLPBinary,
			OutputTemplate: filepath.Join(
				cfg.AcquisitionStagingPath,
				"%(id)s.%(ext)s",
			),
			AudioFormat:    cfg.AcquisitionAudioFormat,
			SearchLimit:    cfg.YTDLPSearchLimit,
			MinimumScore:   cfg.YTDLPMinimumScore,
			CommandTimeout: cfg.AcquisitionCommandTimeout,
		}, runner)
	default:
		return nil, fmt.Errorf("unsupported acquisition backend %q", cfg.AcquisitionBackend)
	}
}

func closeDatabase(log *zap.Logger, closer interface{ Close(context.Context) error }) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := closer.Close(ctx); err != nil {
		log.Warn("failed to close database connection", zap.Error(err))
	}
}

func resolveWorkerID(configured string) string {
	if configured != "" {
		return configured
	}

	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}

	return fmt.Sprintf("spotdl-worker-%d", time.Now().UnixNano())
}

func buildLogger(cfg *config.Config) (*zap.Logger, func()) {
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
		lokiCore := loki.NewLokiCore(cfg.Loki.URL, map[string]string{
			"service": "spotdl-wrapper",
			"job":     "music-services",
		}, zapcore.InfoLevel)

		logger := zap.New(zapcore.NewTee(consoleCore, lokiCore))
		logger.Info("Loki log shipping enabled")
		return logger, func() {
			lokiCore.Stop()
			_ = logger.Sync()
		}
	}

	logger := zap.New(consoleCore)
	logger.Info("Loki log shipping disabled")
	return logger, func() {
		_ = logger.Sync()
	}
}
