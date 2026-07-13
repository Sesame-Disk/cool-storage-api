import { serviceURL } from './config';

const TOKEN_KEY = 'seahub_token';

export function getAuthToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setAuthToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearAuthToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

export function authHeaders(): Record<string, string> {
  const token = getAuthToken();
  return {
    'Authorization': `Token ${token}`,
    'Accept': 'application/json',
  };
}

/** Remove a URL from the service-worker API cache so the next fetch hits the network. */
export async function invalidateApiCache(path: string): Promise<void> {
  try {
    const cache = await caches.open('sesamefs-api-v1');
    const keys = await cache.keys();
    for (const req of keys) {
      if (new URL(req.url).pathname.startsWith(path)) {
        await cache.delete(req);
      }
    }
  } catch {
    // caches API may not be available — safe to ignore
  }
}

export async function login(email: string, password: string): Promise<string> {
  const res = await fetch(`${serviceURL()}/api2/auth-token/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ username: email, password }),
  });

  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.non_field_errors?.[0] || 'Login failed');
  }

  const data = await res.json();
  const token: string = data.token;
  setAuthToken(token);
  return token;
}

// --------------------------- Local auth (sesameauth) ---------------------------
// Unified auth (E11): the mobile login mirrors the web. Which methods are shown
// is advertised by GET /api/v2.1/auth/methods; local username/password login is
// handled by the optional sesameauth service, proxied same-origin by nginx.

export interface AuthMethods {
  local: boolean;
  oidc: boolean;
}

/**
 * Discover which auth methods are enabled so the login UI can render the right
 * options. Resolves to `{ local: false, oidc: false }` when the endpoint is
 * unavailable (e.g. sesameauth not running → 502), so callers can fall back to
 * the legacy dev/password path.
 */
export async function getAuthMethods(): Promise<AuthMethods> {
  try {
    const res = await fetch(`${serviceURL()}/api/v2.1/auth/methods`, {
      headers: { Accept: 'application/json' },
      credentials: 'include',
    });
    if (!res.ok) return { local: false, oidc: false };
    const data = await res.json();
    return { local: Boolean(data.local), oidc: Boolean(data.oidc) };
  } catch {
    return { local: false, oidc: false };
  }
}

export interface LocalLoginResult {
  token: string;
  email?: string;
  name?: string;
  role?: string;
  must_change_password?: boolean;
}

/**
 * Log in with a local (username/password) account against sesameauth. On
 * success the returned session token is stored the same way as any other
 * session (localStorage.seahub_token) so the rest of the app works.
 */
export async function localLogin(email: string, password: string): Promise<LocalLoginResult> {
  const res = await fetch(`${serviceURL()}/api/v2.1/auth/local/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    credentials: 'include',
    body: JSON.stringify({ email: email.trim(), password }),
  });

  if (!res.ok) {
    if (res.status === 429) {
      throw new Error('Too many failed attempts. Please wait and try again.');
    }
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Login failed. Please check your credentials.');
  }

  const data = (await res.json()) as LocalLoginResult;
  if (data.token) setAuthToken(data.token);
  return data;
}

/**
 * Change the current user's local-auth password (verifies the current one).
 * Authenticated with the active session token.
 */
export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/auth/local/change-password`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to change password');
  }
}

// Group types

export interface Group {
  id: number;
  name: string;
  owner: string;
  created_at: string;
  member_count: number;
}

export interface GroupMember {
  email: string;
  name: string;
  role: string;
  avatar_url: string;
}

export interface GroupRepo {
  repo_id: string;
  repo_name: string;
  permission: string;
  size: number;
  owner_email: string;
  owner_name: string;
  encrypted: boolean;
  last_modified: string;
  modifier_email: string;
  modifier_name: string;
}

// Group API methods

export async function listGroups(): Promise<Group[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load groups');
  const data = await res.json();
  return data as Group[];
}

export async function createGroup(name: string): Promise<Group> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to create group');
  }
  await invalidateApiCache('/api/v2.1/groups');
  return await res.json();
}

export async function listGroupRepos(groupId: string): Promise<GroupRepo[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/libraries`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load group libraries');
  return await res.json();
}

export async function listGroupMembers(groupId: string): Promise<GroupMember[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load group members');
  return await res.json();
}

// Group management (mutations). Access levels enforced by the backend:
//   - add member:      group owner OR admin
//   - remove member:   owner/admin (anyone) · a plain member may remove only
//                      themselves (i.e. "leave the group")
//   - set/unset admin: owner only
//   - delete group:    owner only

