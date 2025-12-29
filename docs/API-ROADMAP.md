# API Roadmap: Pending Endpoints

This document tracks the Seafile-compatible API endpoints that need to be implemented for feature parity with Seafile's core functionality.

## Implementation Status Legend

| Status | Meaning |
|--------|---------|
| ✅ | Fully implemented and tested |
| ⚠️ | Stub exists (route defined, returns success, but no backend logic) |
| ❌ | Not implemented |

---

## Current Implementation Status

### Sync Protocol (`/seafhttp/`) - ✅ Complete

These endpoints enable Seafile Desktop client synchronization:

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/seafhttp/protocol-version` | GET | ✅ | Returns `{"version": 2}` |
| `/seafhttp/repo/:id/permission-check/` | GET | ✅ | Returns empty 200 |
| `/seafhttp/repo/:id/quota-check/` | GET | ✅ | Returns quota info |
| `/seafhttp/repo/:id/commit/HEAD` | GET/PUT | ✅ | Get/update HEAD |
| `/seafhttp/repo/:id/commit/:cid` | GET/PUT | ✅ | Get/store commit |
| `/seafhttp/repo/:id/block/:bid` | GET/PUT | ✅ | Download/upload block |
| `/seafhttp/repo/:id/check-blocks/` | POST | ✅ | Check block existence |
| `/seafhttp/repo/:id/fs/:fsid` | GET | ✅ | Get FS object |
| `/seafhttp/repo/:id/fs-id-list/` | GET | ✅ | List FS IDs (JSON array) |
| `/seafhttp/repo/:id/recv-fs/` | POST | ✅ | Receive FS objects (binary) |
| `/seafhttp/repo/:id/check-fs/` | POST | ✅ | Check FS object existence |
| `/seafhttp/repo/:id/pack-fs/` | POST | ✅ | Pack multiple FS objects |
| `/seafhttp/repo/head-commits-multi` | POST | ✅ | Multi-repo head check |

### Libraries (`/api2/repos/`) - ✅ Complete

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api2/repos/` | GET | ✅ | List libraries |
| `/api2/repos/` | POST | ✅ | Create library |
| `/api2/repos/:id/` | GET | ✅ | Get library info |
| `/api2/repos/:id/` | DELETE | ✅ | Delete library |
| `/api2/repos/:id/download-info/` | GET | ✅ | Sync info for desktop |

### Authentication - ✅ Basic Complete

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api2/auth-token/` | POST | ✅ | Login (dev mode) |
| `/api2/account/info/` | GET | ✅ | User info |
| `/api2/server-info/` | GET | ✅ | Server capabilities |

---

## Phase 1: Core File Operations

**Priority: HIGH**
**Prerequisite for: Web UI development**

These endpoints are essential for basic file management through a web interface.

### File Metadata & Info

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/detail/` | GET | ❌ | Get file metadata (size, mtime, modifier, etc.) |

**Implementation notes:**
- Query `fs_objects` table by path
- Return: `id`, `name`, `size`, `mtime`, `type`, `modifier_email`, `modifier_name`
- Used by web UI to show file properties

### File CRUD Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/` | GET | ⚠️ | Get file info (currently returns 404) |
| `/api2/repos/:id/file/` | DELETE | ⚠️ | Delete file (stub, needs backend) |
| `/api2/repos/:id/file/` | POST | ❌ | Create/rename/revert file |

**POST operations (via `operation` parameter):**
```
POST /api2/repos/:id/file/?p=/path/to/file
  operation=create     → Create empty file
  operation=rename     → Rename file (needs newname param)
  operation=revert     → Revert to commit (needs commit_id param)
```

**Implementation notes:**
- All operations create a new commit in `commits` table
- Update `fs_objects` with new directory structure
- Update `libraries.head_commit_id`
- For delete: decrement `blocks.ref_count`, don't delete S3 objects immediately

### Directory Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/dir/` | GET | ✅ | List directory contents |
| `/api2/repos/:id/dir/` | POST | ⚠️ | Create directory (stub) |
| `/api2/repos/:id/dir/` | DELETE | ⚠️ | Delete directory (stub) |
| `/api2/repos/:id/dir/detail/` | GET | ❌ | Get directory metadata |

**POST operations (via `operation` parameter):**
```
POST /api2/repos/:id/dir/?p=/path
  operation=mkdir      → Create directory
  operation=rename     → Rename directory (needs newname)
```

**Implementation notes:**
- Directory creation: add entry to parent dir's `fs_objects.dir_entries`
- Directory deletion: recursive - delete all children first
- Create new commit for each operation

