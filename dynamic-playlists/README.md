# Dynamic Playlists Service

Generates refreshable playlists based on MongoDB query rules, with OpenAI-based
description and genre support.

## Features

- Dynamic playlist generation from MongoDB filters
- Daily/weekly refresh semantics stored in DB records
- Genre mapping via `genre_mappings` collection
- Optional OpenAI usage for descriptions and missing genre classification
- M3U output generation

## Environment Variables

- `DATABASE_URL` (required)
- `DATABASE_NAME` (required)
- `OPENAI_API_KEY` (required)
- `MUSIC_LIBRARY_PATH` (required)
- `PLAYLISTS_OUTPUT_PATH` (required)
- `SPOTIFY_CLIENT_ID` (required)
- `SPOTIFY_CLIENT_SECRET` (required)
- `DRY_RUN` (optional, default `false`)

## Build

```bash
go build -o dynamic-playlists .
```

## Run

```bash
DATABASE_URL="mongodb://localhost:27017" \
DATABASE_NAME="music-services" \
OPENAI_API_KEY="..." \
MUSIC_LIBRARY_PATH="/music" \
PLAYLISTS_OUTPUT_PATH="/music/playlists" \
SPOTIFY_CLIENT_ID="..." \
SPOTIFY_CLIENT_SECRET="..." \
./dynamic-playlists
```

## Dynamic Playlist Document Example

```javascript
db.dynamic_playlists.insertOne({
  _id: "my-playlist-id",
  name: "My Playlist",
  description: "Optional description",
  query: {
    genre: {$regex: "rock", $options: "i"}
  },
  refresh_frequency: "daily",
  max_tracks: 100,
  sort_by: "created_at",
  active: true,
  output_path: "",
  last_refreshed: 0,
  track_count: 0,
  created_at: Math.floor(Date.now() / 1000),
  updated_at: Math.floor(Date.now() / 1000)
})
```
