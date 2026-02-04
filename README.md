# Harmoniq Maestro

A monorepo for music library management services. This collection of microservices handles Spotify playlist downloads, music file indexing, deduplication, and dynamic playlist generation.

## Services

| Service | Description |
|---------|-------------|
| [album-queue](./album-queue/) | Telegram bot for queueing Spotify album/playlist downloads |
| [spotdl-wapper](./spotdl-wapper/) | Go wrapper for spotdl that processes download queue requests |
| [dynamic-playlists](./dynamic-playlists/) | Generates dynamic playlists based on genre classification using OpenAI |
| [ai-playlist-composer](./ai-playlist-composer/) | AI-powered playlist composition tool |
| [album-normalizer](./album-normalizer/) | Normalizes album metadata (ID3 tags, FLAC tags) |
| [deduplicator](./deduplicator/) | Identifies and removes duplicate music files across playlists |

## Shared Packages

| Package | Description |
|---------|-------------|
| [models](./models/) | Shared data models and database operations (`github.com/supperdoggy/spot-models`) |

## Architecture

```
harmoniq-maestro/
├── models/                    # Shared models package
│   ├── database/              # MongoDB operations
│   └── spotify/               # Spotify API types and client
├── album-queue/               # Telegram bot service
├── spotdl-wapper/             # Download processor service
├── dynamic-playlists/         # Playlist generator service
├── ai-playlist-composer/      # AI playlist tool
├── album-normalizer/          # Metadata normalizer
└── deduplicator/              # Duplicate file handler
```

## Data Flow

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│ album-queue │────▶│ spotdl-wapper│────▶│ Music Library   │
│ (Telegram)  │     │ (Download)   │     │ (Files on disk) │
└─────────────┘     └──────────────┘     └────────┬────────┘
                                                  │
                    ┌──────────────┐              │
                    │ deduplicator │◀─────────────┤
                    └──────────────┘              │
                                                  │
                    ┌──────────────────┐          │
                    │ dynamic-playlists│◀─────────┘
                    │ (Genre-based)    │
                    └──────────────────┘
```

## Prerequisites

- Go 1.23+
- MongoDB
- Spotify API credentials
- OpenAI API key (for dynamic-playlists)
- [spotdl](https://github.com/spotDL/spotify-downloader) installed (for spotdl-wapper)

## Getting Started

### 1. Clone the repository

```bash
git clone https://github.com/supperdoggy/SmartHomeServer.git
cd SmartHomeServer/harmoniq-maestro
```

### 2. Set up environment variables

Each service requires specific environment variables. See individual service READMEs for details:

- [album-queue/readme.md](./album-queue/readme.md)
- [spotdl-wapper/readme.md](./spotdl-wapper/readme.md)
- [dynamic-playlists/README.md](./dynamic-playlists/README.md)

Common variables across services:

```bash
DATABASE_URL="mongodb://localhost:27017"
DATABASE_NAME="music-services"
```

### 3. Build a service

```bash
cd album-queue
go build -o album-queue .
```

### 4. Run with Docker (where available)

```bash
cd album-queue
docker build -t album-queue .
docker run -e BOT_TOKEN=xxx -e DATABASE_URL=xxx album-queue
```

## Development

### Module Structure

Each service is its own Go module with a local replace directive for the shared models package:

```go
// go.mod
module github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/<service-name>

replace github.com/supperdoggy/spot-models => ../models

require (
    github.com/supperdoggy/spot-models v0.0.0
    // ... other dependencies
)
```

### Running Tests

```bash
# Run tests for a specific service
cd album-queue
go test ./...

# Run tests for models
cd models
go test ./...
```

### Adding a New Service

1. Create a new directory: `mkdir new-service && cd new-service`
2. Initialize the module:
   ```bash
   go mod init github.com/supperdoggy/SmartHomeServer/harmoniq-maestro/new-service
   ```
3. Add the models replace directive to `go.mod`:
   ```
   replace github.com/supperdoggy/spot-models => ../models
   ```
4. Import shared models:
   ```go
   import "github.com/supperdoggy/spot-models"
   import "github.com/supperdoggy/spot-models/database"
   ```

## License

MIT