### Move & Copy

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/move/` | POST | ⚠️ | Move file (stub) |
| `/api2/repos/:id/file/copy/` | POST | ⚠️ | Copy file (stub) |

**Parameters:**
- `src_repo_id` - Source repository (optional, defaults to current)
- `src_dir` - Source directory path
- `dst_repo_id` - Destination repository
- `dst_dir` - Destination directory path
- `file_names` - JSON array of filenames

**Implementation notes:**
- Copy: duplicate fs_object entries, increment block ref_counts
- Move: update parent directory entries, no block changes
- Cross-repo copy: may need to copy blocks if different org

### Update Link (File Overwrite)

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/update-link/` | GET | ❌ | Get URL to overwrite existing file |

**Implementation notes:**
- Similar to upload-link but for existing files
- Token should store the existing file path
- On upload: create new commit replacing the file

---

## Phase 2: User Features

**Priority: MEDIUM**
**Prerequisite for: User-facing web features**

### Starred Files

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/starredfiles/` | GET | ❌ | List user's starred files |
| `/api2/starredfiles/` | POST | ❌ | Star a file |
| `/api2/starredfiles/` | DELETE | ❌ | Unstar a file |

**New table needed:**
```cql
CREATE TABLE starred_files (
    user_id UUID,
    repo_id UUID,
    path TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id, path)
);
```

### Trash / Recycle Bin

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/trash/` | GET | ❌ | List deleted files |
| `/api2/repos/:id/trash/` | DELETE | ❌ | Empty trash |
| `/api2/repos/:id/trash/revert/` | POST | ❌ | Restore file from trash |

**Implementation notes:**
- Trash is derived from commit history
- "Deleted" = file exists in older commit but not in HEAD
- Restore = create new commit adding file back
- Empty trash = mark commits as purgeable (don't actually delete yet)

### File History & Revisions

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/history/` | GET | ❌ | List file's revision history |
| `/api2/repos/:id/file/revision/` | GET | ❌ | Download specific revision |

**Query parameters:**
- `p` - File path
- `commit_id` - Specific commit (for revision download)

**Implementation notes:**
- Walk commit history backwards
- For each commit, check if file exists and get its fs_id
- Return list of commits where file changed

---

## Phase 3: Productivity Features

**Priority: MEDIUM**
**Prerequisite for: Collaborative editing (OnlyOffice integration)**

### File Locking

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/` | PUT | ❌ | Lock/unlock file |

**PUT operations (via `operation` parameter):**
```
PUT /api2/repos/:id/file/?p=/path
  operation=lock       → Lock file for editing
  operation=unlock     → Release lock
```

**New table needed:**
```cql
CREATE TABLE file_locks (
    repo_id UUID,
    path TEXT,
    locked_by UUID,
    locked_at TIMESTAMP,
    expires_at TIMESTAMP,
    PRIMARY KEY ((repo_id), path)
) WITH default_time_to_live = 86400;  -- 24 hour TTL
```

**Implementation notes:**
- Check lock before allowing upload/update
- Auto-expire locks after 24 hours
- OnlyOffice integration will require this

### Batch Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/batch-copy-item/` | POST | ❌ | Copy multiple files |
| `/api2/repos/batch-move-item/` | POST | ❌ | Move multiple files |
| `/api2/repos/batch-delete-item/` | POST | ❌ | Delete multiple files |

**Request body:**
```json
{
  "src_repo_id": "xxx",
  "src_parent_dir": "/",
  "src_dirents": ["file1.txt", "folder1"],
  "dst_repo_id": "yyy",
  "dst_parent_dir": "/backup"
}
```

**Implementation notes:**
- Wrap individual operations in a single commit
- Return operation ID for async operations
- Consider background job for large batch operations

### Activities & Events

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/events/` | GET | ❌ | User's activity feed |
| `/api2/repo-history/:id/` | GET | ❌ | Library change history |

**New table needed:**
```cql
CREATE TABLE activities (
    org_id UUID,
    activity_id TIMEUUID,
    user_id UUID,
    repo_id UUID,
    path TEXT,
    op_type TEXT,  -- 'create', 'update', 'delete', 'move', 'rename'
    details MAP<TEXT, TEXT>,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), activity_id)
) WITH CLUSTERING ORDER BY (activity_id DESC);
```

---

## Phase 4: Advanced Features

**Priority: LOW**
**Prerequisite for: Feature parity with Seafile Pro**

### Search

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/search/` | GET | ❌ | Search files by name/content |

**Implementation notes:**
- Phase 4a: Filename search (query fs_objects)
- Phase 4b: Full-text search (requires Elasticsearch)
- Consider using Cassandra's SASI indexes for basic search

