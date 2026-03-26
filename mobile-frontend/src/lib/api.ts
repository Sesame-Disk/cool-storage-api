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

function authHeaders(): Record<string, string> {
  const token = getAuthToken();
  return {
    'Authorization': `Token ${token}`,
    'Accept': 'application/json',
  };
}

/** Remove a URL from the service-worker API cache so the next fetch hits the network. */
async function invalidateApiCache(path: string): Promise<void> {
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

// Group management API methods

export async function renameGroup(groupId: number, name: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to rename group');
  }
  await invalidateApiCache('/api/v2.1/groups');
}

export async function deleteGroup(groupId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete group');
  await invalidateApiCache('/api/v2.1/groups');
}

export async function transferGroup(groupId: number, owner: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ owner }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to transfer group');
  }
  await invalidateApiCache('/api/v2.1/groups');
}

export async function quitGroup(groupId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members/myself/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to leave group');
  await invalidateApiCache('/api/v2.1/groups');
}

export async function addGroupMembers(groupId: number, emails: string[]): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members/bulk/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ emails: emails.join(',') }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to add members');
  }
}

export async function deleteGroupMember(groupId: number, email: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members/${encodeURIComponent(email)}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove member');
}

export async function setGroupAdmin(groupId: number, email: string, isAdmin: boolean): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/groups/${groupId}/members/${encodeURIComponent(email)}/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ is_admin: String(isAdmin) }),
  });
  if (!res.ok) throw new Error('Failed to update member role');
}

// Encryption

export async function setRepoPassword(repoId: string, password: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ password }),
  });
  if (!res.ok) throw new Error('Incorrect password');
}

// File/Directory types

import type { Activity, Dirent, FileHistoryRecord, FileTag, LinkedDevice, Notification, Repo, RepoTag, SearchResult, TrashItem } from './models';

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
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/file/?p=${encodeURIComponent(path)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'rename', newname: newName }),
  });
  if (!res.ok) throw new Error('Failed to rename file');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

export async function renameDir(repoId: string, path: string, newName: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(path)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'rename', newname: newName }),
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

// Move / Copy

export async function moveFile(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${srcRepoId}/file/?p=${encodeURIComponent(srcPath)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'move', dst_repo: dstRepoId, dst_dir: dstDir }),
  });
  if (!res.ok) throw new Error('Failed to move file');
  await invalidateApiCache(`/api2/repos/${srcRepoId}/dir`);
  await invalidateApiCache(`/api2/repos/${dstRepoId}/dir`);
}

export async function copyFile(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${srcRepoId}/file/?p=${encodeURIComponent(srcPath)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'copy', dst_repo: dstRepoId, dst_dir: dstDir }),
  });
  if (!res.ok) throw new Error('Failed to copy file');
  await invalidateApiCache(`/api2/repos/${dstRepoId}/dir`);
}

export async function moveDir(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${srcRepoId}/dir/?p=${encodeURIComponent(srcPath)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'move', dst_repo: dstRepoId, dst_dir: dstDir }),
  });
  if (!res.ok) throw new Error('Failed to move folder');
  await invalidateApiCache(`/api2/repos/${srcRepoId}/dir`);
  await invalidateApiCache(`/api2/repos/${dstRepoId}/dir`);
}

export async function copyDir(srcRepoId: string, srcPath: string, dstRepoId: string, dstDir: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${srcRepoId}/dir/?p=${encodeURIComponent(srcPath)}`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'copy', dst_repo: dstRepoId, dst_dir: dstDir }),
  });
  if (!res.ok) throw new Error('Failed to copy folder');
  await invalidateApiCache(`/api2/repos/${dstRepoId}/dir`);
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
  return await res.json();
}

export async function starFile(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/starredfiles/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ repo_id: repoId, p: path }),
  });
  if (!res.ok) throw new Error('Failed to star file');
}

export async function unstarFile(repoId: string, path: string): Promise<void> {
  const params = new URLSearchParams({ repo_id: repoId, p: path });
  const res = await fetch(`${serviceURL()}/api2/starredfiles/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to unstar file');
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
  if (options.permissions) body.permissions = options.permissions;
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
  const params = new URLSearchParams({ repo_id: repoId, path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list shared items');
  const data = await res.json();
  return (data as any[]).filter(item => item.share_type === 'user');
}

export async function listRepoGroupShares(repoId: string, path: string): Promise<GroupShareItem[]> {
  const params = new URLSearchParams({ repo_id: repoId, path });
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
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ share_type: 'user', username: email, path, permission }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to share');
  }
}

export async function shareToGroup(repoId: string, path: string, groupId: number, permission: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ share_type: 'group', group_id: groupId, path, permission }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to share to group');
  }
}

