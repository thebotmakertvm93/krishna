# Step 1: Build the Go application
FROM golang:1.22-alpine AS builder

# Install essential build dependencies
RUN apk add --no-cache git gcc musl-dev cgo

WORKDIR /app

# Copy dependency configuration files first
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire codebase
COPY . .

# Build your production Go application binary
RUN CGO_ENABLED=1 GOOS=linux go build -v -trimpath -ldflags="-w -s" -o app ./cmd/app/

# Step 2: Create the final lightweight runtime container
FROM alpine:latest

# Install runtime dependencies needed for streaming and processing
RUN apk add --no-cache ca-certificates ffmpeg yt-dlp

WORKDIR /app

# Copy the compiled binary out of the builder step
COPY --from=builder /app/app .

# Expose a dummy port because Hugging Face monitors HTTP traffic on port 7860
EXPOSE 7860

# Run the Go bot binary
CMD ["./app"]
