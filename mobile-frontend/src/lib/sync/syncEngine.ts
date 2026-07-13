import * as api from '../api';
import { uploadManager } from '../upload';
import * as db from '../offlineDb';
import type { SyncConfigEntry, SyncManifestEntry } from '../offlineDb';
import { computeSyncPlan } from './diff';
import type { LocalEntry, SyncPlan } from './diff';
import { scanLocal, hashFile } from './scanLocal';

// FileSystemHandle permission methods are Chromium-only and missing from the
// DOM lib types — narrow locally.
type PermFn = (opts: { mode: 'read' | 'readwrite' }) => Promise<PermissionState>;
type PermissionedHandle = FileSystemDirectoryHandle & {
  queryPermission?: PermFn;
  requestPermission?: PermFn;
};

// --- Status notifications for the UI ---------------------------------------
type Listener = (configs: SyncConfigEntry[]) => void;
const listeners = new Set<Listener>();

export function subscribeSync(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

async function notify(): Promise<void> {
  const all = await db.getAllSyncConfigs();
  for (const l of listeners) l(all);
}

async function setStatus(
  libraryId: string,
  patch: Partial<SyncConfigEntry>,
): Promise<void> {
  const current = await db.getSyncConfig(libraryId);
  if (!current) return;
  await db.putSyncConfig({ ...current, ...patch });
  await notify();
}

// One in-flight sync per library at a time.
const running = new Set<string>();

// --- Public control surface ------------------------------------------------

/** Begin syncing a library to an already-picked local folder handle. */
export async function enableSync(
  libraryId: string,
  name: string,
  handle: FileSystemDirectoryHandle,
): Promise<void> {
  await db.saveSyncHandle(libraryId, handle);
  await db.putSyncConfig({
    libraryId,
    name,
    autoSync: true,
    status: 'idle',
    lastSyncAt: null,
    error: null,
  });
  await notify();
  await syncLibrary(libraryId, { interactive: true });
}

/** Stop syncing a library and forget its manifest + handle. */
export async function unsync(libraryId: string): Promise<void> {
  await db.deleteSyncConfig(libraryId);
  await db.deleteSyncHandle(libraryId);
  await db.clearSyncManifest(libraryId);
  await notify();
}

/** Pause (disable auto-sync) or resume a library. */
export async function setAutoSync(libraryId: string, autoSync: boolean): Promise<void> {
  await setStatus(libraryId, {
    autoSync,
    status: autoSync ? 'idle' : 'paused',
  });
}

// --- The sync pass ---------------------------------------------------------

async function ensureReadPermission(
  handle: FileSystemDirectoryHandle,
  interactive: boolean,
): Promise<boolean> {
  const h = handle as PermissionedHandle;
  if (!h.queryPermission) return true; // permissions API absent -> assume ok
  const opts = { mode: 'read' as const };
  if ((await h.queryPermission(opts)) === 'granted') return true;
  // Requesting permission requires a user gesture; only viable when the user
  // explicitly triggered this pass (manual "Sync now").
  if (interactive && h.requestPermission) {
    return (await h.requestPermission(opts)) === 'granted';
  }
  return false;
}

/**
 * Run one differential sync pass for a library (one-way local -> remote).
 * Serialized per library; a no-op if offline, paused (unless interactive), or
 * already running.
 */
export async function syncLibrary(
  libraryId: string,
  opts: { interactive?: boolean } = {},
): Promise<void> {
  const interactive = opts.interactive ?? false;

  if (running.has(libraryId)) return;
  if (typeof navigator !== 'undefined' && navigator.onLine === false) return;

  const config = await db.getSyncConfig(libraryId);
  if (!config) return;
  if (!config.autoSync && !interactive) return; // paused: only manual runs

  const handle = await db.getSyncHandle(libraryId);
  if (!handle) {
    await setStatus(libraryId, { status: 'error', error: 'Local folder is no longer available' });
    return;
  }

  running.add(libraryId);
  try {
    if (!(await ensureReadPermission(handle, interactive))) {
      await setStatus(libraryId, {
        status: 'error',
        error: 'Permission to read the local folder was not granted',
      });
      return;
    }

    await setStatus(libraryId, { status: 'syncing', error: null });

    const { entries, fileHandles } = await scanLocal(handle);
    const prev = await db.getSyncManifest(libraryId);

    // Hash only the ambiguous candidates: a file whose size is unchanged but
    // whose mtime moved, and for which we have a previous hash to compare
    // against. Everything else the cheap size/mtime pass settles.
    for (const [relPath, entry] of entries) {
      if (entry.isDir) continue;
      const p = prev.get(relPath);
      if (p && !p.isDir && p.size === entry.size && p.mtime !== entry.mtime && p.contentHash) {
        const fh = fileHandles.get(relPath);
        if (fh) entry.contentHash = await hashFile(await fh.getFile());
      }
    }

    const plan = computeSyncPlan(prev, entries);
    await applyPlan(libraryId, plan, fileHandles);

    await db.putSyncManifest(libraryId, buildManifest(libraryId, entries, prev, plan));
    await setStatus(libraryId, {
      status: 'synced',
      lastSyncAt: Date.now(),
      error: null,
    });
  } catch (err) {
    await setStatus(libraryId, {
      status: 'error',
      error: err instanceof Error ? err.message : 'Sync failed',
    });
  } finally {
    running.delete(libraryId);
  }
}

// --- Applying a plan to the remote -----------------------------------------

function remoteParent(relPath: string): string {
  const slash = relPath.lastIndexOf('/');
  return slash === -1 ? '/' : `/${relPath.slice(0, slash)}`;
}

async function applyPlan(
  libraryId: string,
  plan: SyncPlan,
  fileHandles: Map<string, FileSystemFileHandle>,
): Promise<void> {
  // 1. Create new folders parent-first.
  for (const relPath of plan.mkdirs) {
    await api.mkdir(libraryId, `/${relPath}`);
  }

  // 2. Upload new/changed files, grouped by remote parent dir (all parents now
  //    exist). Reuses the modern block/multipart uploadManager unchanged.
  const byParent = new Map<string, LocalEntry[]>();
  for (const up of plan.uploads) {
    const parent = remoteParent(up.relPath);
    (byParent.get(parent) ?? byParent.set(parent, []).get(parent)!).push(up);
  }
  for (const [parent, ups] of byParent) {
    const files: File[] = [];
    for (const up of ups) {
      const fh = fileHandles.get(up.relPath);
      if (fh) files.push(await fh.getFile());
    }
    if (files.length) await uploadFiles(libraryId, files, parent);
  }

  // 3. Delete what disappeared locally, deepest-first.
  for (const del of plan.deletes) {
    if (del.isDir) await api.deleteDir(libraryId, `/${del.relPath}`);
    else await api.deleteFile(libraryId, `/${del.relPath}`);
  }
}

/** Push a batch of files through uploadManager and await terminal states. */
function uploadFiles(repoId: string, files: File[], parentDir: string): Promise<void> {
  const queued = uploadManager.addFiles(files, repoId, parentDir);
  const ids = new Set(queued.map((f) => f.id));
  const failed: string[] = [];

  return new Promise<void>((resolve, reject) => {
    const check = () => {
      if (ids.size === 0) {
        unsub();
        if (failed.length) reject(new Error(`${failed.length} file(s) failed to upload`));
        else resolve();
      }
    };
    const unsub = uploadManager.subscribe((event) => {
      if (!ids.has(event.fileId)) return;
      if (event.type === 'completed') {
        ids.delete(event.fileId);
        check();
      } else if (event.type === 'failed' || event.type === 'cancelled') {
        failed.push(event.fileId);
        ids.delete(event.fileId);
        check();
      }
    });
    check();
  });
}

/** Build the fresh manifest that reflects the post-sync remote state. */
function buildManifest(
  libraryId: string,
  entries: Map<string, LocalEntry>,
  prev: Map<string, SyncManifestEntry>,
  plan: SyncPlan,
): SyncManifestEntry[] {
  const uploaded = new Set(plan.uploads.map((u) => u.relPath));
  const now = Date.now();
  const out: SyncManifestEntry[] = [];
  for (const [relPath, entry] of entries) {
    const p = prev.get(relPath);
    // Prefer a freshly computed hash, else carry the previous one (unchanged
    // files), else null (a new/size-changed upload we didn't hash).
    const contentHash = entry.contentHash ?? (uploaded.has(relPath) ? null : p?.contentHash ?? null);
    out.push({
      libraryId,
      relPath,
      isDir: entry.isDir,
      size: entry.size,
      mtime: entry.mtime,
      contentHash,
      remoteId: p?.remoteId ?? null,
      state: 'synced',
      lastSyncedAt: now,
    });
  }
  return out;
}

/** Run a sync pass for every library that has auto-sync enabled. */
export async function syncAllAuto(): Promise<void> {
  const configs = await db.getAllSyncConfigs();
  for (const c of configs) {
    if (c.autoSync) await syncLibrary(c.libraryId);
  }
}
