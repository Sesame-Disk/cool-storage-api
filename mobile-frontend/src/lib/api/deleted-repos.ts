import { serviceURL } from '../config';
import { authHeaders, invalidateApiCache } from '../api';

// Deleted libraries (recover a whole library). Endpoints mirror the web:
//   GET /api/v2.1/deleted-repos/          -> list soft-deleted libraries
//   PUT /api/v2.1/repos/deleted/:id/      -> restore one

export interface DeletedRepo {
  repo_id: string;
  repo_name: string;
  owner: string;
  del_time: string; // ISO 8601
  size: number;
}

export async function listDeletedRepos(): Promise<DeletedRepo[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/deleted-repos/`, {
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to load deleted libraries');
  }
  const data = await res.json();
  return (Array.isArray(data) ? data : (data.repos ?? [])) as DeletedRepo[];
}

export async function restoreDeletedRepo(repoId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/deleted/${repoId}/`, {
    method: 'PUT',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to restore library');
  }
  await invalidateApiCache('/api2/repos');
}
