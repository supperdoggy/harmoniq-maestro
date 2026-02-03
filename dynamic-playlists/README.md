# Dynamic Playlists Service

A service that generates refreshable playlists based on MongoDB query rules, with OpenAI integration for playlist descriptions and genre classification.

## Features

- **Dynamic Playlist Generation**: Create playlists based on MongoDB query filters
- **Automatic Refresh**: Daily or weekly playlist regeneration
- **Genre Simplification**: Maps specific genres to general categories
- **OpenAI Integration**: 
  - Generates creative playlist names and descriptions
  - Classifies missing genres for music files
- **M3U Playlist Output**: Generates standard M3U playlist files

## Configuration

Set the following environment variables:

- `DATABASE_URL`: MongoDB connection string
- `DATABASE_NAME`: Database name (default: `music-services`)
- `OPENAI_API_KEY`: OpenAI API key for descriptions and genre classification
- `MUSIC_LIBRARY_PATH`: Path to music library root directory
- `PLAYLISTS_OUTPUT_PATH`: Path where M3U playlists will be written
- `DRY_RUN`: Set to `true` to run without making changes (optional)

## Setup

1. **Seed Genre Mappings**:
   ```bash
   mongo music-services seed-genre-mappings.js
   ```

2. **Seed Example Playlists** (optional):
   ```bash
   mongo music-services seed-dynamic-playlists.js
   ```

3. **Build the service**:
   ```bash
   make build-linux
   ```

4. **Deploy**:
   ```bash
   make deploy
   ```

5. **Setup Cron** (on server):
   ```bash
   ./setup-dynamic-playlists-cron.sh
   ```

## Usage

Run manually:
```bash
./run_dynamic-playlists.sh
```

Or via cron (default: daily at 3 AM):
```bash
0 3 * * * /root/run_dynamic-playlists.sh
```

## Creating Dynamic Playlists

Insert a document into the `dynamic_playlists` collection:

```javascript
db.dynamic_playlists.insertOne({
  _id: "my-playlist-id",
  name: "My Playlist",
  description: "Optional description (will be generated if empty)",
  query: {
    genre: {$regex: "rock", $options: "i"},
    created_at: {$gte: <timestamp>}
  },
  refresh_frequency: "daily", // or "weekly"
  max_tracks: 100,
  sort_by: "created_at", // or "random"
  active: true,
  output_path: "",
  last_refreshed: 0,
  track_count: 0,
  created_at: Math.floor(Date.now() / 1000),
  updated_at: Math.floor(Date.now() / 1000)
});
```

## Query Examples

**Recent tracks**:
```javascript
{created_at: {$gte: <7_days_ago>}}
```

**Genre-based**:
```javascript
{genre: {$regex: "jazz", $options: "i"}}
```

**Multiple genres**:
```javascript
{$or: [
  {genre: {$regex: "rock", $options: "i"}},
  {genre: {$regex: "metal", $options: "i"}}
]}
```

**Combined filters**:
```javascript
{
  $and: [
    {genre: {$regex: "electronic", $options: "i"}},
    {created_at: {$gte: <30_days_ago>}}
  ]
}
```

## Genre Simplification

The service uses a `genre_mappings` collection to map specific genres to simplified categories:

- `post-punk` → `rock`
- `deep house` → `electronic`
- `trap` → `hip-hop`

If a music file has an empty genre, OpenAI will classify it based on artist, title, and album metadata.

## Output

Playlists are generated as M3U files in the `PLAYLISTS_OUTPUT_PATH` directory:
- Format: `{playlist_name}.m3u`
- Contains relative paths to music files
- Compatible with standard music players