/** Add a member to a group (owner/admin). POST /groups/:id/members/ {email}. */
export async function addGroupMember(groupId: string, email: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ email }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to add member');
  }
  await invalidateApiCache(`/api/v2.1/groups/${groupId}/members`);
}

/** Remove a member (owner/admin), or leave the group (member removing self). */
export async function removeGroupMember(groupId: string, email: string): Promise<void> {
  const res = await fetch(
    `${serviceURL()}/api/v2.1/groups/${groupId}/members/${encodeURIComponent(email)}`,
    { method: 'DELETE', headers: authHeaders() },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to remove member');
  }
  await invalidateApiCache(`/api/v2.1/groups/${groupId}/members`);
  await invalidateApiCache('/api/v2.1/groups');
}

/** Promote/demote a member to/from group admin (owner only). */
export async function setGroupAdmin(
  groupId: string,
  email: string,
  isAdmin: boolean,
): Promise<void> {
  const res = await fetch(
    `${serviceURL()}/api/v2.1/groups/${groupId}/members/${encodeURIComponent(email)}`,
    {
      method: 'PUT',
      headers: { ...authHeaders(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ is_admin: isAdmin }),
    },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to update member role');
  }
  await invalidateApiCache(`/api/v2.1/groups/${groupId}/members`);
}

/** Delete a group (owner only). */
export async function deleteGroup(groupId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to delete group');
  }
  await invalidateApiCache('/api/v2.1/groups');
}

export interface Department {
  id: string;
  name: string;
}

/** Departments the signed-in user belongs to. GET /api/v2.1/departments/. */
export async function listMyDepartments(): Promise<Department[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/departments/`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to load departments');
  const data = await res.json();
  const list = Array.isArray(data) ? data : (data.departments ?? []);
  return list.map((d: any) => ({ id: String(d.id ?? d.group_id), name: d.name ?? d.group_name }));
}

// Encryption

export async function setRepoPassword(repoId: string, password: string): Promise<void> {
  // Unlock an encrypted library. The dedicated endpoint verifies the password
  // and opens a server-side decrypt session. (The old POST /api2/repos/:id/ with
  // a form `password` returned 400 "unsupported operation" — unlocking never
  // worked.)
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/set-password/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  });
  if (!res.ok) throw new Error('Incorrect password');
}

// File/Directory types

import type { Activity, Dirent, Repo, SearchResult } from './models';

// Repo API

export async function listRepos(): Promise<Repo[]> {
  const res = await fetch(`${serviceURL()}/api2/repos/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load libraries');
  const data = await res.json();
  return data.map((r: Record<string, unknown>) => ({
    repo_id: r.id ?? r.repo_id,
    repo_name: r.name ?? r.repo_name,
    size: r.size ?? 0,
    permission: r.permission ?? 'r',
    owner_email: r.owner ?? r.owner_email ?? '',
    owner_name: r.owner_name ?? '',
    encrypted: !!r.encrypted,
    last_modified: r.mtime ? new Date((r.mtime as number) * 1000).toISOString() : (r.last_modified ?? ''),
  })) as Repo[];
}

// Directory listing

export async function listDir(repoId: string, path: string): Promise<Dirent[]> {
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/dir/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load directory');
  return await res.json();
}

// Rename

export async function renameFile(repoId: string, path: string, newName: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/file/?p=${encodeURIComponent(path)}&operation=rename`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ newname: newName }),
  });
  if (!res.ok) throw new Error('Failed to rename file');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

export async function renameDir(repoId: string, path: string, newName: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(path)}&operation=rename`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ newname: newName }),
  });
  if (!res.ok) throw new Error('Failed to rename folder');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

// Delete

export async function deleteFile(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/file/?p=${encodeURIComponent(path)}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete file');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

export async function deleteDir(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(path)}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete folder');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

// Move / Copy — use the batch-item endpoints (same-repo = sync, cross-repo =
// async), matching the web app. The older /api2 `operation=move|copy` path is
// NOT accepted by this backend ("source path is required").
async function batchTransfer(
  kind: 'move' | 'copy',
  srcRepoId: string,
  srcPath: string,
  dstRepoId: string,
  dstDir: string,
): Promise<void> {
  const lastSlash = srcPath.lastIndexOf('/');
  const srcParent = lastSlash <= 0 ? '/' : srcPath.slice(0, lastSlash);
  const name = srcPath.slice(lastSlash + 1);
  const mode = srcRepoId === dstRepoId ? 'sync' : 'async';
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${mode}-batch-${kind}-item/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({
      src_repo_id: srcRepoId,
      src_parent_dir: srcParent,
      src_dirents: [name],
      dst_repo_id: dstRepoId,
      dst_parent_dir: dstDir,
    }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || `Failed to ${kind}`);
  }
  await invalidateApiCache(`/api2/repos/${srcRepoId}/dir`);
  await invalidateApiCache(`/api2/repos/${dstRepoId}/dir`);
}

