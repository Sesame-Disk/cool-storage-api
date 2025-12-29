# SesameFS Web UI - Frontend Setup Guide

This document explains how the SesameFS Web UI was extracted from Seafile Pro (Seahub) and adapted to work as a standalone SPA with the SesameFS API.

## Overview

The frontend is a React-based SPA extracted from Seahub (Seafile Pro 11.0.16). It has been modified to:
- Remove Django dependencies (no server-side rendering)
- Use token-based authentication only (no CSRF)
- Work as a standalone application
- Connect to SesameFS API endpoints

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Browser                                  │
│                            │                                     │
│                            ▼                                     │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              SesameFS Web UI (React SPA)                  │   │
│  │              http://localhost:3000                        │   │
│  │                                                           │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐       │   │
│  │  │  Login      │  │  Libraries  │  │  Files      │       │   │
│  │  │  Page       │  │  List       │  │  Browser    │       │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘       │   │
│  │              │              │              │               │   │
│  │              └──────────────┴──────────────┘               │   │
│  │                            │                               │   │
│  │                     seafile-api.js                         │   │
│  │                     (Token Auth)                           │   │
│  └────────────────────────────┬─────────────────────────────┘   │
│                               │                                  │
│                               ▼                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              SesameFS Backend (Go)                        │   │
│  │              http://localhost:8080                        │   │
│  │                                                           │   │
│  │  • /api2/auth-token/ - Authentication                     │   │
│  │  • /api2/repos/ - Libraries                               │   │
│  │  • /api2/repos/:id/dir/ - Directories                     │   │
│  │  • /seafhttp/ - File transfers                            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Key Modifications from Seahub

### 1. Removed Django Integration

**Files modified:**
- `config/webpack.config.js` - Removed `webpack-bundle-tracker`, enabled `HtmlWebpackPlugin` and `InterpolateHtmlPlugin`
- `config/paths.js` - Changed build output from Django static to standalone `/build`

**Before (Seahub):**
```javascript
// Django integration for asset tracking
const BundleTracker = require('webpack-bundle-tracker');
new BundleTracker({ filename: './webpack-stats.json' });
```

**After (SesameFS):**
```javascript
// Standalone SPA build
new HtmlWebpackPlugin({
  inject: true,
  template: paths.appHtml,
});
```

### 2. Token-Based Authentication Only

**File modified:** `src/utils/seafile-api.js`

Seahub supports two auth modes:
1. **CSRF mode** - Django sessions with CSRF tokens injected via templates
2. **Token mode** - `Authorization: Token xyz` header

We use token mode exclusively since there's no Django to inject CSRF tokens.

**Key functions:**
```javascript
// Login and get token
async function login(username, password) {
  const response = await fetch(`${server}/api2/auth-token/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ username, password }),
  });
  // Store token in localStorage
  localStorage.setItem(TOKEN_KEY, data.token);
}

// Check authentication
function isAuthenticated() {
  return !!localStorage.getItem(TOKEN_KEY);
}

// Logout
function logout() {
  localStorage.removeItem(TOKEN_KEY);
  window.location.href = '/login/';
}
```

### 3. Standalone HTML Template

**File created:** `public/index.html`

Replaces Django template variable injection with static `window.app` config:

```html
<script>
  window.SESAMEFS_API_URL = window.SESAMEFS_API_URL || 'http://localhost:8080';
  window.app = {
    config: {
      siteRoot: '/',
      serviceURL: window.SESAMEFS_API_URL,
      fileServerRoot: window.SESAMEFS_API_URL + '/seafhttp',
      siteTitle: 'SesameFS',
      // ... other config
    },
    pageOptions: {
      canAddRepo: true,
      canGenerateShareLink: true,
      // ... user permissions
    }
  };
</script>
```

### 4. Login Page Integration

**Files created:**
- `src/pages/login/index.js` - Login form component
- `src/pages/login/login.css` - Login page styles

**File modified:** `src/app.js`

Added authentication flow:
```javascript
import { isAuthenticated } from './utils/seafile-api';
import LoginPage from './pages/login';

// In componentDidMount:
const loggedIn = isAuthenticated();
if (!loggedIn && !isLoginPage) {
  window.location.href = '/login/';
  return;
}

// In render:
if (!isLoggedIn) {
  return <LoginPage />;
}
```

## Development Setup

### Prerequisites
- Node.js 18+ (recommended: 22)
- npm or yarn

### Local Development

```bash
# Navigate to frontend directory
cd frontend

# Install dependencies
npm ci --legacy-peer-deps

