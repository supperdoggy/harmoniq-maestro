package spotify

type SpotifyObjectType string

const (
	SpotifyObjectTypePlaylist SpotifyObjectType = "playlist"
	SpotifyObjectTypeAlbum    SpotifyObjectType = "album"
	SpotifyObjectTypeTrack    SpotifyObjectType = "track"
	SpotifyObjectTypeArtist   SpotifyObjectType = "artist"
)
