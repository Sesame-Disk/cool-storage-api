# Software Versions Reference

**Last Updated**: 2026-01-22

This document tracks the versions of all major dependencies and frameworks used in SesameFS. Always check this file before making assumptions about API compatibility or syntax.

## Frontend

### Core Framework
- **React**: `17.0.0`
- **React DOM**: `17.0.0`
- **React Router**: `@gatsbyjs/reach-router@1.3.9`

### UI Framework
- **Bootstrap**: `4.6.2` ⚠️ **IMPORTANT: Bootstrap 4, NOT Bootstrap 5!**
  - Use `className="close"` with `<span>&times;</span>` for close buttons
  - Use `className="modal"` for modals (not `className="btn-close"`)
- **Reactstrap**: `8.9.0` (Bootstrap 4 compatible)

### Key Libraries
- **seafile-js**: `0.2.232` (Seafile API client - hardcoded endpoints, cannot modify)
- **moment**: `2.22.2` (date formatting)
- **i18next**: `17.0.13` (internationalization)
- **react-select**: `5.7.0`
- **uuid**: `9.0.1`
- **crypto-js**: `4.2.0`
- **copy-to-clipboard**: `3.0.8`

### Seafile-specific
- **@seafile/seafile-editor**: `1.0.99`
- **@seafile/sdoc-editor**: `0.5.64`
- **@seafile/resumablejs**: `1.1.16`
- **@seafile/seafile-calendar**: `0.0.12`

### Build Tools
- **Node.js**: (check `node --version` in Docker container)
- **npm**: (check `npm --version` in Docker container)
- **webpack**: (via react-scripts build system)
- **react-scripts**: (check package.json for version)

## Backend (Go)

### Core
- **Go**: `1.25.12` (`go.mod`; build image `golang:1.25.12-trixie`)
- **Gin**: `v1.11.0` (`go.mod`) — this project uses Gin, not Echo

### Database
- **Cassandra**: `5.0.9` (target; Compose files still require exact image/digest pinning before acceptance)
- **gocql driver**: (check go.mod)

### Storage
- **MinIO**: (Docker image version in docker-compose.yaml)
- **AWS SDK for Go v2**: core `v1.41.5`; S3 `v1.97.3` (`go.mod`)

### Crypto
- **bcrypt**: `golang.org/x/crypto/bcrypt`
- **Argon2**: `golang.org/x/crypto/argon2`
- **PBKDF2**: `golang.org/x/crypto/pbkdf2`

## Docker

### Base Images
- **Frontend**: `node:22-bookworm` → `nginx:stable-alpine` (multi-stage build, `frontend/Dockerfile`)
- **Backend**: `golang:1.25.12-trixie` → `debian:trixie-slim` (multi-stage build, `Dockerfile`)
- **Cassandra**: `cassandra:5.0.9` (pin the exact digest for production/acceptance)
- **MinIO**: (check docker-compose.yaml)

## Common Version Mismatch Issues

### Bootstrap 4 vs 5
**WRONG (Bootstrap 5)**:
```jsx
<button className="btn-close" onClick={close}></button>
```

**CORRECT (Bootstrap 4)**:
```jsx
<button className="close" onClick={close}>
  <span aria-hidden="true">&times;</span>
</button>
```

### React 17 vs 18
- No automatic batching (React 18 feature)
- No `createRoot` API (React 18)
- Use `ReactDOM.render()` not `ReactDOM.createRoot()`

### Reactstrap 8 Modal Rendering
- **Issue**: Reactstrap `<Modal>` component does NOT work inside `<ModalPortal>` (double portal issue)
- **Solution**: Use plain Bootstrap 4 modal classes instead:
```jsx
<div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
  <div className="modal-dialog modal-dialog-centered">
    <div className="modal-content">
      <div className="modal-header">
        <h5 className="modal-title">Title</h5>
        <button type="button" className="close" onClick={toggle}>
          <span aria-hidden="true">&times;</span>
        </button>
      </div>
      <div className="modal-body">Content</div>
      <div className="modal-footer">
        <Button color="primary">Action</Button>
      </div>
    </div>
  </div>
</div>
```

## Version Checking Commands

```bash
# Frontend versions
cd frontend && npm list react react-dom bootstrap reactstrap

# Backend versions
go list -m all | grep -E "gin-gonic|gocql|aws-sdk-go-v2"

# Docker image versions
docker-compose images

# Node/npm in container
docker exec cool-storage-api-frontend-1 node --version
docker exec cool-storage-api-frontend-1 npm --version
```

## Update Policy

- Frontend dependencies are pinned to exact versions (from Seafile upstream)
- Backend Go modules use semantic versioning
- Docker images use specific tags (avoid `:latest` in production)

## References

- Bootstrap 4 Documentation: https://getbootstrap.com/docs/4.6/
- Reactstrap 8 Documentation: https://reactstrap.github.io/?path=/story/home-installation--page
- React 17 Documentation: https://17.reactjs.org/
- Seafile API: https://seafile-api.readme.io/
