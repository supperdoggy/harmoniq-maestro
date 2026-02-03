package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/bogem/id3v2"
	"github.com/go-flac/go-flac"
	"github.com/go-flac/flacvorbis"
	"github.com/kelseyhightower/envconfig"
	mp4tag "github.com/Sorrow446/go-mp4tag"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Config struct {
	DatabaseURL    string `envconfig:"DATABASE_URL" required:"true"`
	DatabaseName   string `envconfig:"DATABASE_NAME" required:"true"`
	CollectionName string `envconfig:"COLLECTION_NAME" default:"music-files"`
}

type MusicFile struct {
	ID       string         `bson:"_id"`
	Artist   string         `bson:"artist"`
	Album    string         `bson:"album"`
	Title    string         `bson:"title"`
	Genre    string         `bson:"genre"`
	Path     string         `bson:"path"`
	MetaData map[string]any `bson:"meta_data"`
}

// AlbumGroup represents a group of albums that normalize to the same name
type AlbumGroup struct {
	NormalizedName  string
	MusicBrainzID   string                 // If available from metadata
	Variations      map[string][]MusicFile // variation name -> songs with that variation
	HasMBIDConflict bool                   // True if same normalized name but different MBIDs
	MBIDVariations  map[string]string      // album variation -> its MBID
}

// MusicBrainz metadata keys to check (different tag formats use different keys)
var mbidKeys = []string{
	"TXXX:MusicBrainz Album Id",
	"MusicBrainz Album Id",
	"musicbrainz_albumid",
	"MUSICBRAINZ_ALBUMID",
	"----:com.apple.iTunes:MusicBrainz Album Id",
}

// BackupEntry represents a single backup entry for reverting album name changes
type BackupEntry struct {
	FilePath     string `json:"file_path"`
	OriginalAlbum string `json:"original_album"`
	NewAlbum     string `json:"new_album"`
	Timestamp    int64  `json:"timestamp"`
	TrackID      string `json:"track_id"`
}

// BackupLog represents the entire backup log
type BackupLog struct {
	Entries []BackupEntry `json:"entries"`
}