### Thumbnails & Preview

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/thumbnail/` | GET | ❌ | Get image thumbnail |
| `/api2/repos/:id/file/preview/` | GET | ❌ | Preview document |

**Implementation notes:**
- Generate thumbnails on upload (background job)
- Store in separate S3 bucket/prefix
- Cache with CDN for performance
- Preview requires document conversion (LibreOffice/OnlyOffice)

### File Comments

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/comments/` | GET | ❌ | List comments on file |
| `/api2/repos/:id/file/comments/` | POST | ❌ | Add comment |
| `/api2/repos/:id/file/comments/:id/` | PUT | ❌ | Update comment |
| `/api2/repos/:id/file/comments/:id/` | DELETE | ❌ | Delete comment |

### File Tags

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file-tags/` | GET | ❌ | List tags on file |
| `/api2/repos/:id/file-tags/` | POST | ❌ | Add tag |
| `/api2/repos/:id/file-tags/:id/` | DELETE | ❌ | Remove tag |

### Folder Permissions (Seafile Pro feature)

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/dir/shared_items/` | GET | ❌ | List folder shares |
| `/api2/repos/:id/dir/shared_items/` | PUT | ❌ | Share folder |
| `/api2/repos/:id/dir/shared_items/` | DELETE | ❌ | Unshare folder |

---

## Implementation Guidelines

### Creating New Commits

All file/directory modifications should:

1. Create a new root FS object with updated structure
2. Create a new commit pointing to the new root
3. Update `libraries.head_commit_id`

```go
func (h *Handler) createCommit(ctx context.Context, repoID, parentCommitID, rootFSID, description string) error {
    commitID := generateCommitID()

    // Insert commit
    h.db.Session().Query(`
        INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, description, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, repoID, commitID, parentCommitID, rootFSID, description, time.Now()).Exec()

    // Update library HEAD
    h.db.Session().Query(`
        UPDATE libraries SET head_commit_id = ?, updated_at = ?
        WHERE org_id = ? AND library_id = ?
    `, commitID, time.Now(), orgID, repoID).Exec()

    return nil
}
```

### File Path Handling

- Always normalize paths: start with `/`, no trailing `/`
- Handle both URL-encoded and decoded paths
- Seafile uses `p` query parameter for paths

### Error Responses

Follow Seafile's error format:
```json
{"error_msg": "File not found"}
```

Or for validation errors:
```json
{"error": "path is required"}
```

---

## Database Schema Additions

The following tables need to be added for full feature support:

```cql
-- Starred files
CREATE TABLE starred_files (
    user_id UUID,
    repo_id UUID,
    path TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id, path)
);

-- File locks
CREATE TABLE file_locks (
    repo_id UUID,
    path TEXT,
    locked_by UUID,
    locked_at TIMESTAMP,
    expires_at TIMESTAMP,
    PRIMARY KEY ((repo_id), path)
) WITH default_time_to_live = 86400;

-- Activities
CREATE TABLE activities (
    org_id UUID,
    activity_id TIMEUUID,
    user_id UUID,
    repo_id UUID,
    path TEXT,
    op_type TEXT,
    old_path TEXT,
    details MAP<TEXT, TEXT>,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), activity_id)
) WITH CLUSTERING ORDER BY (activity_id DESC);

-- File comments
CREATE TABLE file_comments (
    repo_id UUID,
    path TEXT,
    comment_id TIMEUUID,
    user_id UUID,
    content TEXT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    PRIMARY KEY ((repo_id, path), comment_id)
) WITH CLUSTERING ORDER BY (comment_id DESC);

-- File tags
CREATE TABLE file_tags (
    repo_id UUID,
    tag_id UUID,
    name TEXT,
    color TEXT,
    PRIMARY KEY ((repo_id), tag_id)
);

