import { serviceURL } from '../config';
import { authHeaders, invalidateApiCache } from '../api';

// Custom share permissions (E8). Endpoints mirror the web frontend exactly
// (seafile-api.js listCustomSharePermissions / createCustomSharePermission /
// deleteCustomSharePermission / updateCustomSharePermission):
//   GET    /api/v2.1/repos/:id/custom-share-permissions/
//   POST   /api/v2.1/repos/:id/custom-share-permissions/
//   GET    /api/v2.1/repos/:id/custom-share-permissions/:permId/
//   PUT    /api/v2.1/repos/:id/custom-share-permissions/:permId/
//   DELETE /api/v2.1/repos/:id/custom-share-permissions/:permId/
//
// GOTCHA: the Go handler (internal/api/v2/file_shares.go CreateCustomSharePermission)
// binds `permission` as a JSON *string* and json.Unmarshal's it — so the body must
// send permission as JSON.stringify(permObj), NOT a nested object. The create/update
// body is { permission_name, description, permission } (permission is a stringified
// object of boolean toggles: upload/download/create/modify/copy/delete/preview/
// download_external_link).

export interface CustomPermissionOptions {
  upload: boolean;
  download: boolean;
  create: boolean;
  modify: boolean;
  copy: boolean;
  delete: boolean;
  preview: boolean;
  download_external_link: boolean;
}

export interface CustomPermission {
  id: string;
  name: string;
  description: string;
  permission: Partial<CustomPermissionOptions>;
}

export const EMPTY_PERMISSION: CustomPermissionOptions = {
  upload: false,
  download: false,
  create: false,
  modify: false,
  copy: false,
  delete: false,
  preview: false,
  download_external_link: false,
};

function baseUrl(repoId: string): string {
  return `${serviceURL()}/api/v2.1/repos/${repoId}/custom-share-permissions`;
}

function cacheKey(repoId: string): string {
  return `/api/v2.1/repos/${repoId}/custom-share-permissions`;
}

export async function listCustomPermissions(repoId: string): Promise<CustomPermission[]> {
  const res = await fetch(`${baseUrl(repoId)}/`, { headers: authHeaders() });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to load custom permissions');
  }
  const data = await res.json();
  return (data.permission_list ?? []) as CustomPermission[];
}

export async function createCustomPermission(
  repoId: string,
  input: { name: string; description: string; permission: Partial<CustomPermissionOptions> },
): Promise<CustomPermission> {
  const res = await fetch(`${baseUrl(repoId)}/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({
      permission_name: input.name,
      description: input.description,
      // Backend expects a JSON string for `permission`.
      permission: JSON.stringify({ ...EMPTY_PERMISSION, ...input.permission }),
    }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to create custom permission');
  }
  const data = await res.json();
  await invalidateApiCache(cacheKey(repoId));
  return data.permission as CustomPermission;
}

export async function updateCustomPermission(
  repoId: string,
  permissionId: string,
  input: { name: string; description: string; permission: Partial<CustomPermissionOptions> },
): Promise<CustomPermission> {
  const res = await fetch(`${baseUrl(repoId)}/${permissionId}/`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({
      permission_name: input.name,
      description: input.description,
      permission: JSON.stringify({ ...EMPTY_PERMISSION, ...input.permission }),
    }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to update custom permission');
  }
  const data = await res.json();
  await invalidateApiCache(cacheKey(repoId));
  return data.permission as CustomPermission;
}

export async function deleteCustomPermission(repoId: string, permissionId: string): Promise<void> {
  const res = await fetch(`${baseUrl(repoId)}/${permissionId}/`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || data.error_msg || 'Failed to delete custom permission');
  }
  await invalidateApiCache(cacheKey(repoId));
}