func main() {
	// Parse flags
	dryRun := flag.Bool("dry-run", true, "Only show what would be changed, don't modify database")
	showMetadata := flag.Bool("show-metadata", false, "Show raw metadata differences for albums")
	minVariations := flag.Int("min-variations", 2, "Minimum number of variations to show")
	targetAlbum := flag.String("album", "", "Target specific album name (normalized)")
	fixAlbum := flag.String("fix", "", "Fix a specific album by providing the target album name to use")
	listAll := flag.Bool("list", false, "List all unique albums")
	groupByMBID := flag.Bool("mbid", false, "Group albums by MusicBrainz Album ID (like Navidrome)")
	showNavidrome := flag.Bool("navidrome", false, "Show how Navidrome would group albums (MBID|album)")
	updateFiles := flag.Bool("update-files", false, "Also update album metadata in the actual music files (requires -dry-run=false)")
	restoreBackup := flag.String("restore", "", "Restore album metadata from backup file (path to backup JSON file)")
	backupFile := flag.String("backup-file", "album-normalizer-backup.json", "Path to backup file for storing original album names")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, collection, err := connectDB(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect(ctx)

	fmt.Println("Fetching music files from database...")
	files, err := fetchAllMusicFiles(ctx, collection)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch music files: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d music files\n\n", len(files))

	// Handle restore operation
	if *restoreBackup != "" {
		err := restoreFromBackup(*restoreBackup, ctx, collection)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restore from backup: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *listAll {
		listAllAlbums(files)
		return
	}

	// Show Navidrome-style grouping
	if *showNavidrome {
		showNavidromeGrouping(files)
		return
	}

	// Group albums - either by MBID or by normalized name
	var groups map[string]AlbumGroup
	if *groupByMBID {
		groups = groupByMusicBrainzID(files)
		fmt.Println("Grouping by MusicBrainz Album ID...")
	} else {
		groups = groupByNormalizedAlbum(files)
	}

	// Filter to only groups with variations
	var variedGroups []AlbumGroup
	for _, group := range groups {
		if len(group.Variations) >= *minVariations {
			variedGroups = append(variedGroups, group)
		}
	}

	// Sort by number of variations (most first) then by track count
	sort.Slice(variedGroups, func(i, j int) bool {
		if len(variedGroups[i].Variations) != len(variedGroups[j].Variations) {
			return len(variedGroups[i].Variations) > len(variedGroups[j].Variations)
		}
		return getTotalTracks(variedGroups[i]) > getTotalTracks(variedGroups[j])
	})

	// Handle specific album target
	if *targetAlbum != "" {
		normalized := normalizeAlbumName(*targetAlbum)
		for _, group := range variedGroups {
			if group.NormalizedName == normalized {
				printAlbumGroup(group, *showMetadata)

				if *fixAlbum != "" && !*dryRun {
					err := fixAlbumName(ctx, collection, group, *fixAlbum, *updateFiles, *backupFile)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Failed to fix album: %v\n", err)
						os.Exit(1)
					}
					fmt.Printf("\n✓ Successfully updated all songs to use album name: %q\n", *fixAlbum)
				}
				return
			}
		}
		fmt.Printf("No variations found for album: %s\n", *targetAlbum)
		return
	}

	// Print summary
	fmt.Printf("=== Album Normalization Report ===\n\n")
	fmt.Printf("Total unique albums (raw): %d\n", countUniqueAlbums(files))
	fmt.Printf("Total unique albums (normalized): %d\n", len(groups))
	fmt.Printf("Albums with %d+ variations: %d\n\n", *minVariations, len(variedGroups))

	if len(variedGroups) == 0 {
		fmt.Println("No album variations found!")
		return
	}

	fmt.Printf("=== Albums with Variations ===\n\n")

	for i, group := range variedGroups {
		printAlbumGroup(group, *showMetadata)

		if i < len(variedGroups)-1 {
			fmt.Println(strings.Repeat("-", 60))
		}
	}

	// Summary with suggestions
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Found %d albums with naming variations\n", len(variedGroups))

	if *dryRun {
		fmt.Println("\nTo fix an album, run with:")
		fmt.Println("  -album=\"<normalized name>\" -fix=\"<target album name>\" -dry-run=false")
		fmt.Println("\nTo also update file metadata:")
		fmt.Println("  -album=\"<normalized name>\" -fix=\"<target album name>\" -dry-run=false -update-files")
		fmt.Println("\nTo restore from a backup:")
		fmt.Println("  -restore=\"<path-to-backup.json>\"")
		fmt.Println("\nExample:")
		fmt.Println("  -album=\"ok computer\" -fix=\"OK Computer\" -dry-run=false")
		fmt.Println("  -album=\"ok computer\" -fix=\"OK Computer\" -dry-run=false -update-files")
		fmt.Println("  -restore=\"album-normalizer-backup.json\"")
	}
}

func loadConfig() (*Config, error) {
	cfg := &Config{}
	err := envconfig.Process("", cfg)
	return cfg, err
}

func connectDB(ctx context.Context, cfg *Config) (*mongo.Client, *mongo.Collection, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.DatabaseURL))
	if err != nil {
		return nil, nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, nil, err
	}

	collection := client.Database(cfg.DatabaseName).Collection(cfg.CollectionName)
	return client, collection, nil
}

func fetchAllMusicFiles(ctx context.Context, collection *mongo.Collection) ([]MusicFile, error) {
	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var files []MusicFile
	if err := cursor.All(ctx, &files); err != nil {
		return nil, err
	}

	return files, nil
}

// normalizeAlbumName normalizes an album name for comparison
func normalizeAlbumName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Normalize unicode characters (like different types of quotes, dashes)
	name = normalizeUnicode(name)

	// Remove extra whitespace
	name = strings.Join(strings.Fields(name), " ")

	// Remove common noise patterns
	name = removeNoisePatterns(name)

	return strings.TrimSpace(name)
}

