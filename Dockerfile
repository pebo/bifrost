# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build argument for version
ARG VERSION=dev

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o bifrost ./cmd/main.go

# Runtime stage
FROM alpine:3.23

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bifrost /app/bifrost

# Create non-root user
RUN addgroup -g 1000 bifrost && \
    adduser -D -u 1000 -G bifrost bifrost && \
    chown -R bifrost:bifrost /app

USER bifrost

EXPOSE 9000

ENTRYPOINT ["/app/bifrost"]
CMD ["-config", "/etc/bifrost/config.yaml"]
