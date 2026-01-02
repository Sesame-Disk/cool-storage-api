# Build stage
FROM --platform=$BUILDPLATFORM golang:1.25-trixie AS builder

ARG TARGETOS TARGETARCH

WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary for the target platform
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-arm64} go build \
    -ldflags="-w -s" \
    -o sesamefs \
    ./cmd/sesamefs

# Runtime stage
FROM debian:trixie-slim

# Install ca-certificates for HTTPS, tzdata for timezones, wget for healthchecks
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        wget && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

# Create non-root user for security
RUN useradd -r -u 1000 -s /bin/false sesamefs

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/sesamefs .

# Copy config files if needed
COPY --from=builder /build/config*.yaml* ./

# Use non-root user
USER sesamefs

# Expose API port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O - http://localhost:8080/ping || exit 1

ENTRYPOINT ["/app/sesamefs"]
CMD ["serve"]
