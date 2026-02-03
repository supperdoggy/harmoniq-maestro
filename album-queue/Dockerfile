# Use the official Golang image as a build stage
FROM golang:1.23-alpine AS builder

# Install git and other dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy the local models module first (needed for replace directive)
COPY models /models

# Copy go.mod and go.sum files to the workspace
COPY album-queue/go.mod album-queue/go.sum ./

# Download all Go modules
RUN go mod download

# Copy the rest of the application's source code
COPY album-queue/ .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o main .

# Use a minimal base image for the final stage
FROM alpine:3.20

WORKDIR /app

# Install only runtime dependencies (no git needed)
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user for security
RUN adduser -D -g '' appuser
USER appuser

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/main .

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the executable
CMD ["./main"]
