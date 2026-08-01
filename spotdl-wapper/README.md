# spotdl-wapper

Long-running worker that turns queued Spotify metadata into validated local
catalog entries. The directory name contains the historical `wapper` typo and
is retained for compatibility.

The worker is provider-neutral internally:

1. Atomically claim and backend-pin a MongoDB request, fence it with a random
   claim ID, and renew its lease.
2. Resolve the Spotify URL into canonical per-track metadata.
3. Resolve and acquire one track through the configured provider.
4. Validate and tag the staged asset with FFmpeg/FFprobe.
5. Publish the file atomically and persist a path/checksum recovery journal.
6. Upsert `music-files` and persist cataloged progress, provenance, and typed
   failure details.

The default `spotdl` provider is the compatibility and rollback path. Set
`ACQUISITION_BACKEND=yt-dlp` to opt into direct yt-dlp search and conservative
candidate scoring. Provider selection is explicit; the worker never falls back
from one backend to another automatically.

The first worker to claim an unassigned request atomically pins it to that
worker's backend. Retries and expired-lease reclaims stay pinned. Running both
backend types is therefore safe for already assigned work, but unassigned work
goes nondeterministically to whichever worker wins the claim. There is not yet
a producer cohort-routing or operator reroute API.

> **Authorized content only:** use the direct yt-dlp backend only for media you
> have the right to download. The backend choice does not change Spotify,
> YouTube, copyright, or local-law obligations.

## Backend behavior

| Backend | Selection | Matching responsibility | Intended role |
| --- | --- | --- | --- |
| `spotdl` | Default | spotDL performs source matching internally | Compatibility and rollback |
| `yt-dlp` | Explicit opt-in | The wrapper searches, scores, and rejects candidates before download | Replacement canary for authorized content |

The direct matcher rejects unexpected or missing live, remix, cover/karaoke,
nightcore, slowed, and sped-up variants. A low-confidence or absent match moves
the request to `needs_review`; there is not yet a review UI.

## Queue and playlist behavior

Download requests use durable states, atomic claims, expiring leases, lease
heartbeats, scheduled retries, and a configurable attempt limit. Work is
processed per track, so imported tracks remain recorded when a later track
fails. Legacy `active` and `errored` flags are dual-written for older services.
Lease mutations require both the worker identity and a random per-claim token,
preventing a stale process from regaining ownership merely by reusing a worker
ID. If catalog upsert fails after publication, the journal lets a retry
revalidate and catalog the same file without reacquiring it.

After draining eligible downloads, the worker processes one-off
`playlist-requests`. Missing entries can enqueue individual track requests;
`no_pull` playlists are materialized from currently cataloged files only. M3U
files are safely replaced and synced; every entry must be an existing regular
non-symlink file contained beneath the resolved music-library root. A
non-`no_pull` request with missing tracks stays active without consuming its
error budget and does not publish a partial M3U. This path no longer waits for
`index-status`.

Downloads are drained before playlist work. Sustained download traffic can
therefore delay one-off playlist processing, and a playlist can wait
indefinitely when a missing track is held in review or successful request
history without a usable catalog file.

## Configuration

Copy the repository's `.env.example` and use
[the configuration reference](../docs/configuration.md) for all settings.
Important controls are:

- `ACQUISITION_BACKEND` (`spotdl` by default, or `yt-dlp`);
- `MEDIA_OUTPUT_TEMPLATE` and `PLAYLISTS_OUTPUT_PATH`;
- `ACQUISITION_STAGING_PATH`, `ACQUISITION_AUDIO_FORMAT`, and command timeout;
- `SPOTDL_USE_CONFIG` for the default provider's standard-path configuration;
- worker poll, lease, retry-delay, and attempt settings;
- `SPOTIFY_REFRESH_TOKEN` for current Development Mode playlist reads.

`DESTINATION` remains only as a deprecated fallback for
`MEDIA_OUTPUT_TEMPLATE`.

Supported audio formats are `mp3`, `flac`, `ogg`, `opus`, `m4a`, and `wav`.
Private staging attempts use reserved, ownership-marked directories and are
discarded after failures before successful import; only old marked attempt
directories left by crashes are removed on a later processing loop.

## Build

```bash
go build -o spotdl-wapper .
```

## Run

```bash
DATABASE_URL="mongodb://localhost:27017" \
DATABASE_NAME="music-services" \
MEDIA_OUTPUT_TEMPLATE="/music/downloads/{artists} - {title}.{output-ext}" \
MUSIC_LIBRARY_PATH="/music" \
PLAYLISTS_OUTPUT_PATH="/music/playlists" \
ACQUISITION_BACKEND="spotdl" \
SPOTIFY_CLIENT_ID="..." \
SPOTIFY_CLIENT_SECRET="..." \
./spotdl-wapper
```

## Runtime Dependencies

The container currently pins Go 1.23.4, Python 3.13.14, Deno 2.9.4, spotDL
4.5.2, yt-dlp 2026.7.4, and yt-dlp-ejs 0.8.0. FFmpeg and CA/timezone packages
come from the Debian image repositories. The final process runs as UID 10001,
so the mounted staging, media, and playlist paths must be writable by that
user.

For the full as-built architecture and operational caveats, see
[the current-state document](../docs/spotdl-wapper-current-state.md). For the
decision history, rollout, rollback, and remaining replacement work, see
[the migration document](../docs/spotdl-wapper-migration.md).