# Start development server (hot reload)
npm start
# Opens http://localhost:3001

# Make sure SesameFS backend is running
# In another terminal:
cd ..
go run ./cmd/sesamefs/main.go
# Runs on http://localhost:8080

# Build for production
npm run build
# Output in ./build
```

### Environment Configuration

The API URL can be configured in several ways:

1. **Build-time** (Dockerfile ARG):
   ```dockerfile
   ARG SESAMEFS_API_URL=http://api.example.com
   ```

2. **Runtime** (nginx injection):
   ```javascript
   // docker-entrypoint.sh injects this
   window.SESAMEFS_API_URL = 'https://api.production.com';
   ```

3. **Default** (public/index.html):
   ```javascript
   window.SESAMEFS_API_URL = window.SESAMEFS_API_URL || 'http://localhost:8080';
   ```

## Docker Deployment

### Build Image

```bash
docker build -t sesamefs-frontend ./frontend
```

### Docker Compose

```yaml
frontend:
  build:
    context: ./frontend
    dockerfile: Dockerfile
    args:
      - SESAMEFS_API_URL=http://api.sesamefs.com
  ports:
    - "3000:80"
  environment:
    - SESAMEFS_API_URL=http://api.sesamefs.com
```

### Nginx Configuration

The frontend uses nginx to serve static files and handle SPA routing:

```nginx
server {
    listen 80;
    root /usr/share/nginx/html;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location /static {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

## Authentication Flow

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Browser    │     │   Frontend    │     │   Backend     │
│               │     │   (React)     │     │   (Go API)    │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │                     │
        │  Visit /            │                     │
        │─────────────────────>                     │
        │                     │                     │
        │          Check localStorage               │
        │          (no token)                       │
        │                     │                     │
        │  Redirect to /login │                     │
        │<─────────────────────                     │
        │                     │                     │
        │  Enter credentials  │                     │
        │─────────────────────>                     │
        │                     │                     │
        │                     │  POST /api2/auth-token/
        │                     │─────────────────────>
        │                     │                     │
        │                     │  {"token": "xyz"}   │
        │                     │<─────────────────────
        │                     │                     │
        │          Store token in localStorage      │
        │                     │                     │
        │  Redirect to /      │                     │
        │<─────────────────────                     │
        │                     │                     │
        │                     │  GET /api2/repos/   │
        │                     │  Authorization: Token xyz
        │                     │─────────────────────>
        │                     │                     │
        │                     │  [libraries...]     │
        │                     │<─────────────────────
        │                     │                     │
        │  Show libraries     │                     │
        │<─────────────────────                     │
```

## Dev Credentials

For local development with SesameFS in dev mode:

| Field | Value |
|-------|-------|
| Email | Any `*@sesamefs.local` |
| Password | `dev-token-123` |

Example: `admin@sesamefs.local` / `dev-token-123`

## CORS Configuration

The SesameFS backend includes CORS middleware for frontend access:

```go
corsConfig := cors.Config{
    AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
    AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
    AllowCredentials: true,
    AllowAllOrigins:  true, // In dev mode
}
```

## Troubleshooting

### Login page not showing
- Check browser console for JavaScript errors
- Verify `window.app.config` is defined
- Clear localStorage to reset auth state

### CORS errors
- Ensure SesameFS backend is running with CORS enabled
- Check that `AUTH_DEV_MODE=true` for development

### Broken images
- Logo and avatar images need to be added to `public/static/img/`
- Or update paths in `public/index.html`

### Build errors
- Run `npm ci --legacy-peer-deps` to install dependencies
- Check Node.js version (18+ required)

## File Structure

```
frontend/
├── public/
│   └── index.html          # Standalone HTML with window.app config
├── src/
│   ├── app.js              # Main app with auth routing
│   ├── pages/
│   │   └── login/          # Login page component
│   └── utils/
│       ├── seafile-api.js  # Token-based API client
│       └── constants.js    # Reads from window.app
├── config/
│   ├── webpack.config.js   # Build config (no Django)
│   └── paths.js            # Build paths
├── Dockerfile              # Multi-stage build
├── nginx.conf              # SPA routing config
├── docker-entrypoint.sh    # Runtime config injection
├── .env.example            # Environment template
└── .eslintrc.js            # ESLint config
```

## Future Improvements

- [ ] Add proper logo and branding images
- [ ] Implement proper user info display after login
- [ ] Add logout button functionality
- [ ] Style improvements for login page
- [ ] Add loading states and error handling
- [ ] Implement share link functionality
- [ ] Add file preview support
