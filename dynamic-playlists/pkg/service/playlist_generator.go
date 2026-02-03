package service

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	models "github.com/supperdoggy/spot-models"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"
)

type PlaylistGenerator struct {
	db              PlaylistDB
	genreClassifier *GenreClassifier
	musicRoot       string
	outputPath      string
	log             *zap.Logger
}

type PlaylistDB interface {
	FindMusicFilesByQuery(ctx context.Context, query bson.M, sortBy string, limit int) ([]models.MusicFile, error)
	UpdateDynamicPlaylist(ctx context.Context, playlist models.DynamicPlaylist) error
}

func NewPlaylistGenerator(db PlaylistDB, genreClassifier *GenreClassifier, musicRoot, outputPath string, log *zap.Logger) *PlaylistGenerator {
	return &PlaylistGenerator{
		db:              db,
		genreClassifier: genreClassifier,
		musicRoot:       musicRoot,
		outputPath:      outputPath,
		log:             log,
	}
}

// GeneratePlaylist generates a playlist from a DynamicPlaylist definition
func (pg *PlaylistGenerator) GeneratePlaylist(ctx context.Context, playlist models.DynamicPlaylist) error {
	pg.log.Info("Generating playlist", zap.String("playlist_id", playlist.ID), zap.String("name", playlist.Name))

	// Normalize time-based queries (update created_at timestamps to be relative to now)
	normalizedQuery := pg.normalizeTimeQuery(playlist.Query, playlist.RefreshFrequency)

	// Execute query
	files, err := pg.db.FindMusicFilesByQuery(ctx, normalizedQuery, playlist.SortBy, playlist.MaxTracks)
	if err != nil {
		return err
	}

	pg.log.Info("Found files matching query", zap.Int("count", len(files)), zap.String("playlist_id", playlist.ID))

	// Filter files based on simplified_genre if needed
	// This is done post-query since simplified_genre is computed on-the-fly
	filteredFiles := make([]models.MusicFile, 0)
	for _, file := range files {
		// Check if query references simplified_genre
		if pg.queryNeedsSimplifiedGenre(playlist.Query) {
			simplified, err := pg.genreClassifier.EnrichMusicFileWithSimplifiedGenre(ctx, file)
			if err != nil {
				pg.log.Warn("Failed to enrich genre", zap.Error(err), zap.String("file_id", file.ID))
				continue
			}
			// Apply simplified_genre filter if present in query
			if !pg.matchesSimplifiedGenreFilter(playlist.Query, simplified) {
				continue
			}
		}
		filteredFiles = append(filteredFiles, file)
	}

	// Apply random shuffle if sort_by is "random"
	if playlist.SortBy == "random" && len(filteredFiles) > 0 {
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(filteredFiles), func(i, j int) {
			filteredFiles[i], filteredFiles[j] = filteredFiles[j], filteredFiles[i]
		})
	}

	// Limit tracks
	if playlist.MaxTracks > 0 && len(filteredFiles) > playlist.MaxTracks {
		filteredFiles = filteredFiles[:playlist.MaxTracks]
	}

	// Generate M3U file
	m3uPath := filepath.Join(pg.outputPath, playlist.Name+".m3u")
	if err := pg.createM3UPlaylist(filteredFiles, m3uPath); err != nil {
		return err
	}

	// Update playlist metadata
	playlist.TrackCount = len(filteredFiles)
	playlist.LastRefreshed = time.Now().Unix()
	playlist.OutputPath = m3uPath
	playlist.UpdatedAt = time.Now().Unix()

	if err := pg.db.UpdateDynamicPlaylist(ctx, playlist); err != nil {
		pg.log.Error("Failed to update playlist", zap.Error(err))
		return err
	}

	pg.log.Info("Playlist generated successfully", zap.String("playlist_id", playlist.ID), zap.Int("tracks", len(filteredFiles)))
	return nil
}

// normalizeTimeQuery updates time-based queries to use current timestamps
// This ensures queries like "created_at >= 7 days ago" stay fresh
func (pg *PlaylistGenerator) normalizeTimeQuery(query bson.M, refreshFrequency string) bson.M {
	// Deep copy the query to avoid modifying the original
	normalized := pg.deepCopyQuery(query)

	// Determine default lookback period based on refresh frequency
	var defaultDays int
	switch refreshFrequency {
	case "daily":
		defaultDays = 7 // Default to 7 days for daily refresh
	case "weekly":
		defaultDays = 30 // Default to 30 days for weekly refresh
	default:
		defaultDays = 7
	}

	// Update created_at filters
	pg.updateCreatedAtFilters(normalized, defaultDays)

	return normalized
}

