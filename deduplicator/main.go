package main

import (
	"context"

	"github.com/supperdoggy/SmartHomeServer/music-services/deduplicator/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/music-services/deduplicator/pkg/service"
	"go.uber.org/zap"
)

func main() {
	log, _ := zap.NewDevelopment()
	ctx := context.Background()

	cfg, err := config.NewConfig(ctx)
	if err != nil {
		log.Fatal("failed to load config", zap.Error(err))
	}

	// db, err := database.NewDatabase(ctx, log, cfg.DatabaseConfig)
	// if err != nil {
	// 	log.Fatal("failed to connect to database", zap.Error(err))
	// }

	count, err := service.RunApp(log, cfg, nil)
	if err != nil {
		log.Fatal("failed to run app", zap.Error(err))
	}

	log.Info("done", zap.Int("count", count))

}
