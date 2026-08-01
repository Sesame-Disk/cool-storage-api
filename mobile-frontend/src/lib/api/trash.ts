import { serviceURL } from '../config';
import { authHeaders, invalidateApiCache } from '../api';

// Trash / recycle bin. Endpoints mirror the web frontend exactly:
//   GET    /api/v2.1/repos/:id/trash/?parent_dir=&scan_stat=
//   DELETE /api/v2.1/repos/:id/trash/?keep_days=
//   POST   /api/v2.1/repos/:id/file/restore/   { commit_id, p }
//   POST   /api/v2.1/repos/:id/dir/restore/    { commit_id, p }

export interface TrashItem {
  obj_name: string;
  parent_dir: string;
  is_dir: boolean;
  size: number;
  commit_id: string;
  deleted_time: string;
}

export interface TrashPage {
  items: TrashItem[];
  moreOffset: number | null;
  scanStat: string | null;
}

export async function listTrash(
  repoId: string,
  parentDir?: string,
  scanStat?: string,
): Promise<TrashPage> {
  const params = new URLSearchParams();
  if (parentDir) params.set('parent_dir', parentDir);
  if (scanStat) params.set('scan_stat', scanStat);
  const qs = params.toString();
  const res = await fetch(
    `${serviceURL()}/api/v2.1/repos/${repoId}/trash/${qs ? `?${qs}` : ''}`,
    { headers: authHeaders() },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to load trash');
  }
  const data = await res.json();
  return {
    items: (data.data ?? []) as TrashItem[],
    moreOffset: data.more ? (data.scan_stat ? null : (data.offset ?? null)) : null,
    scanStat: data.scan_stat ?? null,
  };
}

function restorePath(item: TrashItem): string {
  const dir = item.parent_dir.endsWith('/') ? item.parent_dir : `${item.parent_dir}/`;
  return `${dir}${item.obj_name}`;
}

/** Restore a single trashed file or folder to its original location. */
export async function restoreTrashItem(repoId: string, item: TrashItem): Promise<void> {
  const endpoint = item.is_dir ? 'dir' : 'file';
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/${endpoint}/restore/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ commit_id: item.commit_id, p: restorePath(item) }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to restore item');
  }
  await invalidateApiCache(`/api2/repos/${repoId}`);
}

/** Permanently empty the library's trash (optionally keeping the last N days). */
export async function cleanTrash(repoId: string, keepDays?: number): Promise<void> {
  const qs = keepDays !== undefined ? `?keep_days=${keepDays}` : '';
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/trash/${qs}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to clean trash');
  }
}
