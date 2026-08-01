# Playwright E2E runner. Uses the official Playwright image (bundles Chromium)
# so both the API-driven specs and the browser-driven login spec can run.
FROM mcr.microsoft.com/playwright:v1.48.2-noble

WORKDIR /e2e

# Install deps first for layer caching.
COPY tests/e2e-localauth/package.json ./
RUN npm install --no-audit --no-fund

# Test sources.
COPY tests/e2e-localauth/ ./

CMD ["npx", "playwright", "test"]
