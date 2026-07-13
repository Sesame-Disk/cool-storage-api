import type { SyncManifestEntry } from '../offlineDb';

// A single node discovered by scanning the local folder. `contentHash` is
// populated lazily — only for files the cheap size/mtime pass flags as diff
// candidates — so a full scan doesn't hash everything.
export interface LocalEntry {
  relPath: string;
  isDir: boolean;
  size: number;
  mtime: number;
  contentHash?: string | null;
}

// The remote mutations needed to make the library mirror the local folder,
// one-way local -> remote. This is the whole "sync, not copy" output: only
// changes, never the untouched majority.
export interface SyncPlan {
  mkdirs: string[]; // relPaths of new folders, shallowest-first
  uploads: LocalEntry[]; // new or changed files
  deletes: { relPath: string; isDir: boolean }[]; // gone locally, deepest-first
}

function depth(relPath: string): number {
  return relPath.split('/').length;
}

/**
 * Decide whether a local file differs from its last-synced manifest entry.
 *
 * Cheap first: a new file, or a size change, is unconditionally "changed".
 * Same size + same mtime is treated as unchanged (no hashing). Only when the
 * mtime moved but the size held do we consult content hashes — if both sides
 * carry a hash and they match, the touch was cosmetic and we skip the upload;
 * otherwise (hash differs, or we can't prove equality) we re-upload.
 */
function fileChanged(prev: SyncManifestEntry | undefined, local: LocalEntry): boolean {
  if (!prev) return true;
  if (prev.size !== local.size) return true;
  if (prev.mtime === local.mtime) return false;
  if (prev.contentHash && local.contentHash) {
    return prev.contentHash !== local.contentHash;
  }
  return true;
}

/**
 * Pure differential planner. Given the previous manifest and the current local
 * scan (both keyed by relPath), produce the minimal set of remote operations.
 * No browser/network dependencies so it is fully unit-testable.
 */
export function computeSyncPlan(
  prev: Map<string, SyncManifestEntry>,
  local: Map<string, LocalEntry>,
): SyncPlan {
  const mkdirs: string[] = [];
  const uploads: LocalEntry[] = [];
  const deletes: { relPath: string; isDir: boolean }[] = [];

  // Additions / modifications (local drives remote).
  for (const [relPath, entry] of local) {
    const prevEntry = prev.get(relPath);
    if (entry.isDir) {
      // New folder -> mkdir. An existing folder needs no operation.
      if (!prevEntry) mkdirs.push(relPath);
    } else if (fileChanged(prevEntry, entry)) {
      uploads.push(entry);
    }
  }

  // Deletions: anything previously synced that is no longer present locally.
  for (const [relPath, prevEntry] of prev) {
    if (!local.has(relPath)) {
      deletes.push({ relPath, isDir: prevEntry.isDir });
    }
  }

  // Order for safe application: create parents before children, and delete
  // children before parents.
  mkdirs.sort((a, b) => depth(a) - depth(b));
  uploads.sort((a, b) => depth(a.relPath) - depth(b.relPath));
  deletes.sort((a, b) => depth(b.relPath) - depth(a.relPath));

  return { mkdirs, uploads, deletes };
}