export async function removeUserShare(repoId: string, path: string, email: string): Promise<void> {
  const params = new URLSearchParams({ share_type: 'user', username: email, path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove share');
}

export async function removeGroupShare(repoId: string, path: string, groupId: number): Promise<void> {
  const params = new URLSearchParams({ share_type: 'group', group_id: String(groupId), path });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove group share');
}

// Notifications

export async function listNotifications(page: number = 1, perPage: number = 25): Promise<{ notification_list: Notification[]; count: number }> {
  const params = new URLSearchParams({ page: String(page), per_page: String(perPage) });
  const res = await fetch(`${serviceURL()}/api/v2.1/notifications/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load notifications');
  return await res.json();
}

export async function getUnseenNotificationCount(): Promise<number> {
  try {
    const res = await fetch(`${serviceURL()}/api/v2.1/notifications/count/`, {
      headers: authHeaders(),
    });
    if (!res.ok) return 0;
    const data = await res.json();
    return data.unseen_count ?? 0;
  } catch {
    return 0;
  }
}

export async function markNotificationAsRead(notificationId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/notification/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ notice_id: String(notificationId) }),
  });
  if (!res.ok) throw new Error('Failed to mark notification as read');
}

export async function markAllNotificationsRead(): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/notifications/`, {
    method: 'PUT',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to mark all notifications as read');
}

export async function deleteNotification(notificationId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/notification/?notice_id=${notificationId}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete notification');
}

export async function deleteAllNotifications(): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/notifications/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete all notifications');
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

export async function searchFiles(query: string, page: number = 1, perPage: number = 25): Promise<{ results: SearchResult[]; total: number }> {
  const params = new URLSearchParams({ q: query, page: String(page), per_page: String(perPage) });
  const res = await fetch(`${serviceURL()}/api/v2.1/search-file/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to search files');
  const data = await res.json();
  return { results: data.results || [], total: data.total || 0 };
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
  avatar_url: string;
}

export async function getAccountInfo(): Promise<AccountInfo> {
  const res = await fetch(`${serviceURL()}/api2/account/info/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load account info');
  return await res.json();
}

export async function createRepo(
  name: string,
  encrypted?: boolean,
  password?: string,
): Promise<Repo> {
  const body: Record<string, string> = { name };
  if (encrypted) {
    body.encrypted = 'true';
    if (password) body.passwd = password;
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

// Trash / Recycle Bin

export async function listTrash(
  repoId: string,
  path?: string,
  scanStat?: string,
): Promise<{ data: TrashItem[]; more: boolean; scan_stat: string | null }> {
  const params = new URLSearchParams();
  if (path) params.set('parent_dir', path);
  if (scanStat) params.set('scan_stat', scanStat);
  const qs = params.toString() ? `?${params.toString()}` : '';
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/trash/${qs}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load trash');
  return await res.json();
}

export async function restoreTrashItem(
  repoId: string,
  commitId: string,
  path: string,
  isDir: boolean,
): Promise<void> {
  const endpoint = isDir ? 'dir' : 'file';
  const body = new FormData();
  body.append('commit_id', commitId);
  body.append('p', path);
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/${endpoint}/restore/`, {
    method: 'POST',
    headers: { 'Authorization': `Token ${getAuthToken()}` },
    body,
  });
  if (!res.ok) throw new Error('Failed to restore item');
}

export async function cleanTrash(repoId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/trash/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to clean trash');
}

// File History

export async function listFileHistory(
  repoId: string,
  path: string,
  page: number = 1,
  perPage: number = 25,
): Promise<{ data: FileHistoryRecord[]; total_count: number }> {
  const params = new URLSearchParams({
    path,
    page: String(page),
    per_page: String(perPage),
  });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file/new_history/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load file history');
  return await res.json();
}

export async function getFileRevision(
  repoId: string,
  commitId: string,
  path: string,
): Promise<string> {
  const params = new URLSearchParams({
    p: path,
    commit_id: commitId,
  });
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file/revision/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to get file revision');
  const url = await res.json();
  return url as string;
}

// Repo Tags

export async function listRepoTags(repoId: string): Promise<RepoTag[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load tags');
  const data = await res.json();
  return data.repo_tags ?? data;
}

export async function createRepoTag(repoId: string, name: string, color: string): Promise<RepoTag> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, color }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    if (res.status === 400) throw new Error(`Tag '${name}' already exists`);
    throw new Error(data.error_msg || 'Failed to create tag');
  }
  const data = await res.json();
  return data.repo_tag ?? data;
}

export async function updateRepoTag(repoId: string, tagId: number, name: string, color: string): Promise<RepoTag> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/${tagId}/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, color }),
  });
  if (!res.ok) throw new Error('Failed to update tag');
  const data = await res.json();
  return data.repo_tag ?? data;
}

export async function deleteRepoTag(repoId: string, tagId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/${tagId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete tag');
}

// File Tags

export async function listFileTags(repoId: string, filePath: string): Promise<FileTag[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file-tags/?file_path=${encodeURIComponent(filePath)}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load file tags');
  const data = await res.json();
  return data.file_tags ?? data;
}

export async function addFileTag(repoId: string, filePath: string, repoTagId: number): Promise<FileTag> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file-tags/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ file_path: filePath, repo_tag_id: repoTagId }),
  });
  if (!res.ok) throw new Error('Failed to add tag to file');
  const data = await res.json();
  return data.file_tag ?? data;
}

export async function deleteFileTag(repoId: string, fileTagId: number): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file-tags/${fileTagId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to remove tag from file');
}

export async function listTaggedFiles(repoId: string, tagId: number): Promise<unknown[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/tagged-files/${tagId}/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to list tagged files');
  return await res.json();
}

// Zip download

export interface ZipTaskResponse {
  zip_token: string;
}

export interface ZipProgressResponse {
  zipped: number;
  total: number;
  failed: number;
  failed_reason?: string;
}

export async function zipDownload(
  repoId: string,
  parentDir: string,
  dirents: string | string[],
): Promise<ZipTaskResponse> {
  const body = new URLSearchParams();
  body.append('parent_dir', parentDir);
  if (typeof dirents === 'string') {
    body.append('dirents', JSON.stringify([dirents]));
  } else {
    body.append('dirents', JSON.stringify(dirents));
  }
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/zip-task/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });
  if (!res.ok) throw new Error('Failed to start zip download');
  return await res.json();
}

export async function queryZipProgress(zipToken: string): Promise<ZipProgressResponse> {
  const params = new URLSearchParams({ token: zipToken });
  const res = await fetch(`${serviceURL()}/api/v2.1/query-zip-progress/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to query zip progress');
  return await res.json();
}

export async function cancelZipTask(zipToken: string): Promise<void> {
  const params = new URLSearchParams({ token: zipToken });
  const res = await fetch(`${serviceURL()}/api/v2.1/cancel-zip-task/?${params}`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to cancel zip task');
}

// Library management

export async function renameRepo(repoId: string, newName: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/?op=rename`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ repo_name: newName }),
  });
  if (!res.ok) throw new Error('Failed to rename library');
  await invalidateApiCache('/api2/repos');
}

export async function deleteRepo(repoId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete library');
  await invalidateApiCache('/api2/repos');
}

export async function transferRepo(repoId: string, owner: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/owner/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ owner }),
  });
  if (!res.ok) throw new Error('Failed to transfer library');
  await invalidateApiCache('/api2/repos');
}

export async function leaveShareRepo(repoId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/shared-repos/${repoId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to leave shared library');
  await invalidateApiCache('/api2/repos');
}

export async function revertFile(
  repoId: string,
  commitId: string,
  path: string,
): Promise<void> {
  const res = await fetch(
    `${serviceURL()}/api/v2.1/repos/${repoId}/file/revert/?p=${encodeURIComponent(path)}`,
    {
      method: 'POST',
      headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ commit_id: commitId }),
    },
  );
  if (!res.ok) throw new Error('Failed to revert file');
}

// Shared Folders

export interface SharedFolder {
  repo_id: string;
  repo_name: string;
  path: string;
  folder_name: string;
  share_type: 'personal' | 'group';
  share_permission: string;
  share_permission_name?: string;
  user_email?: string;
  user_name?: string;
  contact_email?: string;
  group_id?: number;
  group_name?: string;
}

export async function listSharedFolders(): Promise<SharedFolder[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/shared-folders/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load shared folders');
  return await res.json();
}

export async function unshareFolder(
  repoId: string,
  path: string,
  shareType: 'user' | 'group',
  shareToId: string,
): Promise<void> {
  const params = new URLSearchParams({ p: path, share_type: shareType });
  if (shareType === 'user') {
    params.set('username', shareToId);
  } else {
    params.set('group_id', shareToId);
  }
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/dir/shared_items/?${params}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to unshare folder');
}

// Linked Devices

export async function listLinkedDevices(): Promise<LinkedDevice[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/devices/`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to load linked devices');
  return await res.json();
}

// File locking

export async function lockFile(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file/?p=${encodeURIComponent(path)}`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'lock' }),
  });
  if (!res.ok) throw new Error('Failed to lock file');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

export async function unlockFile(repoId: string, path: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/file/?p=${encodeURIComponent(path)}`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ operation: 'unlock' }),
  });
  if (!res.ok) throw new Error('Failed to unlock file');
  await invalidateApiCache(`/api2/repos/${repoId}/dir`);
}

export async function unlinkDevice(platform: string, deviceId: string, wipeDevice: boolean = false): Promise<void> {
  const form = new FormData();
  form.append('platform', platform);
  form.append('device_id', deviceId);
  form.append('wipe_device', String(wipeDevice));
  const res = await fetch(`${serviceURL()}/api/v2.1/devices/`, {
    method: 'DELETE',
    headers: { 'Authorization': `Token ${getAuthToken()}` },
    body: form,
  });
  if (!res.ok) throw new Error('Failed to unlink device');
}
