# SesameFS Web UI

React-based web interface for SesameFS, extracted from Seafile Pro (Seahub).

## Quick Start

```bash
# From the repository root, start backend + frontend nginx
docker compose up -d sesamefs frontend

# Open http://localhost:3000
```

## Development Credentials

| Field | Value |
|-------|-------|
| Username | `00000000-0000-0000-0000-000000000001` or `00000000-0000-0000-0000-000000000001@sesamefs.local` |
| Password | Any value (dev mode accepts any password) |
| Token | `dev-token-123` (can use directly in API calls) |

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
docker run -p 3000:80 sesamefs-frontend
```

## Configuration

The desktop frontend now assumes same-origin routing:

- **Production:** nginx routes `/`, `/sys/`, and `/org/` to the frontend container and proxies API/file routes to the Go backend on the same host.
- **Docker-based local development:** the frontend nginx container proxies API/file routes to the backend, so same-origin requests still work.
- **No desktop runtime API injection:** the old `SESAMEFS_API_URL` + `docker-entrypoint.sh` path is no longer used by the desktop frontend container.

Production note:
The nginx inside the frontend container may preserve `X-Real-IP` and `X-Forwarded-For` values already canonicalized by an upstream reverse proxy. That is only safe when this frontend container is reachable exclusively from trusted internal network paths, not directly from arbitrary external clients.

## CSS & Styling

The frontend uses multiple CSS sources for styling:

| CSS File | Purpose |
|----------|---------|
| `bootstrap/dist/css/bootstrap.min.css` | Base UI framework (required by reactstrap) |
| `public/static/css/seahub.css` | Seafile base styles (links, buttons, layout) |
| `public/static/css/sf_font2/seafile-font2.css` | Seafile icon font (sf2-icon-*) |
| `public/static/css/sf_font3/iconfont.css` | Seafile icon font (sf3-font-*) |
| `public/static/fontawesome/css/fontawesome-all.min.css` | Font Awesome icons |
| `src/services/css.css` | SesameFS custom overrides |

### Custom Overrides

The `src/services/css.css` file contains SesameFS-specific fixes:
- Sidebar header sizing (Files, Tools)
- Action icon inline layout
- Dropdown menu positioning

## Static Assets

Assets copied from Seahub source:

| Directory | Contents |
|-----------|----------|
| `public/static/css/sf_font2/` | Seafile icon font files |
| `public/static/css/sf_font3/` | Additional icon font files |
| `public/static/fontawesome/` | Font Awesome icons |
| `public/static/img/lib/` | Library type icons (24/, 48/, 256/) |
| `public/static/img/file/` | File type icons (24/, 192/) |

## Key Files

| File | Purpose |
|------|---------|
| `src/app.js` | Main app with auth routing, CSS imports |
| `src/pages/login/` | Login page component |
| `src/utils/seafile-api.js` | Token-based API client |
| `public/index.html` | Configuration injection, CSS links |
| `config/webpack.config.js` | Build configuration |

## API Compatibility

The frontend expects these SesameFS API endpoints:

| Endpoint | Purpose |
|----------|---------|
| `POST /api2/auth-token/` | Login, get auth token |
| `GET /api/v2.1/repos/` | List libraries (web UI format) |
| `GET /api2/repos/` | List libraries (CLI format) |
| `GET /api2/repos/:id/dir/` | List directory contents |
| `GET /api2/repos/:id/upload-link/` | Get upload URL |
| `GET /api2/repos/:id/file/download-link/` | Get download URL |

## Responsiveness

The current UI has limited mobile responsiveness (inherited from Seahub).
Mobile support uses `react-responsive` MediaQuery for different layouts.

## Documentation

See [docs/FRONTEND.md](../docs/FRONTEND.md) for detailed setup guide.
