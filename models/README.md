# spot-models

[![CI](https://github.com/supperdoggy/spot-models/actions/workflows/ci.yml/badge.svg)](https://github.com/supperdoggy/spot-models/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/supperdoggy/spot-models.svg)](https://pkg.go.dev/github.com/supperdoggy/spot-models)

Shared data models for the SmartHomeServer music services ecosystem.

## Installation

```bash
go get github.com/supperdoggy/spot-models
```

## Models

### DownloadQueueRequest

Represents a request to download music from Spotify.

```go
type DownloadQueueRequest struct {
    ID         string `json:"id" bson:"_id"`
    CreatorID  int64  `json:"creator_id" bson:"creator_id"`
    SpotifyURL string `json:"spotify_url" bson:"spotify_url"`
    Name       string `json:"name" bson:"name"`
    Active     bool   `json:"active" bson:"active"`
    Errored    bool   `json:"errored" bson:"errored"`
    CreatedAt  int64  `json:"created_at" bson:"created_at"`
    UpdatedAt  int64  `json:"updated_at" bson:"updated_at"`
    SyncCount  int    `json:"sync_count" bson:"sync_count"`
    RetryCount int    `json:"retry_count" bson:"retry_count"`
}
```

### PlaylistRequest

Represents a request to process a Spotify playlist.

```go
type PlaylistRequest struct {
    ID         string `json:"id" bson:"_id"`
    CreatorID  int64  `json:"creator_id" bson:"creator_id"`
    SpotifyURL string `json:"spotify_url" bson:"spotify_url"`
    Active     bool   `json:"active" bson:"active"`
    Errored    bool   `json:"errored" bson:"errored"`
    NoPull     bool   `json:"no_pull" bson:"no_pull"`
    CreatedAt  int64  `json:"created_at" bson:"created_at"`
    UpdatedAt  int64  `json:"updated_at" bson:"updated_at"`
}
```

### MusicFile

Represents a music file stored in the library.

```go
type MusicFile struct {
    ID        string         `json:"id" bson:"_id"`
    Artist    string         `json:"artist" bson:"artist"`
    Album     string         `json:"album" bson:"album"`
    Title     string         `json:"title" bson:"title"`
    Genre     string         `json:"genre" bson:"genre"`
    Path      string         `json:"path" bson:"path"`
    MetaData  map[string]any `json:"meta_data" bson:"meta_data"`
    CreatedAt int64          `json:"created_at" bson:"created_at"`
    UpdatedAt int64          `json:"updated_at" bson:"updated_at"`
}
```

## Related Projects

- [album-queue](https://github.com/supperdoggy/album-queue) - Telegram bot for queueing Spotify downloads
- [spotdl-wapper](https://github.com/supperdoggy/spotdl-wapper) - Go wrapper for spotdl

## License

MIT