export async function moveFile(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  return batchTransfer('move', srcRepoId, srcPath, dstRepoId, dstDir);
}

export async function copyFile(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  return batchTransfer('copy', srcRepoId, srcPath, dstRepoId, dstDir);
}

export async function moveDir(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  return batchTransfer('move', srcRepoId, srcPath, dstRepoId, dstDir);
}

export async function copyDir(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  return batchTransfer('copy', srcRepoId, srcPath, dstRepoId, dstDir);
}

// File download link

export async function getFileDownloadLink(repoId: string, path: string): Promise<string> {
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/file/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to get download link');
  const url = await res.json();
  return url as string;
}

// OnlyOffice document editor

/** Full OnlyOffice editor config the DocsAPI.DocEditor constructor consumes. */
export interface OnlyOfficeDocConfig {
  document: Record<string, unknown>;
  documentType: string;
  editorConfig: Record<string, unknown>;
  token?: string;
}

export interface OnlyOfficeConfigResponse {
  /** Config object passed straight to `new DocsAPI.DocEditor(id, doc)`. */
  doc: OnlyOfficeDocConfig;
  /** URL of the OnlyOffice document server's api.js (browser-facing). */
  api_js_url: string;
}

/**
 * Fetch the signed OnlyOffice editor configuration for a document. Mirrors the
 * web frontend: GET /api/v2.1/repos/:repo/onlyoffice/?p=<path>. Throws with the
 * backend's error_msg (e.g. "OnlyOffice is not enabled") so the viewer can fall
 * back to download.
 */
export async function getOnlyOfficeConfig(
  repoId: string,
  path: string,
): Promise<OnlyOfficeConfigResponse> {
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/onlyoffice/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) {
    let msg = 'Failed to open document';
    try {
      const body = await res.json();
      if (body?.error_msg) msg = body.error_msg;
    } catch {
      // non-JSON error body — keep the generic message
    }
    throw new Error(msg);
  }
  return (await res.json()) as OnlyOfficeConfigResponse;
}

// Star / Unstar

export interface StarredFile {
  repo_id: string;
  repo_name: string;
  path: string;
  obj_name: string;
  mtime: number;
  size: number;
  is_dir: boolean;
}

