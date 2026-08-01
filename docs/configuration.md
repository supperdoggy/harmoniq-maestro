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
- `SPOTIFY_REFRESH_TOKEN`: Optional Spotify user OAuth refresh token. Development
  Mode playlist reads require user authorization; keep this value secret.

## album-queue

- `BOT_TOKEN`: Telegram bot token
- `BOT_WHITELIST`: Comma-separated Telegram user IDs
- `WEBHOOK_URL`: URL pinged when new queue items are added

## spotdl-wapper

### Worker lifecycle

- `WORKER_POLL_INTERVAL`: Worker ticker cadence. Uses Go duration syntax and
  defaults to `1m`. The ticker starts before a processing pass, so a pass that
  lasts longer than this interval can be followed immediately by the next pass;
  this is not a guaranteed idle delay after each pass.
- `WORKER_ID`: Optional stable worker identity. When empty, the process uses
  its hostname and generates a fallback only if the hostname is unavailable.
- `WORKER_LEASE_DURATION`: Queue claim duration, using Go duration syntax.
  Defaults to `45m` and must be at least `3s`. A claimed request is renewed at
  one-third of this duration, bounded to an interval between `1s` and `30s`.
- `WORKER_RETRY_DELAY`: Backoff before retrying a retryable request, using Go
  duration syntax. Defaults to `15m` and must be greater than zero. It also
  applies while retrying catalog finalization for an already published
  recovery-journal artifact.
- `WORKER_MAX_ATTEMPTS`: Maximum budget-consuming acquisition-pipeline failures
  before the request is moved to `failed`. Retryable metadata, resolution,
  download, and pre-publication import failures consume this budget;
  non-retryable ambiguity moves directly to `needs_review`. Once an artifact
  has been published and entered recovery-journal/catalog finalization,
  retryable finalization failures back off without incrementing this counter,
  so they can continue beyond the configured limit. Defaults to `3` and must
  be at least `1`.
- `SLEEP_IN_MINUTES`: Legacy delay between individual requests inside one
  processing pass. Defaults to `1`; set `0` to disable it.

The worker runs continuously and handles `SIGINT` and `SIGTERM`. Compose gives
it a 30-second shutdown grace period.

### Acquisition

- `ACQUISITION_BACKEND`: Acquisition implementation. Allowed values are
  `spotdl` and `yt-dlp`; the default is `spotdl`. Selecting `yt-dlp` is an
  explicit opt-in—there is no automatic fallback from spotDL. A request with no
  backend is atomically pinned to the backend of the first worker that claims
  it. Retries remain pinned, so changing this variable does not migrate
  previously claimed work. There is no producer cohort-routing or operator
  reroute API yet.
- `ACQUISITION_AUDIO_FORMAT`: Requested output audio format. It is normalized
  to lowercase and defaults to `mp3`. Supported values are `mp3`, `flac`,
  `ogg`, `opus`, `m4a`, and `wav`; the direct backend maps `ogg` to yt-dlp's
  `vorbis` extractor format.
- `ACQUISITION_COMMAND_TIMEOUT`: Maximum duration of each downloader,
  FFmpeg, or FFprobe command, using Go duration syntax. Defaults to `30m`.
- `ACQUISITION_STAGING_PATH`: Root for private per-attempt download
  directories before validation and atomic import. Defaults to
  `/music/.staging`. At the start of every processing loop, marked private
  `.harmoniq-attempt-*` directories older than twice the larger of the command
  timeout and lease duration are removed. Direct files, symbolic links, and
  unmarked directories in the staging root are not removed by this cleanup.
  The staging root must differ from `MUSIC_LIBRARY_PATH`, and the final media
  template must not resolve inside staging.
- `YTDLP_SEARCH_LIMIT`: Maximum yt-dlp candidates resolved for scoring.
  Defaults to `10`; accepted values are `1` through `50`.
- `YTDLP_MINIMUM_SCORE`: Minimum normalized candidate confidence accepted by
  the yt-dlp provider. Defaults to `0.72`; accepted values are `0` through `1`.
  An explicit `0` is honored and disables only this aggregate-score threshold;
  hard title, artist, duration, and variant rejection rules still apply.
