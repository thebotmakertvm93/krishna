FROM golang:1.26.1-bookworm AS builder

WORKDIR /build

# hadolint ignore=DL3015
RUN apt-get update && \
    apt-get install -y \
        git \
        gcc \
        unzip \
        curl \
        zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY install.sh ./
COPY . .

RUN chmod +x install.sh && \
    ./install.sh -n --quiet --skip-summary && \
    CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/

FROM debian:bookworm-slim

# Install system dependencies and create non-root user early
RUN apt-get update && \
    apt-get install -y \
        ffmpeg \
        curl \
        unzip \
        zlib1g \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    useradd -r -u 10001 -m -d /home/appuser appuser

# Switch to root-owned app folder for binaries
WORKDIR /app

# Download yt-dlp and Deno directly to global binary paths
RUN curl -fL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux -o /usr/local/bin/yt-dlp && \
    chmod 0755 /usr/local/bin/yt-dlp && \
    curl -fsSL https://deno.land/install.sh -o /tmp/deno-install.sh && \
    sh /tmp/deno-install.sh -d /usr/local/bin && \
    rm -f /tmp/deno-install.sh

# Set up specific runtime cache directories for appuser
RUN mkdir -p /app/cache /home/appuser/.cache && \
    chown -R appuser:appuser /app/cache /home/appuser/.cache

# Copy built application binary
COPY --from=builder /build/app /app/app
RUN chmod +x /app/app

# Expose Render's default routing port (Render overrides this via env)
EXPOSE 10000

USER appuser

ENTRYPOINT ["/app/app"]
