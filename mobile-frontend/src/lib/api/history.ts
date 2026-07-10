import { serviceURL } from '../config';
import { authHeaders, invalidateApiCache } from '../api';

// Library (repo) history + revert. Endpoints mirror the web frontend exactly:
//   GET /api/v2.1/repos/:id/history/?page=&per_page=
//   PUT /api/v2.1/repos/:id/?operation=revert   { commit_id }   (revert whole repo)
//
// The history response is { data: RepoCommit[], more: boolean }. The FIRST
// commit in the list is the current version.

export interface RepoCommit {
  commit_id: string;
  description: string;
  // `time` is an ISO 8601 / parseable date string (NOT unix seconds).
  time: string;
  // Author display name + email. `name` may be empty for some commits.
  name: string;
  email: string;
  // Present when the commit is a merge (has no single author to attribute).
  second_parent_id?: string;
  client_version?: string;
  device_name?: string;
}

export interface RepoHistoryPage {
  commits: RepoCommit[];
  hasNextPage: boolean;
}

export async function getRepoHistory(
  repoId: string,
  page?: number,
  perPage?: number,
): Promise<RepoHistoryPage> {
  const params = new URLSearchParams();
  if (page) params.set('page', String(page));
  if (perPage) params.set('per_page', String(perPage));
  const qs = params.toString();
  const res = await fetch(
    `${serviceURL()}/api/v2.1/repos/${repoId}/history/${qs ? `?${qs}` : ''}`,
    { headers: authHeaders() },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to load history');
  }
  const data = await res.json();
  return {
    commits: (data.data ?? []) as RepoCommit[],
    hasNextPage: Boolean(data.more),
  };
}

/** Revert the entire library to a specific commit (snapshot). */
export async function revertRepoToCommit(repoId: string, commitId: string): Promise<void> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/?operation=revert`, {
    method: 'PUT',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ commit_id: commitId }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to restore version');
  }
  await invalidateApiCache(`/api2/repos/${repoId}`);
}