CREATE TABLE file_tag_mappings (
    repo_id UUID,
    path TEXT,
    tag_id UUID,
    PRIMARY KEY ((repo_id, path), tag_id)
);
```

---

## Phase 5: Frontend Modernization & Responsiveness

**Priority: LOW (Post-Launch)**
**Prerequisite for: Mobile-friendly web interface**

The current frontend is extracted from Seahub and uses Bootstrap 4 with limited mobile responsiveness. This phase focuses on modernizing the CSS architecture and making the UI fully responsive.

### Recommended Approach: CSS Framework Migration

**Target: Tailwind CSS**

Tailwind CSS is recommended because:
- Utility-first approach works well with existing React components
- Excellent responsive design utilities built-in
- Smaller bundle size with PurgeCSS
- Easy to gradually migrate (can coexist with existing CSS)
- Active community and ecosystem

### Alternative Options Considered

| Option | Effort | Pros | Cons |
|--------|--------|------|------|
| **Tailwind CSS** | 4-6 weeks | Modern, utility-first, great DX | Learning curve, migration effort |
| **CSS-Only Fixes** | 2 weeks | Quick, minimal changes | Limited improvement, technical debt |
| **Bootstrap 5 Upgrade** | 3-4 weeks | Familiar, drop-in upgrade | Still not mobile-first |
| **Ant Design** | 6-8 weeks | Full component library | Heavy, opinionated |
| **Separate Mobile UI** | 2-3 months | Best UX | Two codebases |

### Migration Strategy

#### Phase 5.1: Setup & Infrastructure
| Task | Description |
|------|-------------|
| Install Tailwind CSS | Add to build pipeline with PostCSS |
| Configure PurgeCSS | Optimize for production bundle size |
| Create base config | Define color palette, breakpoints, spacing |
| Setup dark mode | Optional: Add dark theme support |

#### Phase 5.2: Core Layout Components
| Component | Current | Target |
|-----------|---------|--------|
| Side Panel | Fixed width | Collapsible, responsive |
| Main Panel | Flex layout | Responsive grid |
| Header/Toolbar | Desktop-only | Mobile-friendly with hamburger |
| Modals/Dialogs | Fixed size | Responsive, mobile sheets |

#### Phase 5.3: Data Components
| Component | Current | Target |
|-----------|---------|--------|
| File List Table | `<table>` | Responsive grid/cards on mobile |
| Library List | Table rows | Cards on mobile |
| Breadcrumb | Overflow hidden | Scrollable, collapsible |
| Action Menus | Dropdown | Bottom sheet on mobile |

#### Phase 5.4: Forms & Inputs
| Component | Current | Target |
|-----------|---------|--------|
| Login Form | Basic | Touch-friendly, larger targets |
| Share Dialog | Complex form | Step-by-step wizard on mobile |
| Upload UI | Drag-drop only | Mobile file picker |
| Search | Desktop search bar | Expandable mobile search |

### Responsive Breakpoints

```javascript
// Tailwind default breakpoints (recommended)
screens: {
  'sm': '640px',   // Mobile landscape
  'md': '768px',   // Tablets
  'lg': '1024px',  // Desktop
  'xl': '1280px',  // Large desktop
  '2xl': '1536px', // Extra large
}
```

### Mobile-First Patterns to Implement

1. **Navigation**
   - Hamburger menu on mobile
   - Slide-out side panel
   - Bottom navigation bar (optional)

2. **File Browser**
   - Card view default on mobile
   - Swipe actions (delete, share)
   - Long-press for context menu
   - Pull-to-refresh

3. **File Operations**
   - Bottom action sheet instead of dropdown
   - Full-screen dialogs on mobile
   - Touch-friendly selection (checkboxes)

4. **Upload/Download**
   - Native file picker integration
   - Progress in notification area
   - Background upload support

### Files to Modify

| File | Changes |
|------|---------|
| `package.json` | Add tailwindcss, postcss, autoprefixer |
| `tailwind.config.js` | Create configuration |
| `postcss.config.js` | Add Tailwind plugin |
| `src/index.css` | Add Tailwind directives |
| `src/app.js` | Remove Bootstrap import |
| `src/components/*` | Migrate to Tailwind classes |
| `src/css/*` | Gradually deprecate |

### Estimated Timeline

| Phase | Duration | Dependencies |
|-------|----------|--------------|
| 5.1 Setup | 1 week | None |
| 5.2 Core Layout | 2 weeks | 5.1 |
| 5.3 Data Components | 2 weeks | 5.2 |
| 5.4 Forms | 1 week | 5.2 |
| Testing & Polish | 1 week | All |
| **Total** | **~6-7 weeks** | |

### Success Criteria

- [ ] UI works on mobile devices (320px+)
- [ ] Touch-friendly interactions
- [ ] No horizontal scrolling on mobile
- [ ] Lighthouse mobile score > 80
- [ ] File upload works on mobile browsers
- [ ] All features accessible on mobile

### Notes

- This phase should be started **after** core API features are complete
- Seafile mobile apps (iOS/Android) can be used as interim mobile solution
- Consider A/B testing new responsive design before full rollout
- May want to keep "desktop mode" toggle for power users

---

## References

- [Seafile API Reference](https://seafile-api.readme.io/)
- [Seafile Admin Manual](https://manual.seafile.com/12.0/develop/web_api_v2.1/)
- [Current Implementation](../internal/api/v2/files.go)
- [Tailwind CSS Documentation](https://tailwindcss.com/docs)
- [Tailwind UI Components](https://tailwindui.com/) (paid, but good reference)
