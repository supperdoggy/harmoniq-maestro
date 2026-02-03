# album-queue

[![CI](https://github.com/supperdoggy/album-queue/actions/workflows/ci.yml/badge.svg)](https://github.com/supperdoggy/album-queue/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/supperdoggy/album-queue)](https://goreportcard.com/report/github.com/supperdoggy/album-queue)

A Telegram bot that collects Spotify playlist, album, and song links and queues them for download.

## Features

- 🎵 Accepts Spotify links for playlists, albums, or songs
- ✅ Automatically validates Spotify URLs
- 📋 Queue management with `/queue` command
- 🔒 Whitelist-based access control
- 🔔 Webhook notifications when new items are queued
- ❤️ Health check endpoint for monitoring

## Prerequisites

- Go 1.23+
- MongoDB
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✅ | MongoDB connection string |
| `DATABASE_NAME` | ✅ | MongoDB database name |
| `BOT_TOKEN` | ✅ | Telegram bot token |
| `BOT_WHITELIST` | ✅ | Comma-separated list of allowed Telegram user IDs |
| `WEBHOOK_URL` | ✅ | URL to call when new items are queued |

## Installation

```bash
# Clone the repository
git clone https://github.com/supperdoggy/album-queue.git
cd album-queue

# Install dependencies
go mod download

# Build
go build -o album-queue .

# Run
./album-queue
```

## Docker

```bash
# Build image
docker build -t album-queue .

# Run
docker run -d \
  -e DATABASE_URL="mongodb://..." \
  -e DATABASE_NAME="music-services" \
  -e BOT_TOKEN="your-bot-token" \
  -e BOT_WHITELIST="123456789,987654321" \
  -e WEBHOOK_URL="http://spotdl-wapper:8080/trigger" \
  -p 8080:8080 \
  album-queue
```

## Bot Commands

| Command | Description |
|---------|-------------|
| `/start` | Welcome message |
| `/queue` | Show active download requests |
| `/deactivate <id>` | Deactivate a specific request |
| `/p <url>` | Add a playlist to the queue |
| `/pnp <url>` | Add a playlist without pulling missing songs |

Simply send any Spotify URL to add it to the download queue.

## Health Endpoints

- `GET /health` - Returns `OK` if the service is running
- `GET /ready` - Returns `Ready` if the service is ready to accept requests

## Related Projects

- [spot-models](https://github.com/supperdoggy/spot-models) - Shared data models
- [spotdl-wapper](https://github.com/supperdoggy/spotdl-wapper) - Music download processor

## License

MIT
