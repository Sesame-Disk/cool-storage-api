import { serviceURL } from '../config';
import { authHeaders, getAccountInfo, invalidateApiCache } from '../api';

// Org-admin API client. All endpoints live under
//   /api/v2.1/org/<ORG_ID>/admin/...
// where <ORG_ID> is the logged-in account's `institution` field
// (GET /api2/account/info/). Paths mirror internal/api/v2/org_admin_routes.go
// EXACTLY. Most writes are disabled on this backend — see notes on each fn.

const ORG_BASE = '/api/v2.1/org';

/**
 * Resolve the current account's organization id from account info. The org-admin
 * routes require the org id in the URL; it equals the account's `institution`.
 * Resolved at runtime — never hardcode.
 */
export async function getOrgId(): Promise<string> {
  const info = await getAccountInfo();
  if (!info.institution) {
    throw new Error('No organization associated with this account');
  }
  return info.institution;
}

function orgUrl(orgId: string, path: string): string {
  return `${serviceURL()}${ORG_BASE}/${orgId}/admin${path}`;
}

async function readJson<T>(res: Response, fallbackMsg: string): Promise<T> {
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || fallbackMsg);
  }
  return (await res.json()) as T;
}

// --------------------------------------------------------------------------
// Users  (READ-ONLY: org-admin user writes are disabled on this backend)
// --------------------------------------------------------------------------

export interface OrgUser {
  id: string;
  email: string;
  name: string;
  owner_contact_email: string;
  status: string;
  is_active: boolean;
  is_org_staff: boolean;
  role: string;
  quota_total: number;
  quota_usage: number;
  ctime: string;
  last_login: string;
  org_id: string;
}

export interface OrgUsersPage {
  users: OrgUser[];
  total: number;
  page: number;
  hasNext: boolean;
}

/** GET /users/ → { user_list, total_count, page, page_next }. Read-only. */
export async function listOrgUsers(orgId: string, page = 1): Promise<OrgUsersPage> {
  const res = await fetch(`${orgUrl(orgId, '/users/')}?page=${page}&per_page=100`, {
    headers: authHeaders(),
  });
  const data = await readJson<{
    user_list?: OrgUser[];
    total_count?: number;
    page?: number;
    page_next?: boolean;
  }>(res, 'Failed to load users');
  return {
    users: data.user_list ?? [],
    total: data.total_count ?? (data.user_list?.length ?? 0),
    page: data.page ?? page,
    hasNext: Boolean(data.page_next),
  };
}

/** GET /users/:email/ → single OrgUser. Read-only. */
export async function getOrgUser(orgId: string, email: string): Promise<OrgUser> {
  const res = await fetch(orgUrl(orgId, `/users/${encodeURIComponent(email)}/`), {
    headers: authHeaders(),
  });
  return readJson<OrgUser>(res, 'Failed to load user');
}

// --------------------------------------------------------------------------
// Groups  (list is read-only here)
// --------------------------------------------------------------------------

export interface OrgGroup {
  id: string;
  group_name: string;
  creator_name: string;
  creator_email: string;
  creator_contact_email: string;
  ctime: string;
}

/** GET /groups/ → { groups, page, page_next }. */
export async function listOrgGroups(orgId: string): Promise<OrgGroup[]> {
  const res = await fetch(`${orgUrl(orgId, '/groups/')}?per_page=100`, {
    headers: authHeaders(),
  });
  const data = await readJson<{ groups?: OrgGroup[] }>(res, 'Failed to load groups');
  return data.groups ?? [];
}

// --------------------------------------------------------------------------
// Departments (address-book groups). Managed by org admins / superadmins.
//   list/create/delete: org admin (or superadmin) via the org-scoped endpoints
//   add/remove members:  reuse addGroupMember / removeGroupMember (a department
//                        IS a group; the admin who created it is its owner)
// --------------------------------------------------------------------------

export interface OrgDepartment {
  id: string;
  name: string;
  parent_group_id?: string;
  member_count?: number;
}

/** GET /address-book/groups/ → department groups in the org. */
export async function listOrgDepartments(orgId: string): Promise<OrgDepartment[]> {
  const res = await fetch(`${orgUrl(orgId, '/address-book/groups/')}?per_page=1000`, {
    headers: authHeaders(),
  });
  const data = await readJson<any>(res, 'Failed to load departments');
  // The endpoint returns { data: [...] }; tolerate { groups } / bare array too.
  const list: any[] = Array.isArray(data) ? data : (data.data ?? data.groups ?? []);
  return list.map((d: any) => ({
    id: d.id ?? d.group_id,
    name: d.name ?? d.group_name,
    parent_group_id: d.parent_group_id ?? '',
    member_count: d.member_count,
  }));
}

