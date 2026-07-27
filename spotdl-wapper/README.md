# spotdl-wapper

Worker service that reads queued Spotify requests from MongoDB and downloads
content via `spotdl`.

For an architecture, lifecycle, data-contract, and operational description of
the component as it exists today, see
[the current-state document](../docs/spotdl-wapper-current-state.md).
For replacement options and a staged target architecture, see
[the migration proposal](../docs/spotdl-wapper-migration.md).

## Features

- Queue-based request processing
- Playlist/track handling
- Retry flow and sleep-based throttling
- Optional Loki log shipping
- M3U generation support

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | yes | MongoDB connection string |
| `DATABASE_NAME` | yes | MongoDB database name |
| `DESTINATION` | yes | Download destination template/path |
| `MUSIC_LIBRARY_PATH` | yes | Root music library path |
| `SLEEP_IN_MINUTES` | yes | Delay between requests |
| `SPOTIFY_CLIENT_ID` | yes | Spotify client ID |
| `SPOTIFY_CLIENT_SECRET` | yes | Spotify client secret |
| `LOKI_ENABLED` | no | Enable Loki shipping (`true/false`) |
| `LOKI_URL` | no | Loki push URL |

## Build

```bash
go build -o spotdl-wapper .
```

## Run

```bash
DATABASE_URL="mongodb://localhost:27017" \
DATABASE_NAME="music-services" \
DESTINATION="/music/downloads/{artists} - {title}.{output-ext}" \
MUSIC_LIBRARY_PATH="/music" \
SLEEP_IN_MINUTES=1 \
SPOTIFY_CLIENT_ID="..." \
SPOTIFY_CLIENT_SECRET="..." \
./spotdl-wapper
```

## Runtime Dependencies

- `spotdl`
- `ffmpeg`
- `yt-dlp`
- `nodejs`
- optional: `yt-dlp-ejs`
