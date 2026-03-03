# Configuration

## Global Compose Variables

- `MONGO_ROOT_USERNAME`: MongoDB root user
- `MONGO_ROOT_PASSWORD`: MongoDB root password
- `MONGO_DATABASE`: Database name used by services

## Shared Service Variables

- `DATABASE_URL`: MongoDB connection URI
- `DATABASE_NAME`: Database name
- `SPOTIFY_CLIENT_ID`: Spotify client ID
- `SPOTIFY_CLIENT_SECRET`: Spotify client secret

## album-queue

- `BOT_TOKEN`: Telegram bot token
- `BOT_WHITELIST`: Comma-separated Telegram user IDs
- `WEBHOOK_URL`: URL pinged when new queue items are added

## spotdl-wapper

- `DESTINATION`: Download output template path
- `MUSIC_LIBRARY_PATH`: Root music library path
- `SLEEP_IN_MINUTES`: Delay between processing items
- `LOKI_ENABLED`: Optional Loki logging toggle (`true/false`)
- `LOKI_URL`: Optional Loki endpoint URL

## dynamic-playlists

- `OPENAI_API_KEY`: OpenAI API key
- `PLAYLISTS_OUTPUT_PATH`: Directory for generated M3U files
- `DRY_RUN`: `true/false`
- `DYNAMIC_PLAYLISTS_INTERVAL_SECONDS`: Loop interval in compose service

## Volume and Path Notes

- Ensure `MUSIC_LIBRARY_PATH` points to an existing host directory.
- Keep `PLAYLISTS_OUTPUT_PATH` inside the mounted library for portability.
