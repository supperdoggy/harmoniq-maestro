# Quickstart

This quickstart targets Linux-first self-hosted usage with Docker Compose.

## 1. Prerequisites

- Docker Engine 24+
- Docker Compose v2+
- Spotify API credentials
- Telegram bot token
- OpenAI API key (for dynamic playlists)
- Local music directory path

## 2. Configure Environment

```bash
cp .env.example .env
```

Edit `.env`:

- Set Spotify and Telegram credentials.
- Set `MUSIC_LIBRARY_PATH` to your local library mount path.
- Set `OPENAI_API_KEY`.

## 3. Start Services

```bash
docker compose up -d --build
```

## 4. Validate

Album queue health:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Service state:

```bash
docker compose ps
docker compose logs -f --tail=100
```

## 5. Use the Bot

- Add your Telegram user ID to `BOT_WHITELIST`.
- Send Spotify album/playlist/track URLs to the bot.
- The worker consumes queued requests from MongoDB.

## Notes

- `dynamic-playlists` runs in a periodic loop inside the container.
- `spotdl-wapper` requires write access to mounted music directories.