// normalizeUnicode handles various unicode variations
func normalizeUnicode(s string) string {
	// Replace various types of dashes with standard hyphen
	dashRegex := regexp.MustCompile(`[–—−]`)
	s = dashRegex.ReplaceAllString(s, "-")

	// Replace various types of quotes (smart quotes to regular)
	s = strings.ReplaceAll(s, "'", "'")
	s = strings.ReplaceAll(s, "'", "'")
	s = strings.ReplaceAll(s, "\u201c", "\"") // left double quote
	s = strings.ReplaceAll(s, "\u201d", "\"") // right double quote
	s = strings.ReplaceAll(s, "…", "...")

	// Remove zero-width characters and other invisible unicode
	var result strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			result.WriteRune(r)
		}
	}

	return result.String()
}

// removeNoisePatterns removes common variations that don't change album identity
func removeNoisePatterns(name string) string {
	patterns := []struct {
		regex       *regexp.Regexp
		replacement string
	}{
		// Remove trailing year patterns like "(2003)" or "[2003]" or "- 2003"
		{regexp.MustCompile(`\s*[\(\[]\s*\d{4}\s*[\)\]]\s*$`), ""},
		{regexp.MustCompile(`\s*-\s*\d{4}\s*$`), ""},

		// Remove "remaster", "remastered", "deluxe", etc.
		{regexp.MustCompile(`\s*[\(\[]\s*(remaster(ed)?|deluxe|expanded|anniversary|special)\s*(edition|version)?\s*[\)\]]`), ""},
		{regexp.MustCompile(`\s*-\s*(remaster(ed)?|deluxe|expanded|anniversary|special)\s*(edition|version)?$`), ""},

		// Remove "disc 1", "cd 1", etc.
		{regexp.MustCompile(`\s*[\(\[]\s*(disc|cd|disk)\s*\d+\s*[\)\]]`), ""},

		// Normalize multiple spaces/dashes
		{regexp.MustCompile(`\s+`), " "},
		{regexp.MustCompile(`-+`), "-"},
	}

	for _, p := range patterns {
		name = p.regex.ReplaceAllString(name, p.replacement)
	}

	return strings.TrimSpace(name)
}

