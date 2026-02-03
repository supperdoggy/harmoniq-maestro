package utils

import (
	"os"
	"path/filepath"
	"strings"
)

type PlaylistTrack struct {
	Name          string   `json:"name"`
	Artists       []string `json:"artists"`
	Artist        string   `json:"artist"`
	Genres        []string `json:"genres"`
	DiscNumber    int      `json:"disc_number"`
	DiscCount     int      `json:"disc_count"`
	AlbumName     string   `json:"album_name"`
	AlbumArtist   string   `json:"album_artist"`
	Duration      int      `json:"duration"`
	Year          string   `json:"year"`
	Date          string   `json:"date"`
	TrackNumber   int      `json:"track_number"`
	TracksCount   int      `json:"tracks_count"`
	SongID        string   `json:"song_id"`
	Explicit      bool     `json:"explicit"`
	Publisher     string   `json:"publisher"`
	URL           string   `json:"url"`
	ISRC          string   `json:"isrc"`
	CoverURL      string   `json:"cover_url"`
	CopyrightText string   `json:"copyright_text"`
	DownloadURL   *string  `json:"download_url"` // Nullable field
	Lyrics        string   `json:"lyrics"`
	Popularity    int      `json:"popularity"`
	AlbumID       string   `json:"album_id"`
	ListName      string   `json:"list_name"`
	ListURL       string   `json:"list_url"`
	ListPosition  int      `json:"list_position"`
	ListLength    int      `json:"list_length"`
	ArtistID      string   `json:"artist_id"`
	AlbumType     string   `json:"album_type"`
}

func FindUnindexedSongs(songList []string, musicRoot, outputPath string) ([]string, error) {
	var matchedPaths []string
	matchedSongs := make(map[string]bool)

	for _, song := range songList {
		parts := strings.SplitN(song, " - ", 2)
		if len(parts) != 2 {
			continue
		}

		songLower := strings.ToLower(song)

		var matchedPath string

		_ = filepath.Walk(musicRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasSuffix(strings.ToLower(path), ".lrc") {
				return nil
			}

			if _, ok := matchedSongs[song]; ok {
				return filepath.SkipDir // skip remaining walk if already matched
			}

			lowerPath := strings.ToLower(path)

			if strings.Contains(lowerPath, songLower+".") { // || (strings.Contains(lowerPath, artist) && strings.Contains(lowerPath, title))

				relPath, _ := filepath.Rel(filepath.Dir(outputPath), path)
				relPath = strings.Replace(relPath, "../../", "", 1)
				relPath = strings.ReplaceAll(relPath, "..", "Job-downloaded")
				relPath = "/music/" + relPath

				matchedPath = relPath
				matchedSongs[song] = true
				return filepath.SkipDir // stop walking further for this song
			}

			return nil
		})

		if matchedPath != "" {
			matchedPaths = append(matchedPaths, matchedPath)
		}
	}

	return matchedPaths, nil
}

// CreateM3UPlaylist generates an .m3u playlist from a list of "Artist - Song" strings
func CreateM3UPlaylist(matchedPaths []string, musicRoot, outputPath string) error {

	// check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		return os.ErrExist
	}

	// Write the M3U file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, path := range matchedPaths {
		_, err := f.WriteString(path + "\n")
		if err != nil {
			return err
		}
	}

	return nil
}
