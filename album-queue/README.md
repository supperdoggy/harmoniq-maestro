# album-queue

Telegram bot service that accepts Spotify links and queues download requests in MongoDB.

## Features

- Accepts Spotify playlist/album/track URLs
- Queue inspection and management commands
- Whitelist-based access control
- Health and readiness endpoints (`/health`, `/ready`)

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | yes | MongoDB connection string |
| `DATABASE_NAME` | yes | MongoDB database name |
| `BOT_TOKEN` | yes | Telegram bot token |
| `BOT_WHITELIST` | yes | Comma-separated Telegram user IDs |
| `WEBHOOK_URL` | yes | URL pinged when a new item is queued |
| `SPOTIFY_CLIENT_ID` | yes | Spotify client ID |
| `SPOTIFY_CLIENT_SECRET` | yes | Spotify client secret |

## Build

```bash
go build -o album-queue .
```

## Run

```bash
DATABASE_URL="mongodb://localhost:27017" \
DATABASE_NAME="music-services" \
BOT_TOKEN="..." \
BOT_WHITELIST="123456789" \
WEBHOOK_URL="http://localhost:8080/health" \
SPOTIFY_CLIENT_ID="..." \
SPOTIFY_CLIENT_SECRET="..." \
./album-queue
```

## Bot Commands

- `/start`
- `/queue`
- `/failed`
- `/redownload <track_url>`
- `/deactivate <id>`
- `/p <url>`
- `/pnp <url>`
- `/subscribe <url>`
- `/unsubscribe <url>`
- `/subscriptions`

## Health Endpoints

- `GET /health`
- `GET /ready`
- `GET /stats`
