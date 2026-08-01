# syntax=docker/dockerfile:1

ARG GO_VERSION=1.23.4
ARG PYTHON_VERSION=3.13.14
ARG DENO_VERSION=2.9.4

# The official bin image contains only the pinned, multi-platform Deno binary.
FROM denoland/deno:bin-${DENO_VERSION} AS deno

FROM golang:${GO_VERSION}-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy the shared module used by the local replace directive first so module
# downloads remain cacheable when application source changes.
COPY models /models
COPY spotdl-wapper/go.mod spotdl-wapper/go.sum ./
RUN go mod download

COPY spotdl-wapper/ .

# The builder image follows the requested target platform, so Go's native
# GOOS/GOARCH defaults keep the binary aligned with the runtime image.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o spotdl-wapper .

FROM python:${PYTHON_VERSION}-slim-bookworm

ENV DENO_DIR=/home/appuser/.cache/deno \
    DENO_NO_UPDATE_CHECK=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        ffmpeg \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=deno /deno /usr/local/bin/deno

# Pin the downloader entry points. Transitive compatibility is checked during
# the image build so an incompatible resolver result fails immediately.
RUN python -m pip install --no-cache-dir \
        "spotdl==4.5.2" \
        "yt-dlp==2026.7.4" \
        "yt-dlp-ejs==0.8.0" \
	    && python -m pip check \
	    && deno --version \
	    && spotdl --version \
	    && yt-dlp --version

RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser \
    && install -d -o appuser -g appuser \
        /home/appuser/.cache/deno \
        /home/appuser/.config/spotdl \
        /home/appuser/.spotdl

COPY --from=builder --chown=appuser:appuser /app/spotdl-wapper .

USER appuser

CMD ["./spotdl-wapper"]