// deepCopyQuery creates a deep copy of a bson.M query
func (pg *PlaylistGenerator) deepCopyQuery(query bson.M) bson.M {
	result := make(bson.M)
	for k, v := range query {
		switch val := v.(type) {
		case bson.M:
			result[k] = pg.deepCopyQuery(val)
		case bson.A:
			copied := make(bson.A, len(val))
			for i, item := range val {
				if nested, ok := item.(bson.M); ok {
					copied[i] = pg.deepCopyQuery(nested)
				} else {
					copied[i] = item
				}
			}
			result[k] = copied
		case []interface{}:
			copied := make([]interface{}, len(val))
			for i, item := range val {
				if nested, ok := item.(bson.M); ok {
					copied[i] = pg.deepCopyQuery(nested)
				} else {
					copied[i] = item
				}
			}
			result[k] = copied
		default:
			result[k] = v
		}
	}
	return result
}

// updateCreatedAtFilters recursively updates created_at filters in the query
func (pg *PlaylistGenerator) updateCreatedAtFilters(query bson.M, defaultDays int) {
	now := time.Now().Unix()

	// Check if this level has created_at with $gte
	if createdAt, ok := query["created_at"]; ok {
		if gte, ok := createdAt.(bson.M); ok {
			if timestamp, ok := gte["$gte"].(int64); ok {
				// Calculate how many days ago this timestamp represents
				daysAgo := (now - timestamp) / (24 * 60 * 60)

				// If it's approximately a round number of days (within 1 day), use that
				// Otherwise use defaultDays
				var days int
				if daysAgo >= 1 && daysAgo <= 365 {
					days = int(daysAgo)
				} else {
					days = defaultDays
				}

				// Update to be relative to now
				newTimestamp := now - int64(days*24*60*60)
				gte["$gte"] = newTimestamp
				pg.log.Debug("Updated created_at filter",
					zap.Int64("old_timestamp", timestamp),
					zap.Int64("new_timestamp", newTimestamp),
					zap.Int("days", days))
			}
		}
	}

	// Recursively process nested queries ($and, $or, etc.)
	for _, v := range query {
		switch val := v.(type) {
		case bson.M:
			pg.updateCreatedAtFilters(val, defaultDays)
		case bson.A:
			for _, item := range val {
				if nested, ok := item.(bson.M); ok {
					pg.updateCreatedAtFilters(nested, defaultDays)
				}
			}
		case []interface{}:
			for _, item := range val {
				if nested, ok := item.(bson.M); ok {
					pg.updateCreatedAtFilters(nested, defaultDays)
				}
			}
		}
	}
}

// GetSampleTracks gets a few sample tracks for description generation
func (pg *PlaylistGenerator) GetSampleTracks(ctx context.Context, query bson.M, limit int) ([]models.MusicFile, error) {
	return pg.db.FindMusicFilesByQuery(ctx, query, "created_at", limit)
}

func (pg *PlaylistGenerator) queryNeedsSimplifiedGenre(query bson.M) bool {
	// Check if query contains simplified_genre field
	return pg.hasField(query, "simplified_genre")
}

func (pg *PlaylistGenerator) hasField(m bson.M, field string) bool {
	for k, v := range m {
		if k == field {
			return true
		}
		if nested, ok := v.(bson.M); ok {
			if pg.hasField(nested, field) {
				return true
			}
		}
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if nested, ok := item.(bson.M); ok {
					if pg.hasField(nested, field) {
						return true
					}
				}
			}
		}
		if or, ok := v.(bson.A); ok {
			for _, item := range or {
				if nested, ok := item.(bson.M); ok {
					if pg.hasField(nested, field) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (pg *PlaylistGenerator) matchesSimplifiedGenreFilter(query bson.M, simplifiedGenre string) bool {
	// Simple check - if simplified_genre is in query, check if it matches
	if val, ok := query["simplified_genre"]; ok {
		if expected, ok := val.(string); ok {
			return strings.ToLower(expected) == strings.ToLower(simplifiedGenre)
		}
	}
	// If not explicitly filtered, allow all
	return true
}

func (pg *PlaylistGenerator) createM3UPlaylist(files []models.MusicFile, outputPath string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	// Remove existing file if it exists
	if _, err := os.Stat(outputPath); err == nil {
		if err := os.Remove(outputPath); err != nil {
			return err
		}
	}

	// Create M3U file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write M3U header
	if _, err := f.WriteString("#EXTM3U\n"); err != nil {
		return err
	}

	// Write tracks
	for _, file := range files {
		// Convert absolute path to relative path for M3U
		relPath, err := filepath.Rel(pg.musicRoot, file.Path)
		if err != nil {
			// If relative path fails, use absolute path
			relPath = file.Path
		}

		// Format as /music/... path
		relPath = strings.ReplaceAll(relPath, "..", "")
		relPath = strings.TrimPrefix(relPath, "/")
		if !strings.HasPrefix(relPath, "/music/") {
			relPath = "/music/Job-downloaded/" + relPath
		}

		// Write EXTINF line
		extinf := "#EXTINF:-1," + file.Artist + " - " + file.Title + "\n"
		if _, err := f.WriteString(extinf); err != nil {
			return err
		}

		// Write path
		if _, err := f.WriteString(relPath + "\n"); err != nil {
			return err
		}
	}

	return nil
}
