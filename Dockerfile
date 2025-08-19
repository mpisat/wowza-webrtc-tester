# Build stage
FROM golang:1.24.6-alpine3.22 AS builder

# Install build dependencies
RUN apk add --no-cache \
    gcc \
    g++ \
    musl-dev \
    pkgconfig \
    opus-dev \
    x264-dev \
    ffmpeg-dev

# Set Go environment
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=amd64

# Create app directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go ./

# Build the application
RUN go build -o wowza-webrtc-tester main.go

# Runtime stage
FROM alpine:3.20

# Install only runtime libraries
RUN apk add --no-cache \
    opus \
    x264-libs \
    ffmpeg-libs \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN adduser -D -s /bin/sh appuser

# Copy built binary
COPY --from=builder /app/wowza-webrtc-tester /usr/local/bin/

# Change to non-root user
USER appuser

# Default command
ENTRYPOINT ["/usr/local/bin/wowza-webrtc-tester"]
CMD ["-help"]