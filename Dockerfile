# LombokDNSProxy — Multi-stage Dockerfile
# Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
#
# Build:  docker build -t codinglombok/lombok-dns-proxy:latest .
# Run:    docker run -p 53:53/udp -p 7787:7787 -p 7788:7788 -p 7789:7789 \
#                    codinglombok/lombok-dns-proxy:latest

# ─── Stage 1: Build CLI binary ────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install CA certs (needed for DoH upstream connections in builder)
RUN apk add --no-cache ca-certificates git

# Cache Go module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY src/     src/
COPY internal/ internal/

# Build static binary (CGO_ENABLED=0 for scratch/distroless compatibility)
ARG VERSION=v0.0.0-docker
ARG BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags "-X github.com/codinglombok/lombok-dns-proxy/internal/build.Version=${VERSION} \
              -X github.com/codinglombok/lombok-dns-proxy/internal/build.BuildTime=${BUILD_TIME} \
              -s -w" \
    -o /lombok-dns \
    ./src/cli/main.go

# ─── Stage 2: Build filter list compiler ─────────────────────────────────────
FROM golang:1.22-alpine AS list-compiler

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY scripts/compile_lists/ scripts/compile_lists/
RUN go build -o /compile_lists ./scripts/compile_lists/main.go

# ─── Stage 3: Runtime image ───────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.title="LombokDNSProxy" \
      org.opencontainers.image.description="Privacy-first DNS proxy + ad blocker" \
      org.opencontainers.image.source="https://github.com/codinglombok/lombok-dns-proxy" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Lombok Ecosystem (@codinglombok)"

# Copy binary and CA certs from builder
COPY --from=builder /lombok-dns /usr/local/bin/lombok-dns
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy default config
COPY configs/docker.yaml /etc/lombok-dns/config.yaml

# Copy pre-downloaded lists (populated during image build)
COPY lists/custom-block.txt /var/lib/lombok-dns/lists/custom-block.txt
COPY lists/custom-allow.txt /var/lib/lombok-dns/lists/custom-allow.txt

# Expose all ports
EXPOSE 53/udp   # DNS UDP
EXPOSE 53/tcp   # DNS TCP
EXPOSE 853/tcp  # DNS-over-TLS
EXPOSE 7787/tcp # Web dashboard
EXPOSE 7788/tcp # HTTP proxy + extension WebSocket
EXPOSE 7789/tcp # SOCKS5 proxy

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/lombok-dns", "status"]

# Run as non-root user (distroless nonroot = uid 65532)
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/lombok-dns"]
CMD ["start", "--config", "/etc/lombok-dns/config.yaml"]
