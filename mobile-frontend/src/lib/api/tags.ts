import { serviceURL } from '../config';
import { authHeaders, invalidateApiCache } from '../api';

// Library (repo) tags. Endpoints mirror the web frontend exactly:
//   GET    /api/v2.1/repos/:id/repo-tags/            -> { repo_tags: [...] }
//   POST   /api/v2.1/repos/:id/repo-tags/            { name, color }  (JSON)
//   PUT    /api/v2.1/repos/:id/repo-tags/:tagId/     { name, color }  (JSON)
//   DELETE /api/v2.1/repos/:id/repo-tags/:tagId/
//
// The backend accepts both JSON and form bodies (`json:"name" form:"name"`);
// the web app (seafile-api.js listRepoTags/createRepoTag/updateRepoTag) sends
// JSON via axios, so we send JSON to match it exactly.
//
// Wire shape uses tag_name / tag_color / repo_tag_id; we normalise to a tidy
// RepoTag with id / name / color to match the web `RepoTag` model.

export interface RepoTag {
  id: number;
  name: string;
  color: string;
  fileCount: number;
}

function cachePath(repoId: string): string {
  return `/api/v2.1/repos/${repoId}/repo-tags`;
}

interface RepoTagWire {
  repo_tag_id: number;
  tag_name: string;
  tag_color: string;
  files_count?: number;
}

function normalize(t: RepoTagWire): RepoTag {
  return {
    id: t.repo_tag_id,
    name: t.tag_name,
    color: t.tag_color,
    fileCount: t.files_count ?? 0,
  };
}

/** List all tags defined in a library. */
export async function listRepoTags(repoId: string): Promise<RepoTag[]> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/`, {
    headers: authHeaders(),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to load tags');
  }
  const data = await res.json();
  return ((data.repo_tags ?? []) as RepoTagWire[]).map(normalize);
}

/** Create a new tag in the library. Color is a hex string (e.g. "#FF8000"). */
export async function createRepoTag(
  repoId: string,
  name: string,
  color: string,
): Promise<RepoTag> {
  const res = await fetch(`${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, color }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to create tag');
  }
  const data = await res.json();
  await invalidateApiCache(cachePath(repoId));
  return normalize(data.repo_tag as RepoTagWire);
}

/** Rename and/or recolor an existing tag. */
export async function updateRepoTag(
  repoId: string,
  tagId: number,
  fields: { name?: string; color?: string },
): Promise<RepoTag> {
  const res = await fetch(
    `${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/${tagId}/`,
    {
      method: 'PUT',
      headers: { ...authHeaders(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: fields.name, color: fields.color }),
    },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to update tag');
  }
  const data = await res.json();
  await invalidateApiCache(cachePath(repoId));
  return normalize(data.repo_tag as RepoTagWire);
}

/** Permanently delete a tag from the library. */
export async function deleteRepoTag(repoId: string, tagId: number): Promise<void> {
  const res = await fetch(
    `${serviceURL()}/api/v2.1/repos/${repoId}/repo-tags/${tagId}/`,
    {
      method: 'DELETE',
      headers: authHeaders(),
    },
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || data.error || 'Failed to delete tag');
  }
  await invalidateApiCache(cachePath(repoId));
}
