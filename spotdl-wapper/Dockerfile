# Use the official Golang image as a build stage
FROM golang:1.23-alpine AS builder

# Install git and other dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go.mod and go.sum files to the workspace
COPY go.mod go.sum ./

# Download all Go modules
RUN go mod download

# Copy the rest of the application's source code
COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o spotdl-wapper .

# Use a minimal base image for the final stage
FROM python:3.13-alpine

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    ffmpeg \
    nodejs \
    npm

# Install spotdl and yt-dlp
RUN pip install --no-cache-dir spotdl yt-dlp yt-dlp-ejs

# Create non-root user for security
RUN adduser -D -g '' appuser

# Create spotdl config directory
RUN mkdir -p /home/appuser/.spotdl && chown -R appuser:appuser /home/appuser

USER appuser

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/spotdl-wapper .

# Run the executable
CMD ["./spotdl-wapper"]
