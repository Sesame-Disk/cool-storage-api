# Seafile CLI container for testing sync protocol
# Uses the upstream Seafile CLI AppImage from GitHub releases because the old
# linux-clients.seafile.com apt repository is no longer reachable reliably.

FROM debian:bullseye-slim

ARG SEAFILE_CLI_VERSION=v9.0.18

# Install dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    jq \
    sqlite3 \
    ca-certificates \
    procps \
    lsb-release \
    && rm -rf /var/lib/apt/lists/*

# Install the upstream Seafile CLI AppImage from GitHub releases.
RUN curl -fsSL -o /usr/local/bin/seaf-cli \
      "https://github.com/haiwen/seafile-client/releases/download/${SEAFILE_CLI_VERSION}/Seafile-cli-x86_64.AppImage" \
    && chmod +x /usr/local/bin/seaf-cli \
        && APPIMAGE_EXTRACT_AND_RUN=1 /usr/local/bin/seaf-cli --help >/tmp/seaf-cli-help.txt 2>&1 \
        && rm -f /tmp/seaf-cli-help.txt

# Create seafile user and directories
RUN useradd -m -u 1000 seafuser \
    && mkdir -p /seafile-data /seafile-client /home/seafuser/.ccnet \
    && chown -R seafuser:seafuser /seafile-data /seafile-client /home/seafuser

# Copy test scripts
COPY --chown=seafuser:seafuser scripts/ /usr/local/bin/

# Make scripts executable
RUN sed -i 's/\r$//' /usr/local/bin/*.sh 2>/dev/null || true \
    && chmod +x /usr/local/bin/*.sh 2>/dev/null || true

WORKDIR /seafile-client
USER seafuser

# Initialize seafile config on first run
ENV CCNET_CONF_DIR=/home/seafuser/.ccnet
ENV SEAFILE_DATA_DIR=/seafile-data
ENV APPIMAGE_EXTRACT_AND_RUN=1

# Keep container running for interactive testing
CMD ["tail", "-f", "/dev/null"]