- `SPOTDL_BINARY`: spotDL executable path. Defaults to
  `/usr/local/bin/spotdl`.
- `YTDLP_BINARY`: yt-dlp executable path. Defaults to
  `/usr/local/bin/yt-dlp`.
- `SPOTDL_USE_CONFIG`: Whether the compatibility provider passes spotDL's
  boolean `--config` flag. Defaults to `true`. spotDL discovers its config from
  its standard locations; this setting is not a file path.
- `FFMPEG_BINARY`: FFmpeg executable used for media conversion. Defaults to
  `ffmpeg`.
- `FFPROBE_BINARY`: FFprobe executable used to validate acquired assets.
  Defaults to `ffprobe`.
- `MEDIA_DURATION_TOLERANCE`: Allowed difference between catalog and acquired
  media duration before validation fails. Uses Go duration syntax and defaults
  to `15s`. The importer also allows 5% of the expected duration when that is
  larger. An explicit `0s` is honored, but the proportional 5% allowance still
  applies when an expected duration is known.

Go durations combine units when useful, for example `30s`, `5m`, `1h`, or
`1h30m`. Poll, retry-delay, and command durations must be positive; the lease
must be at least `3s`. Media tolerance and `SLEEP_IN_MINUTES` may be zero but
not negative.

### Library paths

- `MUSIC_LIBRARY_PATH`: Required root music library path.
- `MEDIA_OUTPUT_TEMPLATE`: Required final-library filename template, for example
  `/music/downloads/{artists}-{title}.{output-ext}`. Supported placeholders are
  `{artists}`, `{artist}`, `{title}`, `{album}`, `{spotify-id}`, and
  `{output-ext}`. A relative template is resolved beneath
  `MUSIC_LIBRARY_PATH`; `{output-ext}` is recommended so the container matches
  the selected audio format. Expanded path components that would exceed safe
  filesystem limits are UTF-8-safely shortened with a deterministic hash
  suffix, with filename headroom reserved for collision suffixes.
- `PLAYLISTS_OUTPUT_PATH`: Directory for generated M3U files. When omitted, it
  defaults to `<MUSIC_LIBRARY_PATH>/playlists`.
- `DESTINATION`: Deprecated compatibility alias for
  `MEDIA_OUTPUT_TEMPLATE`. It is used only when `MEDIA_OUTPUT_TEMPLATE` is
  empty, and the worker logs a value-free deprecation warning.

`SPOTDL_CONFIG_PATH` is a Compose-only host directory containing spotDL's
`config.json`. It defaults to `./.spotdl`. Compose mounts it at the current
`/home/appuser/.config/spotdl` location and temporarily at the legacy
`/home/appuser/.spotdl` location so existing adapters continue to work.

### Logging

- `LOKI_ENABLED`: Optional Loki logging toggle (`true/false`), defaulting to
  `false` in both the application and Compose.
- `LOKI_URL`: Optional Loki endpoint URL. Treat embedded credentials or tokens
  as secrets.

Startup logging uses an explicit non-secret allowlist. It does not serialize
the configuration or log Spotify credentials, refresh tokens, MongoDB URLs, or
the Loki URL.

## dynamic-playlists

- `OPENAI_API_KEY`: OpenAI API key
- `PLAYLISTS_OUTPUT_PATH`: Directory for generated M3U files
- `DRY_RUN`: `true/false`
- `DYNAMIC_PLAYLISTS_INTERVAL_SECONDS`: Loop interval in compose service

## Volume and Path Notes

- Ensure `MUSIC_LIBRARY_PATH` points to an existing host directory.
- Keep `PLAYLISTS_OUTPUT_PATH` inside the mounted library for portability.
- Keep media and playlist output settings inside the same mounted library when
  both must be visible to other services.
- The importer writes its tagged temporary file beside the final destination
  and publishes it with a hard link. The final library filesystem must support
  hard links; the staging directory may be on another filesystem. Published
  media is set to mode `0640`, synced before publication, and followed by an
  output-directory sync.
