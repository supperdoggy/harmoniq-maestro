package genre

import (
	"context"
	"strings"
	"sync"

	"github.com/supperdoggy/spot-models"
	"go.uber.org/zap"
)

type Mapper struct {
	mappings map[string]string // specific_genre -> simplified_genre
	openAI   GenreClassifier
	log      *zap.Logger
	mu       sync.RWMutex
}

type GenreClassifier interface {
	ClassifyGenre(ctx context.Context, artist, title, album string) (string, error)
}

func NewMapper(mappings []models.GenreMapping, openAI GenreClassifier, log *zap.Logger) *Mapper {
	mappingMap := make(map[string]string)
	for _, m := range mappings {
		// Store both lowercase versions for case-insensitive lookup
		mappingMap[strings.ToLower(m.SpecificGenre)] = strings.ToLower(m.SimplifiedGenre)
	}

	return &Mapper{
		mappings: mappingMap,
		openAI:   openAI,
		log:      log,
	}
}

// GetSimplifiedGenre returns the simplified genre for a given genre.
// If the genre is empty or not found in mappings, it returns empty string.
// The caller should use ClassifyGenreIfEmpty for OpenAI classification.
func (m *Mapper) GetSimplifiedGenre(genre string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if genre == "" {
		return ""
	}

	genreLower := strings.ToLower(strings.TrimSpace(genre))
	if simplified, ok := m.mappings[genreLower]; ok {
		return simplified
	}

	// If not found, return the original genre (could be simplified already)
	return genreLower
}

// ClassifyGenreIfEmpty uses OpenAI to classify genre if it's empty
func (m *Mapper) ClassifyGenreIfEmpty(ctx context.Context, genre, artist, title, album string) (string, error) {
	if genre != "" {
		// Genre exists, just simplify it
		return m.GetSimplifiedGenre(genre), nil
	}

	// Genre is empty, use OpenAI to classify
	classified, err := m.openAI.ClassifyGenre(ctx, artist, title, album)
	if err != nil {
		return "", err
	}

	// Now simplify the classified genre
	return m.GetSimplifiedGenre(classified), nil
}

// AddMapping adds a new mapping (thread-safe)
func (m *Mapper) AddMapping(specificGenre, simplifiedGenre string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings[strings.ToLower(specificGenre)] = strings.ToLower(simplifiedGenre)
}