// getMusicBrainzAlbumID extracts the MusicBrainz Album ID from metadata
func getMusicBrainzAlbumID(metadata map[string]any) string {
	for _, key := range mbidKeys {
		if val, ok := metadata[key]; ok {
			switch v := val.(type) {
			case string:
				if v != "" {
					return v
				}
			case []interface{}:
				if len(v) > 0 {
					if s, ok := v[0].(string); ok && s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

// groupByNormalizedAlbum groups music files by their normalized album name
// It also tracks MusicBrainz IDs to detect conflicts
func groupByNormalizedAlbum(files []MusicFile) map[string]AlbumGroup {
	groups := make(map[string]AlbumGroup)

	for _, file := range files {
		if file.Album == "" {
			continue
		}

		normalized := normalizeAlbumName(file.Album)
		mbid := getMusicBrainzAlbumID(file.MetaData)

		group, exists := groups[normalized]
		if !exists {
			group = AlbumGroup{
				NormalizedName: normalized,
				MusicBrainzID:  mbid,
				Variations:     make(map[string][]MusicFile),
				MBIDVariations: make(map[string]string),
			}
		}

		// Track MBID for this album variation
		if mbid != "" {
			group.MBIDVariations[file.Album] = mbid

			// Check for MBID conflicts (same normalized name, different MBIDs)
			if group.MusicBrainzID == "" {
				group.MusicBrainzID = mbid
			} else if group.MusicBrainzID != mbid {
				group.HasMBIDConflict = true
			}
		}

		group.Variations[file.Album] = append(group.Variations[file.Album], file)
		groups[normalized] = group
	}

	return groups
}

// groupByMusicBrainzID groups files that share the same MusicBrainz Album ID
// This catches albums with the same MBID but different normalized names
func groupByMusicBrainzID(files []MusicFile) map[string]AlbumGroup {
	groups := make(map[string]AlbumGroup)

	for _, file := range files {
		mbid := getMusicBrainzAlbumID(file.MetaData)
		if mbid == "" {
			continue
		}

		group, exists := groups[mbid]
		if !exists {
			group = AlbumGroup{
				NormalizedName: normalizeAlbumName(file.Album),
				MusicBrainzID:  mbid,
				Variations:     make(map[string][]MusicFile),
				MBIDVariations: make(map[string]string),
			}
		}

		group.Variations[file.Album] = append(group.Variations[file.Album], file)
		group.MBIDVariations[file.Album] = mbid
		groups[mbid] = group
	}

	return groups
}

func countUniqueAlbums(files []MusicFile) int {
	albums := make(map[string]struct{})
	for _, f := range files {
		if f.Album != "" {
			albums[f.Album] = struct{}{}
		}
	}
	return len(albums)
}

func getTotalTracks(group AlbumGroup) int {
	total := 0
	for _, tracks := range group.Variations {
		total += len(tracks)
	}
	return total
}

func printAlbumGroup(group AlbumGroup, showMetadata bool) {
	fmt.Printf("📀 Normalized: %q\n", group.NormalizedName)

	// Show MusicBrainz ID info
	if group.MusicBrainzID != "" {
		if group.HasMBIDConflict {
			fmt.Printf("   ⚠️  MusicBrainz ID CONFLICT - multiple IDs for same album name!\n")
		} else {
			fmt.Printf("   🏷️  MusicBrainz ID: %s\n", group.MusicBrainzID)
		}
	}

	fmt.Printf("   Total tracks: %d, Variations: %d\n", getTotalTracks(group), len(group.Variations))

	// Sort variations by track count (most first)
	type varCount struct {
		name   string
		tracks []MusicFile
	}
	var variations []varCount
	for name, tracks := range group.Variations {
		variations = append(variations, varCount{name, tracks})
	}
	sort.Slice(variations, func(i, j int) bool {
		return len(variations[i].tracks) > len(variations[j].tracks)
	})

	for _, v := range variations {
		mbidInfo := ""
		if mbid, ok := group.MBIDVariations[v.name]; ok && mbid != "" {
			mbidInfo = fmt.Sprintf(" [MBID: %s]", mbid[:8]+"...")
		}
		fmt.Printf("   • %q (%d tracks)%s\n", v.name, len(v.tracks), mbidInfo)

		// Show sample artists for this variation
		artists := make(map[string]int)
		for _, t := range v.tracks {
			artists[t.Artist]++
		}

		// Get top 3 artists
		type artistCount struct {
			name  string
			count int
		}
		var artistList []artistCount
		for name, count := range artists {
			artistList = append(artistList, artistCount{name, count})
		}
		sort.Slice(artistList, func(i, j int) bool {
			return artistList[i].count > artistList[j].count
		})

		if len(artistList) > 3 {
			artistList = artistList[:3]
		}

		artistStrs := make([]string, len(artistList))
		for i, a := range artistList {
			artistStrs[i] = fmt.Sprintf("%s (%d)", a.name, a.count)
		}
		fmt.Printf("     Artists: %s\n", strings.Join(artistStrs, ", "))

		if showMetadata && len(v.tracks) > 0 {
			// Show metadata from first track
			t := v.tracks[0]
			fmt.Printf("     Sample metadata from: %s\n", t.Title)
			if album, ok := t.MetaData["TALB"]; ok {
				fmt.Printf("       TALB (ID3 Album): %v\n", album)
			}
			if album, ok := t.MetaData["album"]; ok {
				fmt.Printf("       album: %v\n", album)
			}
			// Show MusicBrainz keys found
			for _, key := range mbidKeys {
				if val, ok := t.MetaData[key]; ok {
					fmt.Printf("       %s: %v\n", key, val)
				}
			}
		}
	}
	fmt.Println()
}

// showNavidromeGrouping shows how Navidrome groups albums using MBID|album
func showNavidromeGrouping(files []MusicFile) {
	// Group by Navidrome's key: musicbrainz_albumid|album
	navidromeGroups := make(map[string][]MusicFile)

	for _, file := range files {
		if file.Album == "" {
			continue
		}

		mbid := getMusicBrainzAlbumID(file.MetaData)
		key := mbid + "|" + file.Album
		navidromeGroups[key] = append(navidromeGroups[key], file)
	}

	// Now find albums that have the same MBID but different album strings
	// Group by MBID first
	byMBID := make(map[string]map[string][]MusicFile) // mbid -> album_name -> files

	for _, file := range files {
		if file.Album == "" {
			continue
		}

		mbid := getMusicBrainzAlbumID(file.MetaData)
		if mbid == "" {
			continue
		}

		if byMBID[mbid] == nil {
			byMBID[mbid] = make(map[string][]MusicFile)
		}
		byMBID[mbid][file.Album] = append(byMBID[mbid][file.Album], file)
	}

	// Find MBIDs with multiple album name variations
	type mbidConflict struct {
		mbid       string
		variations map[string][]MusicFile
	}
	var conflicts []mbidConflict

	for mbid, albums := range byMBID {
		if len(albums) > 1 {
			conflicts = append(conflicts, mbidConflict{mbid, albums})
		}
	}

	// Sort by number of variations
	sort.Slice(conflicts, func(i, j int) bool {
		return len(conflicts[i].variations) > len(conflicts[j].variations)
	})

	fmt.Printf("=== Navidrome Album Grouping Analysis ===\n\n")
	fmt.Printf("Total Navidrome album groups (MBID|album): %d\n", len(navidromeGroups))
	fmt.Printf("Albums with MusicBrainz ID: %d\n", len(byMBID))
	fmt.Printf("Albums with SAME MBID but DIFFERENT names: %d\n\n", len(conflicts))

	if len(conflicts) == 0 {
		fmt.Println("✅ All albums with MusicBrainz IDs have consistent naming!")
		return
	}

	fmt.Printf("=== Albums with Same MBID but Different Names ===\n")
	fmt.Println("(These would be grouped together by Navidrome)\n")

	for _, c := range conflicts {
		totalTracks := 0
		for _, tracks := range c.variations {
			totalTracks += len(tracks)
		}

		fmt.Printf("🏷️  MBID: %s\n", c.mbid)
		fmt.Printf("   Total tracks: %d, Name variations: %d\n", totalTracks, len(c.variations))

		// Sort by track count
		type varInfo struct {
			name   string
			tracks []MusicFile
		}
		var vars []varInfo
		for name, tracks := range c.variations {
			vars = append(vars, varInfo{name, tracks})
		}
		sort.Slice(vars, func(i, j int) bool {
			return len(vars[i].tracks) > len(vars[j].tracks)
		})

		for _, v := range vars {
			fmt.Printf("   • %q (%d tracks)\n", v.name, len(v.tracks))
		}
		fmt.Println()
	}

	// Also show albums WITHOUT MBID that might be duplicates
	fmt.Printf("=== Albums Without MusicBrainz ID ===\n")
	fmt.Println("(These might be duplicates that Navidrome can't auto-detect)\n")

	noMBID := make(map[string][]MusicFile)
	for _, file := range files {
		if file.Album == "" {
			continue
		}
		mbid := getMusicBrainzAlbumID(file.MetaData)
		if mbid == "" {
			noMBID[file.Album] = append(noMBID[file.Album], file)
		}
	}

	// Group by normalized name
	byNormalized := make(map[string]map[string]int) // normalized -> album -> count
	for album, tracks := range noMBID {
		norm := normalizeAlbumName(album)
		if byNormalized[norm] == nil {
			byNormalized[norm] = make(map[string]int)
		}
		byNormalized[norm][album] = len(tracks)
	}

	// Show duplicates without MBID
	var noMBIDDups []struct {
		normalized string
		variations map[string]int
	}
	for norm, vars := range byNormalized {
		if len(vars) > 1 {
			noMBIDDups = append(noMBIDDups, struct {
				normalized string
				variations map[string]int
			}{norm, vars})
		}
	}

	sort.Slice(noMBIDDups, func(i, j int) bool {
		return len(noMBIDDups[i].variations) > len(noMBIDDups[j].variations)
	})

	fmt.Printf("Found %d albums without MBID that have naming variations:\n\n", len(noMBIDDups))

	for _, dup := range noMBIDDups {
		fmt.Printf("📀 Normalized: %q\n", dup.normalized)
		for name, count := range dup.variations {
			fmt.Printf("   • %q (%d tracks, NO MBID)\n", name, count)
		}
		fmt.Println()
	}
}

func listAllAlbums(files []MusicFile) {
	albums := make(map[string]int)
	for _, f := range files {
		if f.Album != "" {
			albums[f.Album]++
		}
	}

	type albumCount struct {
		name  string
		count int
	}
	var list []albumCount
	for name, count := range albums {
		list = append(list, albumCount{name, count})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].count > list[j].count
	})

	fmt.Printf("=== All Albums (%d unique) ===\n\n", len(list))
	for _, a := range list {
		fmt.Printf("%4d tracks: %s\n", a.count, a.name)
	}
}

func fixAlbumName(ctx context.Context, collection *mongo.Collection, group AlbumGroup, targetName string, updateFiles bool, backupFilePath string) error {
	// Get all tracks that need to be updated
	var tracksToUpdate []MusicFile
	for variation, tracks := range group.Variations {
		if variation == targetName {
			continue // Skip tracks that already have the target name
		}
		tracksToUpdate = append(tracksToUpdate, tracks...)
	}

	if len(tracksToUpdate) == 0 {
		fmt.Println("No tracks need updating")
		return nil
	}

	fmt.Printf("Updating %d tracks to use album name: %q\n", len(tracksToUpdate), targetName)

	// Load existing backup log or create new one
	backupLog, err := loadBackupLog(backupFilePath)
	if err != nil {
		return fmt.Errorf("failed to load backup log: %w", err)
	}

	// Update database
	var idsToUpdate []string
	for _, t := range tracksToUpdate {
		idsToUpdate = append(idsToUpdate, t.ID)
	}

	result, err := collection.UpdateMany(
		ctx,
		bson.M{"_id": bson.M{"$in": idsToUpdate}},
		bson.M{"$set": bson.M{
			"album":      targetName,
			"updated_at": time.Now().Unix(),
		}},
	)
	if err != nil {
		return fmt.Errorf("failed to update database: %w", err)
	}

	fmt.Printf("Modified %d database documents\n", result.ModifiedCount)

	// Optionally update file metadata
	if updateFiles {
		fmt.Println("\nUpdating file metadata...")
		successCount := 0
		failCount := 0
		timestamp := time.Now().Unix()

		for _, track := range tracksToUpdate {
			if track.Path == "" {
				fmt.Printf("  ⚠️  Skipping track %s: no file path\n", track.ID)
				failCount++
				continue
			}

			// Read current album name from file before updating (for backup)
			originalAlbum, err := readAlbumFromFile(track.Path)
			if err != nil {
				fmt.Printf("  ⚠️  Could not read original album from %s: %v\n", track.Path, err)
				// Use database value as fallback
				originalAlbum = track.Album
			}

			// Add to backup log before updating
			backupLog.Entries = append(backupLog.Entries, BackupEntry{
				FilePath:      track.Path,
				OriginalAlbum: originalAlbum,
				NewAlbum:      targetName,
				Timestamp:     timestamp,
				TrackID:       track.ID,
			})

			err = updateFileMetadata(track.Path, targetName)
			if err != nil {
				fmt.Printf("  ❌ Failed to update %s: %v\n", track.Path, err)
				failCount++
				// Remove from backup log if update failed
				backupLog.Entries = backupLog.Entries[:len(backupLog.Entries)-1]
			} else {
				fmt.Printf("  ✓ Updated %s\n", filepath.Base(track.Path))
				successCount++
			}
		}

		// Save backup log
		if err := saveBackupLog(backupFilePath, backupLog); err != nil {
			fmt.Printf("  ⚠️  Warning: Failed to save backup log: %v\n", err)
		} else {
			fmt.Printf("\nBackup saved to: %s\n", backupFilePath)
		}

		fmt.Printf("\nFile metadata update summary: %d succeeded, %d failed\n", successCount, failCount)
	}

	return nil
}

// updateFileMetadata updates the album tag in the music file based on file extension
func updateFileMetadata(filePath string, albumName string) error {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".mp3":
		return updateMP3Metadata(filePath, albumName)
	case ".flac":
		return updateFLACMetadata(filePath, albumName)
	case ".m4a", ".mp4":
		return updateM4AMetadata(filePath, albumName)
	default:
		return fmt.Errorf("unsupported file format: %s (supported: .mp3, .flac, .m4a, .mp4)", ext)
	}
}

// updateMP3Metadata updates the album tag in an MP3 file using ID3v2
func updateMP3Metadata(filePath string, albumName string) error {
	tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("failed to open MP3 file: %w", err)
	}
	defer tag.Close()

	tag.SetAlbum(albumName)

	if err := tag.Save(); err != nil {
		return fmt.Errorf("failed to save MP3 tags: %w", err)
	}

	return nil
}

// updateFLACMetadata updates the album tag in a FLAC file
func updateFLACMetadata(filePath string, albumName string) error {
	f, err := flac.ParseFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to parse FLAC file: %w", err)
	}

	// Find existing vorbis comment block or create new one
	var cmts *flacvorbis.MetaDataBlockVorbisComment
	var cmtIdx int = -1

	for idx, meta := range f.Meta {
		if meta.Type == flac.VorbisComment {
			cmts, err = flacvorbis.ParseFromMetaDataBlock(*meta)
			if err != nil {
				return fmt.Errorf("failed to parse vorbis comment: %w", err)
			}
			cmtIdx = idx
			break
		}
	}

	// Create new vorbis comment block if none exists
	if cmts == nil {
		cmts = flacvorbis.New()
	}

	// Add/update album tag (Add method replaces existing values)
	cmts.Add(flacvorbis.FIELD_ALBUM, albumName)

	// Marshal the updated comment block
	cmtsMeta := cmts.Marshal()

	// Update or add the metadata block
	if cmtIdx >= 0 {
		f.Meta[cmtIdx] = &cmtsMeta
	} else {
		f.Meta = append(f.Meta, &cmtsMeta)
	}

	// Save the file
	if err := f.Save(filePath); err != nil {
		return fmt.Errorf("failed to save FLAC file: %w", err)
	}

	return nil
}

