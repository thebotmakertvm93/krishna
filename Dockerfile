FROM golang:1.26-bookworm AS builder

WORKDIR /build

# Install compilation tooling alongside python3 for install.sh scripts
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        git \
        gcc \
        g++ \
        build-essential \
        pkg-config \
        unzip \
        curl \
        python3 \
        python3-pip \
        zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY install.sh ./
COPY . .

# Prepare and execute initialization scripts cleanly
RUN chmod +x install.sh
RUN ./install.sh -n --quiet --skip-summary

# Create a fallback empty text file inside internal/cookies if none exist
RUN mkdir -p internal/cookies && touch internal/cookies/placeholder.txt

# Native CGO compilation matching the Debian Bookworm target architecture
RUN CGO_ENABLED=1 GOOS=linux go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/


FROM debian:bookworm-slim

# Install system dependencies including python3 for yt-dlp runtime engine
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ffmpeg \
        curl \
        unzip \
        zlib1g \
        python3 \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# FIXED: Direct link to download the actual yt-dlp executable program binary
RUN curl -fL https://github.com -o /usr/local/bin/yt-dlp && \
    chmod 0755 /usr/local/bin/yt-dlp

# FIXED: Direct link to download the actual Deno shell installation script file
RUN curl -fsSL https://deno.land -o /tmp/deno-install.sh && \
    sh /tmp/deno-install.sh v1.46.3 && \
    mv /root/.deno/bin/deno /usr/local/bin/deno && \
    rm -rf /tmp/deno-install.sh /root/.deno

# Dynamic Hugging Face write-access container permissions setup
RUN mkdir -p /app/cache /app/downloads /app/.cache && \
    chmod -R 777 /app

# Explicitly assign PORT variable to clear Hugging Face reverse-proxy health checks
ENV PORT=7860
ENV HOME=/app
ENV XDG_CACHE_HOME=/app/.cache
ENV PATH=/usr/local/bin:/usr/bin:/bin:$PATH

COPY --from=builder /build/app /app/app
RUN chmod +x /app/app

EXPOSE 7860

ENTRYPOINT ["/app/app"]
