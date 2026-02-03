.PHONY: build run tidy

build:
	go build -o album-normalizer .

run:
	go run .

tidy:
	go mod tidy

# Example usage:
# DATABASE_URL="mongodb://localhost:27017" DATABASE_NAME="music" make run

# Dry run (default) - just show variations:
# DATABASE_URL="..." DATABASE_NAME="..." go run . -dry-run=true

# Show metadata differences:
# DATABASE_URL="..." DATABASE_NAME="..." go run . -show-metadata

# List all albums:
# DATABASE_URL="..." DATABASE_NAME="..." go run . -list

# Show Navidrome-style grouping (MBID|album):
# DATABASE_URL="..." DATABASE_NAME="..." go run . -navidrome

# Group by MusicBrainz Album ID:
# DATABASE_URL="..." DATABASE_NAME="..." go run . -mbid

# Fix a specific album:
# DATABASE_URL="..." DATABASE_NAME="..." go run . -album="ok computer" -fix="OK Computer" -dry-run=false
