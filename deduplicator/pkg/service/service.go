package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/deduplicator/pkg/config"
	"github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/deduplicator/pkg/utils"
	"github.com/supperdoggy/spot-models/database"
	"go.uber.org/zap"
)

func RunApp(log *zap.Logger, cfg *config.Config, db database.Database) (int, error) {

	duplicates := trackDuplicates(log, cfg.DestinationFolder)

	// get all playlist items
	usedFiles := make(map[string]struct{})

	// get all files in playlist folder
	files, err := os.ReadDir(cfg.PlaylistsRoot)
	if err != nil {
		log.Fatal("failed to read playlist folder", zap.Error(err))
	}

	// read all m3u in playlist folder
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if filepath.Ext(file.Name()) != ".m3u" {
			continue
		}

		playlistFile, err := os.Open(cfg.PlaylistsRoot + "/" + file.Name())
		if err != nil {
			log.Fatal("failed to open playlist file", zap.Error(err))
		}

		// read all lines in playlist file
		lines, err := utils.ReadLines(playlistFile)
		if err != nil {
			log.Fatal("failed to read playlist file", zap.Error(err))
		}
		playlistFile.Close()
		for _, line := range lines {
			usedFiles[line] = struct{}{}
		}
	}

	// output used files

	notSafeToDelete := make(map[string]struct{})
	for playlistSongPath := range usedFiles {
		shortPath := strings.Split(playlistSongPath, "/")[len(strings.Split(playlistSongPath, "/"))-1]
		usedSongName := strings.Split(shortPath, ".")[0]

		if _, ok := duplicates[usedSongName]; ok {

			for _, duplicatePath := range duplicates[usedSongName] {
				if strings.Contains(duplicatePath, playlistSongPath[6:]) {
					notSafeToDelete[duplicatePath] = struct{}{}
					log.Info("found duplicate", zap.String("playlistUsedPath", playlistSongPath), zap.Any("duplicatePath", duplicatePath))
				}
			}
		}
	}

	safeToDelete := make(map[string]struct{})
	for duplicateKey, duplicatePaths := range duplicates {
		for _, duplicatePath := range duplicatePaths {
			if _, ok := notSafeToDelete[duplicatePath]; !ok {
				if _, ok := safeToDelete[duplicates[duplicateKey][0]]; !ok {
					safeToDelete[duplicatePath] = struct{}{}
				}
			}
		}
	}

	// but I need to be sure not to delete all duplicate paths, I need to keep one

	log.Info("found not safe to delete files",
		zap.Int("count", len(notSafeToDelete)),
	)

	log.Info("found safe to delete files",
		zap.Int("count", len(safeToDelete)),
	)

	// move all safe to delete files to another folder
	for safe := range safeToDelete {
		log.Info("moving file", zap.String("path", safe))
		err := os.Rename(safe, cfg.DuplicatesFolder+filepath.Base(safe))
		if err != nil {
			log.Error("failed to move file", zap.Error(err))
			continue
		}
	}

	return len(duplicates), nil
}

// migratePlaylist copies files from playlist folder to destination folder
func migratePlaylist(log *zap.Logger, playlistFolder *os.File, playlistPath, destination string) error {
	files, err := playlistFolder.Readdir(0)
	if err != nil {
		return err
	}

	for _, file := range files {
		// check if not in duplicates
		if _, ok := duplicates.m[file.Name()]; ok {
			log.Info("found duplicate", zap.String("file", file.Name()))
			continue
		}

		// copy file to destination folder
		log.Info("copying file", zap.String("file", file.Name()))

		err := utils.CopyFileToFolder(playlistPath+"/"+file.Name(), destination)
		if err != nil {
			log.Error("failed to copy file", zap.Error(err))
			continue
		}

		duplicates.m[file.Name()] = struct{}{}
	}
	return nil
}

// trackDuplicates identifies files that appear in multiple playlists, ignoring file extensions
func trackDuplicates(log *zap.Logger, playlistsRoot string) map[string][]string {
	// Map to store all paths for each base filename (without extension)
	filePathsMap := make(map[string][]string)

	// Loop through all playlists
	filepath.Walk(playlistsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip directories
		if !info.IsDir() {
			// Get full filename
			fullFilename := filepath.Base(path)

			// Remove extension to get the base filename
			extension := filepath.Ext(fullFilename)
			if extension == ".lrc" {
				return nil
			}
			baseFilename := fullFilename[:len(fullFilename)-len(extension)]

			// Add this path to the list of paths for this base filename
			filePathsMap[baseFilename] = append(filePathsMap[baseFilename], path)
		}
		return nil
	})

	// Filter to only files that appear more than once
	duplicatesWithPaths := make(map[string][]string)
	for baseFilename, paths := range filePathsMap {
		if len(paths) > 1 {
			duplicatesWithPaths[baseFilename] = paths
			// log.Info("found duplicate file",
			// 	zap.String("base_name", baseFilename),
			// 	zap.Int("occurrences", len(paths)),
			// 	zap.Strings("paths", paths))
		}
	}

	return duplicatesWithPaths
}
