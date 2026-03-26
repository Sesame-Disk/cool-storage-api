#!/bin/sh
# Inject runtime config into all HTML files
inject_into_all_html() {
  local script="$1"
  find /usr/share/nginx/html -name "index.html" -exec \
    sed -i "s|</head>|${script}</head>|" {} +
}

if [ -n "$SESAMEFS_API_URL" ]; then
  inject_into_all_html "<script>window.SESAMEFS_API_URL='$SESAMEFS_API_URL';</script>"
fi

if [ "$BYPASS_LOGIN" = "true" ]; then
  inject_into_all_html "<script>window.BYPASS_LOGIN=true;</script>"
fi
