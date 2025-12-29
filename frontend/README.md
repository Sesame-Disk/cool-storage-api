# SesameFS Web UI

React-based web interface for SesameFS, extracted from Seafile Pro (Seahub).

## Quick Start

```bash
# Install dependencies
npm ci --legacy-peer-deps

# Start development server
npm start

# Make sure SesameFS backend is running on http://localhost:8080
```

## Development Credentials

| Field | Value |
|-------|-------|
| Email | `admin@sesamefs.local` |
| Password | `dev-token-123` |

## Build

```bash
# Production build
npm run build

# Output in ./build directory
```

## Docker

```bash
# Build image
docker build -t sesamefs-frontend .

# Run container
docker run -p 3000:80 -e SESAMEFS_API_URL=http://localhost:8080 sesamefs-frontend
```

## Configuration

API URL is configured via `window.SESAMEFS_API_URL`:

- **Default:** `http://localhost:8080` (set in `public/index.html`)
- **Docker:** Set via `SESAMEFS_API_URL` environment variable
- **Runtime:** Injected by `docker-entrypoint.sh`

## Key Files

| File | Purpose |
|------|---------|
| `src/app.js` | Main app with auth routing |
| `src/pages/login/` | Login page component |
| `src/utils/seafile-api.js` | Token-based API client |
| `public/index.html` | Configuration injection |
| `config/webpack.config.js` | Build configuration |

## Documentation

See [docs/FRONTEND-SETUP.md](../docs/FRONTEND-SETUP.md) for detailed setup guide.
