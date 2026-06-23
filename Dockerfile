FROM golang:1.26.1-bookworm AS builder

WORKDIR /build

# Install full development tooling needed for strict CGO compiling
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        git \
        gcc \
        g++ \
        build-essential \
        pkg-config \
        unzip \
        curl \
        zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY install.sh ./
COPY . .

# Separate instructions to pinpoint failures in the Render build logs
RUN chmod +x install.sh
RUN ./install.sh -n --quiet --skip-summary

# FIX: Create a fallback empty text file inside internal/cookies if none exist
# This satisfies the Go compiler's strict //go:embed directive
RUN mkdir -p internal/cookies && touch internal/cookies/placeholder.txt

# Native CGO compilation with proper pathing definitions
RUN CGO_ENABLED=1 GOOS=linux go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/


FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ffmpeg \
        curl \
        unzip \
        zlib1g \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    useradd -r -u 10001 -m -d /home/appuser appuser

WORKDIR /app

RUN curl -fL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp && \
    chmod 0755 /usr/local/bin/yt-dlp && \
    curl -fsSL https://deno.land/x/install/install.sh -o /tmp/deno-install.sh && \
    sh /tmp/deno-install.sh && \
    mv /root/.deno/bin/deno /usr/local/bin/deno && \
    rm -rf /tmp/deno-install.sh /root/.deno
    
# Set environment paths so appuser has zero execution blocks
ENV DENO_INSTALL=/home/appuser/.deno
ENV PATH=$DENO_INSTALL/bin:$PATH

RUN mkdir -p /app/cache /home/appuser/.cache && \
    chown -R appuser:appuser /app/cache /home/appuser/.cache

COPY --from=builder /build/app /app/app

# FIX: Give appuser ownership of the /app directory so it can create logs.txt
RUN chown -R appuser:appuser /app && \
    chmod +x /app/app

EXPOSE 10000

USER appuser

ENTRYPOINT ["/app/app"]
