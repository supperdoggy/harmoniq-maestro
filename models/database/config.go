package database

type DataBaseConfig struct {
	DatabaseURL  string `envconfig:"DATABASE_URL" required:"true"`
	DatabaseName string `envconfig:"DATABASE_NAME" required:"true"`

	MusicFilesCollectionName      string `envconfig:"MUSIC_FILES_COLLECTION_NAME" required:"true"`
	DownloadRequestCollectionName string `envconfig:"DOWNLOAD_REQUEST_COLLECTION_NAME" required:"true"`
	PlaylistRequestCollectionName string `envconfig:"PLAYLIST_REQUEST_COLLECTION_NAME" required:"true"`
	IndexStatusCollectionName     string `envconfig:"INDEX_STATUS_COLLECTION_NAME" required:"true"`
}
