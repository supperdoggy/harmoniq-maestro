# album-queue

Telegram bot service that accepts Spotify links and queues download requests in MongoDB.

## Features

- Accepts Spotify playlist/album/track URLs
- Queue inspection and management commands
- Ordered multipart `/queue` replies for large active queues
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
| `SPOTIFY_REFRESH_TOKEN` | no | User OAuth refresh token for Spotify Development Mode playlist reads |

## Build

For a local binary:

```bash
go build -o album-queue .
```

For a container, run BuildKit from the repository root and always select the
deployment platform explicitly. The builder runs on the build host while the
Go binary and final Alpine runtime use the requested target platform:

```bash
revision="$(git rev-parse HEAD)"
revision_short="$(git rev-parse --short=12 HEAD)"
docker buildx build \
  --platform linux/amd64 \
  --load \
  --label "org.opencontainers.image.revision=${revision}" \
  --tag "telegram-queue-bot:${revision_short}-amd64" \
  --file album-queue/Dockerfile \
  .
```

Verify the loaded image before deployment:

```bash
docker image inspect \
  --format '{{.Os}}/{{.Architecture}} {{.Id}}' \
  "telegram-queue-bot:${revision_short}-amd64"
```

Production on `music-services` is Linux/amd64. Do not use the old bare
`docker build` workflow from an ARM host: it can combine an amd64 Go binary
with an ARM64 runtime and make shell-based healthchecks fail even though the
application binary starts. `Dockerfile.dockerignore` restricts this build's
context to `album-queue` and `models` and excludes local environment files,
saved image archives, and editor state.

## Run

```bash
DATABASE_URL="mongodb://localhost:27017" \
DATABASE_NAME="music-services" \
BOT_TOKEN="..." \
BOT_WHITELIST="123456789" \
WEBHOOK_URL="http://localhost:8080/health" \
SPOTIFY_CLIENT_ID="..." \
SPOTIFY_CLIENT_SECRET="..." \
SPOTIFY_REFRESH_TOKEN="" \
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

### Playlist enqueue and display

`/p` and `/pnp` acknowledge that a one-off playlist request was inserted into
MongoDB; the acknowledgement does not prove that Spotify metadata was fetched
or that an M3U was written. `no_pull` means that the worker will not enqueue
downloads for missing tracks. It does not eliminate the Spotify name and item
requests needed to materialize the playlist.

For active one-off playlists, `/queue` renders the persisted `name` populated
by the worker and falls back to the Spotify URL when a legacy or newly queued
row has no cached name. It does not call Spotify merely to display a playlist.
This avoids adding metadata traffic while Spotify has asked the worker to back
off. Active playlist rows can include a future `next_attempt_at`, so “active”
does not necessarily mean runnable on the current worker pass. The reply shows
a future retry time in UTC and, when present, only a sanitized machine error
code; it never includes the persisted raw error message or details.

### Large `/queue` responses

`/queue` is plain text and does not update worker-owned lifecycle, lease,
result, or error fields. When its rendered response exceeds the single-message
budget, the bot:

- splits it into UTF-8-safe chunks with at most 4,000 content bytes;
- labels multipart replies `Черга — частина n/m`;
- sends parts sequentially with one second of pacing;
- honors `RetryAfter` and retries a Telegram 429 exactly once;
- stops after any other send error, or after a failed retry, so later parts do
  not hide a delivery gap.

The final label plus content is capped at 4,096 bytes, a conservative bound
under Telegram's 4,096-character text limit.

## Health Endpoints

- `GET /health`
- `GET /ready`
- `GET /stats`