// updateM4AMetadata updates the album tag in an M4A/MP4 file
func updateM4AMetadata(filePath string, albumName string) error {
	mp4, err := mp4tag.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open M4A file: %w", err)
	}
	defer mp4.Close()

	tags := &mp4tag.MP4Tags{
		Album: albumName,
	}

	// Write the album tag (empty slice means don't delete any tags)
	if err := mp4.Write(tags, []string{}); err != nil {
		return fmt.Errorf("failed to write M4A tags: %w", err)
	}

	return nil
}

// readAlbumFromFile reads the current album name from a music file
func readAlbumFromFile(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".mp3":
		return readMP3Album(filePath)
	case ".flac":
		return readFLACAlbum(filePath)
	case ".m4a", ".mp4":
		return readM4AAlbum(filePath)
	default:
		return "", fmt.Errorf("unsupported file format: %s", ext)
	}
}

// readMP3Album reads album name from MP3 file
func readMP3Album(filePath string) (string, error) {
	tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return "", err
	}
	defer tag.Close()
	return tag.Album(), nil
}

// readFLACAlbum reads album name from FLAC file
func readFLACAlbum(filePath string) (string, error) {
	f, err := flac.ParseFile(filePath)
	if err != nil {
		return "", err
	}

	for _, meta := range f.Meta {
		if meta.Type == flac.VorbisComment {
			cmts, err := flacvorbis.ParseFromMetaDataBlock(*meta)
			if err != nil {
				continue
			}
			album, err := cmts.Get(flacvorbis.FIELD_ALBUM)
			if err == nil && len(album) > 0 {
				return album[0], nil
			}
		}
	}
	return "", fmt.Errorf("no album tag found")
}