/** POST /address-book/groups/ { group_name } — create a top-level department. */
export async function createDepartment(orgId: string, name: string): Promise<OrgDepartment> {
  const res = await fetch(orgUrl(orgId, '/address-book/groups/'), {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ group_name: name }),
  });
  const d = await readJson<any>(res, 'Failed to create department');
  // Drop the cached department list so the new one shows immediately.
  await invalidateApiCache(`/api/v2.1/org/${orgId}/admin/address-book/groups`);
  return { id: d.id ?? d.group_id, name: d.name ?? d.group_name };
}

export interface OrgDepartmentMember {
  email: string;
  name: string;
  role: string;
  avatar_url?: string;
}

/** GET /address-book/groups/:id/ → a department with its members. */
export async function getOrgDepartment(
  orgId: string,
  deptId: string,
): Promise<{ id: string; name: string; members: OrgDepartmentMember[] }> {
  const res = await fetch(orgUrl(orgId, `/address-book/groups/${deptId}/`), {
    headers: authHeaders(),
  });
  const d = await readJson<any>(res, 'Failed to load department');
  const members: OrgDepartmentMember[] = (d.members ?? []).map((m: any) => ({
    email: m.email ?? m.contact_email,
    name: m.name ?? m.email,
    role: m.role ?? 'member',
    avatar_url: m.avatar_url,
  }));
  return { id: d.id ?? d.group_id ?? deptId, name: d.name ?? d.group_name, members };
}

/** DELETE /address-book/groups/:id/ — remove a department (org admin). */
export async function deleteDepartment(orgId: string, deptId: string): Promise<void> {
  const res = await fetch(orgUrl(orgId, `/address-book/groups/${deptId}/`), {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to delete department');
  }
  await invalidateApiCache(`/api/v2.1/org/${orgId}/admin/address-book/groups`);
}

// --------------------------------------------------------------------------
// Repos / Libraries
// --------------------------------------------------------------------------

export interface OrgRepo {
  repo_id: string;
  repo_name: string;
  owner_name: string;
  owner_email: string;
  size: number;
  file_count: number;
  encrypted: boolean;
  is_department_repo: boolean;
  group_id: number | null;
}

/** GET /repos/ → { repo_list, page_info }. */
export async function listOrgRepos(orgId: string): Promise<OrgRepo[]> {
  const res = await fetch(`${orgUrl(orgId, '/repos/')}?per_page=100`, {
    headers: authHeaders(),
  });
  const data = await readJson<{ repo_list?: OrgRepo[] }>(res, 'Failed to load libraries');
  return data.repo_list ?? [];
}

/**
 * DELETE /repos/:rid/ — remove an org library. Verified writable on this
 * backend. Invalidates the org SW cache on success.
 */
export async function deleteOrgRepo(orgId: string, repoId: string): Promise<void> {
  const res = await fetch(orgUrl(orgId, `/repos/${repoId}/`), {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to delete library');
  }
  await invalidateApiCache(ORG_BASE);
}

// --------------------------------------------------------------------------
// Share links
// --------------------------------------------------------------------------

export interface OrgShareLink {
  token: string;
  name: string;
  obj_name: string;
  path: string;
  link: string;
  repo_id: string;
  repo_name: string;
  owner_email: string;
  owner_name: string;
  creator_email: string;
  creator_name: string;
  created_time: string;
  ctime: string;
  view_count: number;
  view_cnt: number;
  expire_date: string | null;
  is_expired: boolean;
  active: boolean;
  status: string;
  has_password: boolean;
  permissions: { can_download: boolean; can_edit: boolean };
}

/** GET /links/ → { link_list, page, page_next, count }. */
export async function listOrgShareLinks(orgId: string): Promise<OrgShareLink[]> {
  const res = await fetch(`${orgUrl(orgId, '/links/')}?per_page=100`, {
    headers: authHeaders(),
  });
  const data = await readJson<{ link_list?: OrgShareLink[] }>(res, 'Failed to load share links');
  return data.link_list ?? [];
}

/**
 * DELETE /links/:token/ — revoke an org share link. Verified writable on this
 * backend. Invalidates the org SW cache on success.
 */
export async function deleteOrgShareLink(orgId: string, token: string): Promise<void> {
  const res = await fetch(orgUrl(orgId, `/links/${token}/`), {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to revoke share link');
  }
  await invalidateApiCache(ORG_BASE);
}

// --------------------------------------------------------------------------
// Web settings  (READ-ONLY here: the PUT only accepts file_ext_white_list,
// not org name, so we surface these as read-only info)
// --------------------------------------------------------------------------

export interface OrgWebSettings {
  org_name: string;
  file_ext_white_list: string;
  logo_path: string;
}

/** GET /web-settings/ → org name, file-ext whitelist, logo path. */
export async function getOrgWebSettings(orgId: string): Promise<OrgWebSettings> {
  const res = await fetch(orgUrl(orgId, '/web-settings/'), {
    headers: authHeaders(),
  });
  return readJson<OrgWebSettings>(res, 'Failed to load organization settings');
}
