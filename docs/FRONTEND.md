# SesameFS Frontend Guide

The SesameFS web interface is a React SPA extracted from Seafile Pro (Seahub), modified to work as a standalone application with the SesameFS API.

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Setup and Development](#setup-and-development)
3. [Docker Deployment](#docker-deployment)
4. [Authentication Flow](#authentication-flow)
5. [Key Patterns](#key-patterns)
6. [CSS and Styling](#css-and-styling)
7. [Known Gaps and Future Work](#known-gaps-and-future-work)
8. [Troubleshooting](#troubleshooting)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Browser                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              SesameFS Web UI (React SPA)                  │   │
│  │              http://localhost:3000                        │   │
│  │                                                           │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐       │   │
│  │  │  Login      │  │  Libraries  │  │  Files      │       │   │
│  │  │  Page       │  │  List       │  │  Browser    │       │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘       │   │
│  │                     │                                     │   │
│  │               seafile-api.js (Token Auth)                │   │
│  └────────────────────────────┬─────────────────────────────┘   │
│                               │                                  │
│                               ▼                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              SesameFS Backend (Go)                        │   │
│  │              http://localhost:8080                        │   │
│  │  • /api2/auth-token/ - Authentication                     │   │
│  │  • /api2/repos/ - Libraries                               │   │
│  │  • /api/v2.1/repos/:id/dir/ - Directories                │   │
│  │  • /seafhttp/ - File transfers                            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Data Flow

```
API Response → Dirent Model → React State → Component Render
```

1. **API Call**: `seafileAPI.listDir()` fetches directory contents
2. **Model Creation**: Each item becomes a `new Dirent(item)` object
3. **State Update**: `direntList` state is updated with Dirent objects
4. **Rendering**: Components read from Dirent properties

### Key Modifications from Seahub

1. **Removed Django Integration** - No server-side rendering, no CSRF tokens
2. **Token-Based Auth Only** - Uses `Authorization: Token xyz` header
3. **Standalone HTML** - Static `window.app` config instead of Django templates
4. **Login Page** - Custom login component instead of Django views

---

## Setup and Development

### Prerequisites

- Node.js 18+ (recommended: 22)
- npm or yarn
- SesameFS backend running on port 8080

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
docker-compose up -d sesamefs
# Or run locally:
go run ./cmd/sesamefs serve
```

### Dev Credentials

| Field | Value |
|-------|-------|
| Email | Any `*@sesamefs.local` |
| Password | `dev-token-123` |

Example: `admin@sesamefs.local` / `dev-token-123`

### Project Structure

```
frontend/
├── public/
│   ├── index.html          # Standalone HTML with window.app config
│   └── static/
│       ├── css/
│       │   ├── seahub.css  # Seafile base styles
│       │   ├── sf_font2/   # Icon font (sf2-icon-*)
│       │   └── sf_font3/   # Icon font (sf3-font-*)
│       ├── fontawesome/    # Font Awesome icons
│       └── img/
│           ├── lib/        # Library icons (24/, 48/, 256/)
│           ├── file/       # File type icons (24/, 192/)
│           └── file-locked-32.png  # Lock overlay icon
├── src/
│   ├── app.js              # Main app with auth routing
│   ├── models/
│   │   └── dirent.js       # File/folder data model
│   ├── components/
│   │   ├── dirent-list-view/  # Directory listing components
│   │   └── dialog/            # Modal dialogs (share, rename, etc.)
│   ├── pages/
│   │   ├── login/          # Login page component
│   │   └── lib-content-view/  # Main directory view
│   ├── utils/
│   │   ├── seafile-api.js  # Token-based API client
│   │   ├── utils.js        # Menu builders, helpers
│   │   └── constants.js    # Reads from window.app
│   └── css/                # Component-specific styles
├── config/
│   ├── webpack.config.js   # Build config (no Django)
│   └── paths.js            # Build paths
├── Dockerfile              # Multi-stage build
├── nginx.conf              # SPA routing config
└── docker-entrypoint.sh    # Runtime config injection
```

---

## Docker Deployment

### Build and Run

```bash
# Build frontend image
docker-compose build frontend

# Start frontend
docker-compose up -d frontend
```

### Docker Build Caching Issues

**Problem**: Docker may cache build layers, causing code changes to not appear.

**Symptoms**:
- Code changes don't appear after `docker-compose build frontend`
- Build completes in under 10 seconds (should take ~5 minutes)

**Solution - Force Complete Rebuild**:

```bash
# Stop and remove container and image
docker-compose stop frontend
docker-compose rm -f frontend
docker rmi cool-storage-api-frontend

# Rebuild without cache
docker-compose build --no-cache frontend

# Start
docker-compose up -d frontend
```

### Environment Configuration

The API URL can be configured:

1. **Build-time** (Dockerfile ARG):
   ```dockerfile
   ARG SESAMEFS_API_URL=http://api.example.com
   ```

2. **Runtime** (docker-entrypoint.sh):
   ```javascript
   window.SESAMEFS_API_URL = 'https://api.production.com';
   ```

3. **Default** (public/index.html):
   ```javascript
   window.SESAMEFS_API_URL = window.SESAMEFS_API_URL || 'http://localhost:8080';
   ```

---

## Authentication Flow

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Browser    │     │   Frontend    │     │   Backend     │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │  Visit /            │                     │
        │─────────────────────>                     │
        │                     │                     │
        │          Check localStorage               │
        │          (no token) │                     │
        │                     │                     │
        │  Redirect to /login │                     │
        │<─────────────────────                     │
        │                     │                     │
        │  Enter credentials  │                     │
        │─────────────────────>                     │
        │                     │  POST /api2/auth-token/
        │                     │─────────────────────>
        │                     │  {"token": "xyz"}   │
        │                     │<─────────────────────
        │                     │                     │
        │          Store token in localStorage      │
        │                     │                     │
        │  Redirect to /      │                     │
        │<─────────────────────                     │
        │                     │  GET /api2/repos/   │
        │                     │  Authorization: Token xyz
        │                     │─────────────────────>
        │  Show libraries     │                     │
        │<─────────────────────<─────────────────────
```

### Token Storage

```javascript
// Login and get token
const response = await fetch(`${server}/api2/auth-token/`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
  body: new URLSearchParams({ username, password }),
});
localStorage.setItem(TOKEN_KEY, data.token);

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

---

## Key Patterns

### Dirent Model

The `Dirent` class (`src/models/dirent.js`) parses API response JSON:

```javascript
class Dirent {
  constructor(json) {
    // Always set
    this.name = json.name;
    this.type = json.type;  // "file" or "dir"
    this.mtime = json.mtime;
    this.permission = json.permission;
    this.starred = json.starred || false;

    // File-specific (only when json.type === 'file')
    if (json.type === 'file') {
      this.is_locked = json.is_locked || false;
      this.lock_owner = json.lock_owner || '';
      this.locked_by_me = json.locked_by_me || false;
      this.file_tags = json.file_tags || [];
      // ... more file properties
    }
  }
}
```

**Important**: Properties like `is_locked` are only set for files (inside `if (json.type === 'file')`).

### Adding a New File Property

1. **Backend**: Add field to `Dirent` struct in `internal/api/v2/files.go`:
   ```go
   type Dirent struct {
     MyNewField bool `json:"my_new_field"`
   }
   ```

2. **Backend**: Set the field in `ListDirectoryV21`:
   ```go
   dirent.MyNewField = someValue
   ```

3. **Frontend Model**: Parse in `src/models/dirent.js`:
   ```javascript
   if (json.type === 'file') {
     this.my_new_field = json.my_new_field || false;
   }
   ```

4. **Frontend Render**: Use in component:
   ```jsx
   {dirent.my_new_field && <SomeComponent />}
   ```

### Lock Icon Implementation

**Location**: `src/components/dirent-list-view/dirent-list-item.js`

```jsx
// Build lock icon URL (~line 711)
const lockedImageUrl = `${mediaUrl}img/file-${dirent.is_freezed ? 'freezed-32.svg' : 'locked-32.png'}`;

// Render lock icon (~line 747)
<div className="dir-icon">
  <img src={iconUrl} width="24" alt='' />
  {dirent.is_locked && <img className="locked" src={lockedImageUrl} alt={lockedMessage}/>}
</div>
```

**CSS** (`src/css/dirent-list-item.css`):
```css
.dir-icon {
  position: relative;
  overflow: visible;  /* Important: allows icon to extend outside bounds */
}

.dir-icon .locked {
  position: absolute;
  width: 14px !important;
  height: 14px !important;
  bottom: 0;
  right: 0;
  z-index: 100;
}
```

### Context Menu Building

**Location**: `src/utils/utils.js` → `getFileOperationList()`

```javascript
if (dirent.is_locked) {
  if (dirent.locked_by_me || isRepoOwner || currentRepoInfo.is_admin) {
    list.push(UNLOCK);
  }
} else {
  list.push(LOCK);
}
```

### Dialogs and Modals

**Pattern** (using ShareDialog as example):

1. Create dialog component in `src/components/dialog/`:
   ```jsx
   class ShareDialog extends React.Component {
     render() {
       return (
         <Modal isOpen={true} toggle={this.props.toggleDialog}>
           <ModalHeader toggle={this.props.toggleDialog}>
             {gettext('Share')}
           </ModalHeader>
           <ModalBody>{/* Form content */}</ModalBody>
         </Modal>
       );
     }
   }
   ```

2. Add state in parent:
   ```jsx
   state = { isShareDialogOpen: false };
   toggleShareDialog = () => {
     this.setState({ isShareDialogOpen: !this.state.isShareDialogOpen });
   };
   ```

3. Render conditionally:
   ```jsx
   {this.state.isShareDialogOpen &&
     <ShareDialog toggleDialog={this.toggleShareDialog} {...otherProps} />
   }
   ```

**Key Dialog Files**:
| Dialog | File | Purpose |
|--------|------|---------|
| Share | `dialog/share-dialog.js` | Share files/folders |
| Tags | `dialog/edit-filetag-dialog.js` | Edit file tags |
| Rename | `dialog/rename-dirent.js` | Rename files/folders |
| Move/Copy | `dialog/move-dirent-dialog.js` | Move or copy operations |
| Properties | `dirent-detail/dirent-details.js` | File/folder properties |

---

## CSS and Styling

### CSS Load Order

1. **Bootstrap 4** (`bootstrap/dist/css/bootstrap.min.css`) - Required by reactstrap
2. **Seahub Base Styles** (`public/static/css/seahub.css`) - Seafile link colors, buttons
3. **Icon Fonts** - `sf_font2/`, `sf_font3/`, `fontawesome/`
4. **Component CSS** (`src/css/`) - layout.css, toolbar.css, etc.
5. **SesameFS Overrides** (`src/services/css.css`) - Custom fixes

### Common Style Issues

| Issue | Cause | Fix |
|-------|-------|-----|
| Links not styled | Missing seahub.css | Add `<link>` in index.html |
| Icons missing | Missing font files | Copy sf_font2, sf_font3 folders |
| Dropdowns broken | Missing Bootstrap | Import in app.js |
| Action icons stacked | Bootstrap block styles | Add inline-block override |

---

## Known Gaps and Future Work

### Server-Rendered Pages Needing React Conversion

| Page | Current Route | Priority | Notes |
|------|---------------|----------|-------|
| File History | `/repo/file_revisions/` | High | Need revision list + restore |
| Trash | `/repo/{repo_id}/trash/` | Medium | Need trash listing + restore |
| Snapshot | `/repo/repo_folder_trash/` | Medium | Folder-level trash |

### File History Conversion Approach

1. Create `src/components/dialog/file-history-dialog.js`
2. Use API: `GET /api/v2.1/repos/{repo_id}/file/history/?p={path}`
3. Render revision list with date, author, size, download/restore buttons
4. Implement restore: `PUT /api/v2.1/repos/{repo_id}/file/?p={path}&revert_to={commit_id}`

### Other TODOs

- [ ] Logout button functionality
- [ ] File delete with commits
- [ ] File move/copy
- [ ] Proper error handling
- [ ] Share link functionality
- [ ] File preview support
- [ ] Mobile responsiveness improvements

---

## Troubleshooting

### Login page not showing
- Check browser console for JavaScript errors
- Verify `window.app.config` is defined
- Clear localStorage to reset auth state

### CORS errors
- Ensure SesameFS backend is running with CORS enabled
- Check `AUTH_DEV_MODE=true` for development

### Broken images
- Logo and avatar images need to be in `public/static/img/`
- Check paths in `public/index.html`

### Build errors
- Run `npm ci --legacy-peer-deps` to install dependencies
- Check Node.js version (18+ required)

### Changes not appearing after build
- Force rebuild with `docker rmi cool-storage-api-frontend && docker-compose build --no-cache frontend`
- Hard refresh browser (Ctrl+Shift+R)

---

## API Implementation Status

| Endpoint | Status | Notes |
|----------|--------|-------|
| `POST /api2/auth-token/` | Working | Dev mode accepts configured tokens |
| `GET /api/v2.1/repos/` | Working | Web UI format |
| `GET /api2/repos/` | Working | CLI format |
| `GET /api/v2.1/repos/:id/dir/` | Working | Includes lock status |
| `PUT /api/v2.1/repos/:id/file/` | Working | Lock/unlock operations |
| `POST /api2/repos/:id/upload-link/` | Working | Returns upload URL |
| `GET /api2/repos/:id/file/download-link/` | Working | Returns download URL |
| `DELETE /api2/repos/:id/file/` | Stub | Returns success |
| `GET /api/v2.1/repos/:id/repo-tags/` | Working | Repository tags |
| `GET /api/v2.1/repos/:id/file-tags/` | Working | File tags |
