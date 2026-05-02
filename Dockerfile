# syntax=docker/dockerfile:1.7

# Stage 1: Build the Go binary
FROM golang:1.26-alpine AS builder

# Install build dependencies for CGO (SQLite)
RUN apk add --no-cache build-base

WORKDIR /app

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy only the files required for the build/runtime assets
COPY cmd ./cmd
COPY ent ./ent
COPY internal ./internal
COPY web ./web

# Build the application with CGO enabled for SQLite
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o ferry ./cmd/ferry/main.go

# Stage 2: Final lightweight image
FROM alpine:latest

# Install runtime dependencies (ca-certificates for HTTPS calls, tzdata for correct logging)
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary from the builder
COPY --from=builder /app/ferry .

# Copy static assets required by the server
COPY --from=builder /app/web/templates ./web/templates
COPY --from=builder /app/internal/i18n ./internal/i18n

# Create directories for storage and database
RUN mkdir -p data/storage data/db

# Expose the default port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

# Default command: start the server. The server opens the database and runs migrations.
ENTRYPOINT ["./ferry"]
CMD ["serve"]
