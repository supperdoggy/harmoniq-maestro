package service

import (
	"context"
	"sync"

	"github.com/supperdoggy/spot-models"
	"go.uber.org/zap"
)

type GenreMapper interface {
	GetSimplifiedGenre(genre string) string
	ClassifyGenreIfEmpty(ctx context.Context, genre, artist, title, album string) (string, error)
}

type GenreClassifier struct {
	mapper GenreMapper
	log    *zap.Logger
	cache  map[string]string // cache for OpenAI classifications
	mu     sync.RWMutex
}

func NewGenreClassifier(mapper GenreMapper, log *zap.Logger) *GenreClassifier {
	return &GenreClassifier{
		mapper: mapper,
		log:    log,
		cache:  make(map[string]string),
	}
}

// EnrichMusicFileWithSimplifiedGenre adds simplified_genre to music files
// This is done on-the-fly during query execution
func (gc *GenreClassifier) EnrichMusicFileWithSimplifiedGenre(ctx context.Context, file models.MusicFile) (string, error) {
	// Check cache first
	cacheKey := file.Artist + "|" + file.Title + "|" + file.Album
	gc.mu.RLock()
	if simplified, ok := gc.cache[cacheKey]; ok {
		gc.mu.RUnlock()
		return simplified, nil
	}
	gc.mu.RUnlock()

	// Get or classify simplified genre
	simplified, err := gc.mapper.ClassifyGenreIfEmpty(ctx, file.Genre, file.Artist, file.Title, file.Album)
	if err != nil {
		return "", err
	}

	// Cache the result
	gc.mu.Lock()
	gc.cache[cacheKey] = simplified
	gc.mu.Unlock()

	return simplified, nil
}