// readM4AAlbum reads album name from M4A/MP4 file
func readM4AAlbum(filePath string) (string, error) {
	mp4, err := mp4tag.Open(filePath)
	if err != nil {
		return "", err
	}
	defer mp4.Close()

	tags, err := mp4.Read()
	if err != nil {
		return "", err
	}
	return tags.Album, nil
}

// loadBackupLog loads the backup log from a JSON file
func loadBackupLog(filePath string) (*BackupLog, error) {
	log := &BackupLog{Entries: []BackupEntry{}}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, return empty log
			return log, nil
		}
		return nil, err
	}

	if len(data) == 0 {
		return log, nil
	}

	if err := json.Unmarshal(data, log); err != nil {
		return nil, fmt.Errorf("failed to parse backup log: %w", err)
	}

	return log, nil
}

// saveBackupLog saves the backup log to a JSON file
func saveBackupLog(filePath string, log *BackupLog) error {
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal backup log: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write backup log: %w", err)
	}

	return nil
}

// restoreFromBackup restores album metadata from a backup file
func restoreFromBackup(backupFilePath string, ctx context.Context, collection *mongo.Collection) error {
	fmt.Printf("Loading backup from: %s\n", backupFilePath)

	backupLog, err := loadBackupLog(backupFilePath)
	if err != nil {
		return fmt.Errorf("failed to load backup: %w", err)
	}

	if len(backupLog.Entries) == 0 {
		fmt.Println("Backup file is empty, nothing to restore")
		return nil
	}

	fmt.Printf("Found %d entries in backup\n\n", len(backupLog.Entries))

	// Group by original album name for database updates
	dbUpdates := make(map[string][]string) // original album -> track IDs
	fileRestores := []BackupEntry{}

	for _, entry := range backupLog.Entries {
		// Check if file still exists
		if _, err := os.Stat(entry.FilePath); os.IsNotExist(err) {
			fmt.Printf("  ⚠️  File not found, skipping: %s\n", entry.FilePath)
			continue
		}

		// Group database updates
		if dbUpdates[entry.OriginalAlbum] == nil {
			dbUpdates[entry.OriginalAlbum] = []string{}
		}
		dbUpdates[entry.OriginalAlbum] = append(dbUpdates[entry.OriginalAlbum], entry.TrackID)

		// Collect file restores
		fileRestores = append(fileRestores, entry)
	}

	// Restore database entries
	fmt.Println("Restoring database entries...")
	totalDBRestored := 0
	for originalAlbum, trackIDs := range dbUpdates {
		result, err := collection.UpdateMany(
			ctx,
			bson.M{"_id": bson.M{"$in": trackIDs}},
			bson.M{"$set": bson.M{
				"album":      originalAlbum,
				"updated_at": time.Now().Unix(),
			}},
		)
		if err != nil {
			fmt.Printf("  ❌ Failed to restore database entries for album %q: %v\n", originalAlbum, err)
			continue
		}
		fmt.Printf("  ✓ Restored %d database entries to album: %q\n", result.ModifiedCount, originalAlbum)
		totalDBRestored += int(result.ModifiedCount)
	}

	// Restore file metadata
	fmt.Println("\nRestoring file metadata...")
	successCount := 0
	failCount := 0

	for _, entry := range fileRestores {
		err := updateFileMetadata(entry.FilePath, entry.OriginalAlbum)
		if err != nil {
			fmt.Printf("  ❌ Failed to restore %s: %v\n", entry.FilePath, err)
			failCount++
		} else {
			fmt.Printf("  ✓ Restored %s to: %q\n", filepath.Base(entry.FilePath), entry.OriginalAlbum)
			successCount++
		}
	}

	fmt.Printf("\n=== Restore Summary ===\n")
	fmt.Printf("Database entries restored: %d\n", totalDBRestored)
	fmt.Printf("File metadata restored: %d succeeded, %d failed\n", successCount, failCount)

	return nil
}
