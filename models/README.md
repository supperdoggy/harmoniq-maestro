# models

Shared models and MongoDB helpers used by core Harmoniq Maestro services.

Module path:

```text
github.com/supperdoggy/spot-models
```

## Included Types

- `DownloadQueueRequest`
- `PlaylistRequest`
- `MusicFile`
- `DynamicPlaylist`
- `SubscribedPlaylist`
- `GenreMapping`

## Development

Run tests:

```bash
go test ./...
```

This module is consumed by:

- `album-queue`
- `spotdl-wapper`
- `dynamic-playlists`
