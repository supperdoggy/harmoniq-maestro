package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/db"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/album-queue/pkg/handler"
	"github.com/supperdoggy/spot-models/spotify"
	"go.uber.org/zap"
	"gopkg.in/tucnak/telebot.v2"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatal("Failed to load config", zap.Error(err))
	}

	log.Info("Loaded config")

	bot, err := telebot.NewBot(telebot.Settings{
		Token:  cfg.BotToken,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal("Failed to create bot", zap.Error(err))
	}

	database, err := db.NewDatabase(ctx, log, cfg.DatabaseURL, cfg.DatabaseName)
	if err != nil {
		log.Fatal("Failed to create database connection", zap.Error(err))
	}

	log.Info("Database connection established")

	spotifyService := spotify.NewSpotifyService(ctx, cfg.SpotifyClientID, cfg.SpotifyClientSecret, log)
	log.Info("Spotify service initialized")

	// Health check server with graceful shutdown
	srv := &http.Server{Addr: ":8080"}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ready"))
	})
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats, err := database.GetStats(r.Context())
		if err != nil {
			log.Error("Failed to get stats", zap.Error(err))
			http.Error(w, "Failed to get stats", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})

	go func() {
		log.Info("Starting health check server on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Health check server error", zap.Error(err))
		}
	}()

	// Periodic stats logging (every 30 minutes)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				stats, err := database.GetStats(ctx)
				if err != nil {
					log.Error("Failed to get stats for periodic log", zap.Error(err))
					continue
				}
				log.Info("periodic_stats",
					zap.Int64("total_music_files", stats.TotalMusicFiles),
					zap.Int64("active_download_queue", stats.ActiveDownloadQueue),
					zap.Int64("total_download_requests", stats.TotalDownloadRequests),
					zap.Int64("active_playlists", stats.ActivePlaylists),
				)
			case <-ctx.Done():
				return
			}
		}
	}()

	h := handler.NewHandler(database, spotifyService, log, bot, cfg.WebhookURL, cfg.BotWhitelist)

	bot.Handle("/start", h.Start)
	bot.Handle(telebot.OnText, h.HandleText)
	bot.Handle("/queue", h.HandleQueue)
	bot.Handle("/failed", h.HandleFailed)
	bot.Handle(handler.FailedPageCallbackEndpoint(), h.HandleFailedPage)
	bot.Handle("/redownload", h.HandleRedownload)
	bot.Handle("/deactivate", h.HandleDeactivate)
	bot.Handle("/p", h.HandlePlaylist)
	bot.Handle("/pnp", h.HandlePlaylistNoPull)
	bot.Handle("/subscribe", h.HandleSubscribe)
	bot.Handle("/unsubscribe", h.HandleUnsubscribe)
	bot.Handle("/subscriptions", h.HandleListSubscriptions)

	// Graceful shutdown
	shutdownDone := make(chan struct{})
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Info("Shutting down...")

		// Shutdown HTTP server first while bot is still running
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("HTTP server shutdown error", zap.Error(err))
		}
		log.Info("HTTP server stopped")

		cancel()

		// Stop bot last - this unblocks bot.Start() in main goroutine
		bot.Stop()
		log.Info("Bot stopped")

		close(shutdownDone)
	}()

	log.Info("Bot is running", zap.String("username", bot.Me.Username))
	bot.Start()

	// Wait for shutdown to complete before exiting
	<-shutdownDone
	log.Info("Shutdown complete")
}
