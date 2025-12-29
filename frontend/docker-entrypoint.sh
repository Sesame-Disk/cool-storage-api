#!/bin/sh
set -e

# Inject runtime configuration into index.html
# This allows SESAMEFS_API_URL to be set at container runtime

if [ -n "$SESAMEFS_API_URL" ]; then
    echo "Configuring SESAMEFS_API_URL: $SESAMEFS_API_URL"

    # Inject the API URL into index.html before the closing </head> tag
    sed -i "s|</head>|<script>window.SESAMEFS_API_URL='$SESAMEFS_API_URL';</script></head>|" /usr/share/nginx/html/index.html
fi

# Execute the main command
exec "$@"