export async function listStarredFiles(): Promise<StarredFile[]> {
  const res = await fetch(`${serviceURL()}/api2/starredfiles/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load starred files');
  // The API returns { starred_item_list: [...] }; older builds returned a bare
  // array. Normalize to an array so callers can always .map() safely.
  const data = await res.json();
  if (Array.isArray(data)) return data;
  return (data?.starred_item_list as StarredFile[]) ?? [];
}

export async function starFile(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/starredfiles/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ repo_id: repoId, p: path }),
  });
  if (!res.ok) throw new Error('Failed to star file');
  // Refresh the dir listing's `starred` flag (SW caches GET /api2/... stale).
  await invalidateApiCache(`/api2/repos/${repoId}`);
  await invalidateApiCache('/api2/starredfiles');
}

export async function unstarFile(repoId: string, path: string): Promise<void> {
  const params = new URLSearchParams({ repo_id: repoId, p: path });
  const res = await fetch(`${serviceURL()}/api2/starredfiles/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to unstar file');
  await invalidateApiCache(`/api2/repos/${repoId}`);
  await invalidateApiCache('/api2/starredfiles');
}

// Share link types

export interface ShareLink {
  token: string;
  link: string;
  repo_id: string;
  path: string;
  is_dir: boolean;
  is_expired: boolean;
  expire_date: string | null;
  permissions: {
    can_edit: boolean;
    can_download: boolean;
  };
  password?: string;
  ctime: string;
  view_cnt: number;
}

export interface ShareLinkOptions {
  password?: string;
  expire_days?: number;
  permissions?: {
    can_edit?: boolean;
    can_download?: boolean;
  };
}

// Share link API methods

export async function listShareLinks(repoId: string, path: string): Promise<ShareLink[]> {
  const params = new URLSearchParams({ repo_id: repoId, path });
  const res = await fetch(`${serviceURL()}/api/v2.1/share-links/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list share links');
  return await res.json();
}

export async function createShareLink(repoId: string, path: string, options: ShareLinkOptions = {}): Promise<ShareLink> {
  const body: Record<string, unknown> = { repo_id: repoId, path };
  if (options.password) body.password = options.password;
  if (options.expire_days) body.expire_days = options.expire_days;
  if (options.permissions) {
    // The backend expects a permission STRING, not an object.
    const p = options.permissions;
    body.permissions = p.can_edit ? 'edit' : p.can_download ? 'download' : 'preview_only';
  }
  const res = await fetch(`${serviceURL()}/api/v2.1/share-links/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to create share link');
  }
  return await res.json();
}

export async function deleteShareLink(token: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/share-links/${token}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete share link');
}

// Internal sharing types

export interface ShareItem {
  user_email: string;
  user_name: string;
  avatar_url: string;
  permission: string;
}

export interface GroupShareItem {
  group_id: number;
  group_name: string;
  permission: string;
}

export interface SearchedUser {
  email: string;
  name: string;
  avatar_url: string;
}

// Internal sharing API methods

export async function listRepoShareItems(repoId: string, path: string): Promise<ShareItem[]> {
  // Backend reads the path from the `p` query param (not `path`); repo comes
  // from the URL. Using the wrong name silently defaulted every lookup to "/".
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list shared items');
  const data = await res.json();
  // The backend returns each user share as { share_to, share_to_name, ... };
  // map it onto the ShareItem shape the UI renders (user_email/user_name).
  // Without this the shared-users list showed blank rows and "remove" sent an
  // undefined username.
  return (data as any[])
    .filter(item => item.share_type === 'user')
    .map(item => ({
      user_email: item.user_email ?? item.share_to,
      user_name: item.user_name ?? item.share_to_name ?? item.share_to,
      avatar_url: item.avatar_url ?? '',
      permission: item.permission,
    }));
}

export async function listRepoGroupShares(repoId: string, path: string): Promise<GroupShareItem[]> {
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list group shares');
  const data = await res.json();
  return (data as any[]).filter(item => item.share_type === 'group').map(item => ({
    group_id: item.group_id,
    group_name: item.group_name,
    permission: item.permission,
  }));
}

export async function shareToUser(repoId: string, path: string, email: string, permission: string): Promise<void> {
  // Backend contract: path is the `p` query param and `username` is an ARRAY.
  // Sending it as a scalar (or path in the body) makes the bind fail → 400
  // "invalid request body", which broke user sharing entirely.
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ share_type: 'user', username: [email], permission }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to share');
  }
  // Drop the cached shared-items list so the UI reflects the new share.
  await invalidateApiCache(`/api/v2.1/repos/${repoId}/dir/shared_items`);
}

export async function shareToGroup(repoId: string, path: string, groupId: number, permission: string): Promise<void> {
  // Same contract as user sharing: path via `p` query, group_id as an ARRAY.
  const params = new URLSearchParams({ p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ share_type: 'group', group_id: [String(groupId)], permission }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to share to group');
  }
  await invalidateApiCache(`/api/v2.1/repos/${repoId}/dir/shared_items`);
}

