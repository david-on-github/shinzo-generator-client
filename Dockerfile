# Multi-stage build for Shinzo Network Ethereum Generator.
# Stage 1: Builder stage
FROM golang:1.26@sha256:dc2521c2a906db43073b8b4d99f491b6341cf15610b6ebbab187c45153f9959e AS builder   # digest-pinned; dependabot proposes updates

# Build arguments
ARG BUILD_DATE
ARG VCS_REF
ARG VERSION=dev
ARG BUILD_TAGS

# Build dependencies
RUN apt-get update && apt-get install -y \
    git \
    ca-certificates \
    tzdata \
    make \
    build-essential \
    pkg-config \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies (this should be cached if go.mod/go.sum don't change)
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify

ENV CGO_ENABLED=1

# Copy source code
COPY . .

# Build the application (lens transforms run on wazero, pure Go)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -ex && \
    BUILD_DATE=$(date -u -Iseconds | sed 's/+00:00/Z/') && \
    VCS_REF=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown") && \
    echo "Building for VERSION=${VERSION}, BUILD_DATE=${BUILD_DATE}, VCS_REF=${VCS_REF}, BUILD_TAGS=${BUILD_TAGS}" && \
    mkdir -p bin && \
    CGO_ENABLED=1 go build -v \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildDate=${BUILD_DATE} -X main.gitCommit=${VCS_REF}" \
    ${BUILD_TAGS:+-tags="${BUILD_TAGS}"} \
    -o bin/block_poster \
    cmd/block_poster/main.go && \
    echo "Build completed, checking binary:" && \
    ls -la bin/ && \
    echo "Binary created successfully"

# Stage 2: Runtime stage
FROM ubuntu:24.04@sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517   # digest-pinned; dependabot proposes updates

# Re-declare build arguments for this stage
ARG BUILD_DATE
ARG VCS_REF
ARG VERSION=dev

# Labels for metadata
LABEL maintainer="Shinzo Network <team@shinzo.network>" \
      org.opencontainers.image.title="Shinzo Network Generator" \
      org.opencontainers.image.description="Ethereum blockchain generator for Shinzo Network" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.source="https://github.com/shinzonetwork/shinzo-generator-client"

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    tzdata \
    curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for security
RUN groupadd -g 1001 shinzo-generator && \
    useradd -u 1001 -g shinzo-generator -m -s /bin/bash shinzo-generator

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/bin/block_poster /app/block_poster

# Copy configuration files
COPY --from=builder /app/config/ /app/config/
COPY --from=builder /app/pkg/schema/ /app/pkg/schema/

# Create necessary directories with proper permissions
RUN mkdir -p /app/data && \
    chown -R shinzo-generator:shinzo-generator /app && \
    chmod -R 755 /app && \
    chmod +x /app/block_poster

# Switch to non-root user
# All node state (database, keys, lens registry) lives here. Declared so that
# even a bare `docker run` gets a volume instead of writing into the container layer.
VOLUME ["/app/data"]

USER shinzo-generator

# Health check with better error handling
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Expose ports health, p2p, graphql
EXPOSE 8080 9171

# Default command
CMD ["./block_poster", "-config", "config/config.yaml"]
