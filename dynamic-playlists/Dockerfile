FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY models /models
COPY dynamic-playlists/go.mod dynamic-playlists/go.sum ./
RUN go mod download

COPY dynamic-playlists/ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o dynamic-playlists .

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -g '' appuser
USER appuser

COPY --from=builder /app/dynamic-playlists .

CMD ["./dynamic-playlists"]