export async function removeUserShare(repoId: string, path: string, email: string): Promise<void> {
  const params = new URLSearchParams({ share_type: 'user', username: email, p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove share');
  await invalidateApiCache(`/api/v2.1/repos/${repoId}/dir/shared_items`);
}

export async function removeGroupShare(repoId: string, path: string, groupId: number): Promise<void> {
  const params = new URLSearchParams({ share_type: 'group', group_id: String(groupId), p: path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove group share');
  await invalidateApiCache(`/api/v2.1/repos/${repoId}/dir/shared_items`);
}

// Activities

export async function listActivities(page: number = 1): Promise<{ events: Activity[]; more: boolean }> {
  const params = new URLSearchParams({ page: String(page) });
  const res = await fetch(`${serviceURL()}/api/v2.1/activities/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load activities');
  const data = await res.json();
  return { events: data.events || [], more: !!data.more };
}

/**
 * Advanced-search filter options. Mirrors the params the Go backend's
 * /api/v2.1/search/ endpoint understands:
 *   - objType: 'file' | 'dir' | 'repo' → sent as `type`; omit for "all types".
 *   - repoId: limit search to a single library → sent as `repo_id`.
 * All fields are optional so existing 3-arg callers keep working unchanged.
 */
export interface SearchOptions {
  objType?: 'file' | 'dir' | 'repo';
  repoId?: string;
}

export async function searchFiles(
  query: string,
  page: number = 1,
  perPage: number = 25,
  options: SearchOptions = {},
): Promise<{ results: SearchResult[]; total: number }> {
  const params = new URLSearchParams({ q: query, page: String(page), per_page: String(perPage) });
  if (options.objType) params.set('type', options.objType);
  if (options.repoId) params.set('repo_id', options.repoId);
  const res = await fetch(`${serviceURL()}/api/v2.1/search/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to search files');
  const data = await res.json();
  const rawResults: Record<string, unknown>[] = data.results || [];
  // The backend returns `fullpath`; the app's SearchResult model uses `path`.
  const results = rawResults.map((r) => ({
    ...r,
    path: (r.path ?? r.fullpath ?? '') as string,
  })) as unknown as SearchResult[];
  return { results, total: data.total ?? results.length };
}

// Shared repos types

export interface SharedRepo {
  repo_id: string;
  repo_name: string;
  repo_desc: string;
  permission: string;
  share_type: string;
  user: string;
  last_modified: number;
  is_virtual: boolean;
  encrypted: number;
}

// Shared repos API methods

export async function listSharedRepos(): Promise<SharedRepo[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/shared-repos/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load shared libraries');
  return await res.json();
}

export async function listBeSharedRepos(): Promise<SharedRepo[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/beshared-repos/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load shared libraries');
  return await res.json();
}

// Upload link types

export interface UploadLink {
  token: string;
  link: string;
  repo_id: string;
  path: string;
  ctime: string;
  username: string;
  view_cnt: number;
}

// List all share links (no repo/path filter)

export async function listAllShareLinks(): Promise<ShareLink[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/share-links/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list share links');
  return await res.json();
}

// List all upload links

export async function listAllUploadLinks(): Promise<UploadLink[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/upload-links/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list upload links');
  return await res.json();
}

// Delete upload link

export async function deleteUploadLink(token: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/upload-links/${token}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete upload link');
}

// Account info

export interface AccountInfo {
  usage: number;
  total: number;
  email: string;
  name: string;
  login_id: string;
  institution: string;
  is_staff: boolean;
  /** 1/true when the account is an org admin (org staff). May be 0/1 or boolean. */
  is_org_staff?: number | boolean;
  avatar_url: string;
}

export async function getAccountInfo(): Promise<AccountInfo> {
  const res = await fetch(`${serviceURL()}/api2/account/info/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load account info');
  return await res.json();
}

/**
 * Update the current user's profile. Mirrors the web `updateUserInfo`
 * (PUT /api2/account/info/ with a `name` field). Invalidates the SW cache so
 * the next getAccountInfo() reflects the change.
 */
export async function updateAccountInfo(fields: { name?: string }): Promise<AccountInfo> {
  const payload: Record<string, string> = {};
  if (typeof fields.name === 'string') payload.name = fields.name;
  const res = await fetch(`${serviceURL()}/api2/account/info/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to update profile');
  }
  await invalidateApiCache('/api2/account');
  return await res.json();
}

export async function createRepo(
  name: string,
  encrypted?: boolean,
  password?: string,
  storageID?: string,
): Promise<Repo> {
  // `encrypted` must be a JSON boolean — sending the string "true" makes the
  // backend reject the body ("cannot unmarshal string into ... bool"), so
  // encrypted-library creation failed outright.
  const body: Record<string, unknown> = { name };
  if (encrypted) {
    body.encrypted = true;
    if (password) body.passwd = password;
  }
  if (storageID) {
    body.storage_id = storageID;
  }
  const res = await fetch(`${serviceURL()}/api2/repos/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to create library');
  }
  await invalidateApiCache('/api2/repos');
  return await res.json();
}

export async function logout(): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/auth/logout/`, {
    method: 'POST',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Logout failed');
  clearAuthToken();
}

export async function searchUsers(query: string): Promise<SearchedUser[]> {
  const params = new URLSearchParams({ q: query });
  const res = await fetch(`${serviceURL()}/api2/search-user/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to search users');
  const data = await res.json();
  return data.users || [];
}
