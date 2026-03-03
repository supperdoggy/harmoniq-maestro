# Harmoniq Maestro

Harmoniq Maestro is a Linux-first, self-hosted music automation stack.

It provides queue-based Spotify ingestion, download processing, and dynamic
playlist generation over a local music library.

## Quickstart

1. Copy environment template:

```bash
cp .env.example .env
```

2. Edit `.env` with your credentials and paths.

3. Start core services:

```bash
docker compose up -d --build
```

4. Check health:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Detailed setup is in [docs/quickstart.md](./docs/quickstart.md).

## Supported Core Services

- [album-queue](./album-queue/README.md): Telegram bot that accepts Spotify links and writes queue items.
- [spotdl-wapper](./spotdl-wapper/README.md): Worker that consumes queue requests and runs `spotdl`.
- [dynamic-playlists](./dynamic-playlists/README.md): Periodic dynamic and subscribed playlist generation.
- [models](./models/README.md): Shared models and MongoDB data access layer.

## Experimental Services

These are in the repository but not part of the OSS launch support contract:

- `ai-playlist-composer`
- `album-normalizer`
- `deduplicator`
- `music-files-indexer`

## Development

Run core checks from repo root:

```bash
make test-core
make build-core-linux
```

Optional:

```bash
make lint-core
make scan-secrets
```

## Legal Notice

This project integrates with third-party APIs/tools (Spotify, OpenAI, `spotdl`,
and YouTube-related providers). You are responsible for complying with all
applicable terms, licenses, and local laws.

## License

[MIT](./LICENSE)
