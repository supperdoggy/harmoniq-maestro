# spotdl-wapper

[![CI](https://github.com/supperdoggy/spotdl-wapper/actions/workflows/ci.yml/badge.svg)](https://github.com/supperdoggy/spotdl-wapper/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/supperdoggy/spotdl-wapper)](https://goreportcard.com/report/github.com/supperdoggy/spotdl-wapper)

A Go wrapper service for [spotdl](https://github.com/spotDL/spotify-downloader) that processes download requests from a MongoDB queue.

## Features

- 🎵 Processes Spotify download requests from MongoDB queue
- 📁 Downloads music to configurable destination
- 🔄 Automatic retry with configurable sleep intervals
- 📋 M3U playlist generation support
- 🎯 Sync-without-deleting mode for playlists

## Prerequisites

- Go 1.23+
- MongoDB
- [spotdl](https://github.com/spotDL/spotify-downloader) installed and configured
- Node.js (for yt-dlp JS challenge solving)
- [yt-dlp-ejs](https://github.com/AJIeKceuD/yt-dlp-ejs) package

## spotdl Configuration

Create `~/.spotdl/config.json`:

```json
{
    "client_id": "your-spotify-client-id",
    "client_secret": "your-spotify-client-secret",
    "audio_providers": ["youtube-music"],
    "lyrics_providers": ["genius", "musixmatch"],
    "output": "/path/to/music/{artists} - {title}.{output-ext}",
    "format": "flac",
    "bitrate": "320k",
    "threads": 1,
    "cookie_file": "~/.spotdl/cookies.txt",
    "yt_dlp_args": "--js-runtimes node --sleep-interval 2 --max-sleep-interval 5"
}
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✅ | MongoDB connection string |
| `DATABASE_NAME` | ✅ | MongoDB database name |
| `DESTINATION` | ✅ | Download destination path |
| `MUSIC_LIBRARY_PATH` | ✅ | Root path of music library |
| `SLEEP_IN_MINUTES` | ✅ | Sleep time between downloads (rate limiting) |
| `SPOTIFY_CLIENT_ID` | ✅ | Spotify API client ID |
| `SPOTIFY_CLIENT_SECRET` | ✅ | Spotify API client secret |

## Installation

```bash
# Clone the repository
git clone https://github.com/supperdoggy/spotdl-wapper.git
cd spotdl-wapper

# Install dependencies
go mod download

# Build
go build -o spotdl-wapper .
```

## Usage

```bash
# Run directly
DATABASE_URL="mongodb://..." \
DATABASE_NAME="music-services" \
DESTINATION="/mnt/music/downloads" \
MUSIC_LIBRARY_PATH="/mnt/music/" \
SLEEP_IN_MINUTES=1 \
SPOTIFY_CLIENT_ID="your-id" \
SPOTIFY_CLIENT_SECRET="your-secret" \
./spotdl-wapper
```

## Docker

```bash
# Build image
docker build -t spotdl-wapper .

# Run
docker run -d \
  -v /mnt/music:/mnt/music \
  -v ~/.spotdl:/root/.spotdl \
  -e DATABASE_URL="mongodb://..." \
  -e DATABASE_NAME="music-services" \
  -e DESTINATION="/mnt/music/downloads" \
  -e MUSIC_LIBRARY_PATH="/mnt/music/" \
  -e SLEEP_IN_MINUTES=1 \
  -e SPOTIFY_CLIENT_ID="your-id" \
  -e SPOTIFY_CLIENT_SECRET="your-secret" \
  spotdl-wapper
```

## How It Works

1. Fetches active download requests from MongoDB
2. Sorts by priority (non-errored first, then by creation date)
3. Executes `spotdl download` for each request
4. Updates request status in database
5. Sleeps between downloads to avoid rate limiting

## Related Projects

- [spot-models](https://github.com/supperdoggy/spot-models) - Shared data models
- [album-queue](https://github.com/supperdoggy/album-queue) - Telegram bot for queueing

## Troubleshooting

### HTTP Error 403: Forbidden

Update yt-dlp to the latest nightly and ensure you have:
- Node.js installed
- yt-dlp-ejs package: `pip install yt-dlp-ejs`
- `--js-runtimes node` in your spotdl config

### Signature solving failed

Make sure you have a JavaScript runtime (Node.js 20+) and yt-dlp-ejs installed.

## License

MIT
