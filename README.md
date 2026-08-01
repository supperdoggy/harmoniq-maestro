# Harmoniq Maestro

Harmoniq Maestro is a Linux-first, self-hosted music automation stack.

It provides queue-based Spotify metadata ingestion, provider-neutral media
acquisition, validated import into a local music catalog, and dynamic playlist
generation.

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
- [spotdl-wapper](./spotdl-wapper/README.md): Long-running acquisition worker
  with a rollback-compatible spotDL backend (default) and an opt-in direct
  `yt-dlp` backend. The first claim durably pins each request to one backend;
  there is no automatic cross-provider fallback.
- [dynamic-playlists](./dynamic-playlists/README.md): Periodic dynamic and subscribed playlist generation.
- [models](./models/README.md): Shared models and MongoDB data access layer.

The wrapper's implemented architecture, lifecycle, storage contracts, and
known limitations are documented in
[docs/spotdl-wapper-current-state.md](./docs/spotdl-wapper-current-state.md).
The migration decision record and rollout/rollback guidance are in
[docs/spotdl-wapper-migration.md](./docs/spotdl-wapper-migration.md).
For the observed `music-services` VM, use the deployment-specific
[current-state snapshot](./docs/vm-infrastructure-current-state.md) and
[migration runbook](./docs/vm-infrastructure-spotdl-migration.md); the root
Compose file is not the live VM topology and must not be applied there
unchanged.

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

This project integrates with third-party APIs and tools (including Spotify,
OpenAI, spotDL, yt-dlp, and media providers). You are responsible for complying
with their terms, content licenses, and local laws.

The direct `yt-dlp` backend is an explicit opt-in. Use it only for content you
are authorized to download; changing acquisition software does not grant
rights to third-party media.

## License

[MIT](./LICENSE)
