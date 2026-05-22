# Seafile CLI Debug container for protocol reverse engineering
# Includes mitmproxy for HTTP traffic capture

FROM debian:bullseye-slim

ARG SEAFILE_CLI_VERSION=v9.0.18

# Install dependencies including mitmproxy and Python for analysis
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    jq \
    sqlite3 \
    ca-certificates \
    procps \
    lsb-release \
    python3 \
    python3-pip \
    xxd \
    bsdmainutils \
    tcpdump \
    net-tools \
    iproute2 \
    iptables \
    && rm -rf /var/lib/apt/lists/*

# Install mitmproxy for HTTP capture
RUN pip3 install mitmproxy

# Install the upstream Seafile CLI AppImage from GitHub releases.
RUN curl -fsSL -o /usr/local/bin/seaf-cli \
            "https://github.com/haiwen/seafile-client/releases/download/${SEAFILE_CLI_VERSION}/Seafile-cli-x86_64.AppImage" \
        && chmod +x /usr/local/bin/seaf-cli \
        && APPIMAGE_EXTRACT_AND_RUN=1 /usr/local/bin/seaf-cli --help >/tmp/seaf-cli-help.txt 2>&1 \
        && rm -f /tmp/seaf-cli-help.txt

# Create seafile user and directories
RUN useradd -m -u 1000 seafuser \
    && mkdir -p /seafile-data /seafile-client /home/seafuser/.ccnet /captures /mitmproxy \
    && chown -R seafuser:seafuser /seafile-data /seafile-client /home/seafuser /captures /mitmproxy

# Copy capture scripts
COPY --chown=seafuser:seafuser scripts/ /usr/local/bin/

# Make scripts executable
RUN sed -i 's/\r$//' /usr/local/bin/*.sh 2>/dev/null || true \
    && chmod +x /usr/local/bin/*.sh 2>/dev/null || true \
    && chmod +x /usr/local/bin/*.py 2>/dev/null || true

WORKDIR /seafile-client
USER seafuser

# Environment
ENV CCNET_CONF_DIR=/home/seafuser/.ccnet
ENV SEAFILE_DATA_DIR=/seafile-data
ENV CAPTURE_DIR=/captures
ENV APPIMAGE_EXTRACT_AND_RUN=1

# mitmproxy will use this CA
ENV SSL_CERT_FILE=/mitmproxy/mitmproxy-ca-cert.pem
ENV REQUESTS_CA_BUNDLE=/mitmproxy/mitmproxy-ca-cert.pem

# Keep container running
CMD ["tail", "-f", "/dev/null"]
