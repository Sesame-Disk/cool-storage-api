import { openDB } from 'idb';
import type { DBSchema, IDBPDatabase } from 'idb';
import type { Repo, Dirent } from './models';

interface SesameFSDB extends DBSchema {
  repos: {
    key: string;
    value: { data: Repo[]; cachedAt: number };
  };
  dirents: {
    key: string;
    value: { data: Dirent[]; cachedAt: number };
  };
  starred: {
    key: string;
    value: { data: unknown[]; cachedAt: number };
  };
  pendingUploads: {
    key: string;
    value: {
      id: string;
      repoId: string;
      path: string;
      fileName: string;
      fileData: ArrayBuffer;
      createdAt: number;
    };
  };
  // --- Folder sync (Seafile-style local->remote) ---
  // A FileSystemDirectoryHandle is not JSON/SQL-serializable but IS
  // IndexedDB structured-cloneable, so the picked folder lives here.
  syncHandles: {
    key: string; // libraryId
    value: { libraryId: string; handle: FileSystemDirectoryHandle };
  };
  // The per-library manifest — "one cached partition per library", keyed
  // `${libraryId}:${relPath}` and iterable by the `libraryId:` prefix.
  syncManifests: {
    key: string; // `${libraryId}:${relPath}`
    value: SyncManifestEntry;
  };
  syncConfig: {
    key: string; // libraryId
    value: SyncConfigEntry;
  };
}

export interface SyncManifestEntry {
  libraryId: string;
  relPath: string;
  isDir: boolean;
  size: number;
  mtime: number;
  contentHash: string | null;
  remoteId: string | null;
  state: 'synced' | 'pending' | 'error';
  lastSyncedAt: number;
}

export type SyncStatus = 'idle' | 'syncing' | 'synced' | 'paused' | 'error';

export interface SyncConfigEntry {
  libraryId: string;
  name: string;
  autoSync: boolean;
  status: SyncStatus;
  lastSyncAt: number | null;
  error: string | null;
}

const DB_NAME = 'sesamefs-offline';
const DB_VERSION = 2;

let dbPromise: Promise<IDBPDatabase<SesameFSDB>> | null = null;

function getDb(): Promise<IDBPDatabase<SesameFSDB>> {
  if (!dbPromise) {
    dbPromise = openDB<SesameFSDB>(DB_NAME, DB_VERSION, {
      upgrade(db) {
        if (!db.objectStoreNames.contains('repos')) {
          db.createObjectStore('repos');
        }
        if (!db.objectStoreNames.contains('dirents')) {
          db.createObjectStore('dirents');
        }
        if (!db.objectStoreNames.contains('starred')) {
          db.createObjectStore('starred');
        }
        if (!db.objectStoreNames.contains('pendingUploads')) {
          db.createObjectStore('pendingUploads', { keyPath: 'id' });
        }
        if (!db.objectStoreNames.contains('syncHandles')) {
          db.createObjectStore('syncHandles', { keyPath: 'libraryId' });
        }
        if (!db.objectStoreNames.contains('syncManifests')) {
          // Keyed `${libraryId}:${relPath}` so a single store partitions per
          // library via IDBKeyRange.bound on the `libraryId:` prefix.
          db.createObjectStore('syncManifests');
        }
        if (!db.objectStoreNames.contains('syncConfig')) {
          db.createObjectStore('syncConfig', { keyPath: 'libraryId' });
        }
      },
    });
  }
  return dbPromise;
}

export async function cacheRepos(repos: Repo[]): Promise<void> {
  const db = await getDb();
  await db.put('repos', { data: repos, cachedAt: Date.now() }, 'all');
}

export async function getCachedRepos(): Promise<Repo[] | null> {
  const db = await getDb();
  const entry = await db.get('repos', 'all');
  return entry ? entry.data : null;
}

export async function cacheDirents(repoId: string, path: string, dirents: Dirent[]): Promise<void> {
  const db = await getDb();
  const key = `${repoId}:${path}`;
  await db.put('dirents', { data: dirents, cachedAt: Date.now() }, key);
}

export async function getCachedDirents(repoId: string, path: string): Promise<Dirent[] | null> {
  const db = await getDb();
  const key = `${repoId}:${path}`;
  const entry = await db.get('dirents', key);
  return entry ? entry.data : null;
}

export async function addPendingUpload(upload: {
  id: string;
  repoId: string;
  path: string;
  fileName: string;
  fileData: ArrayBuffer;
}): Promise<void> {
  const db = await getDb();
  await db.put('pendingUploads', { ...upload, createdAt: Date.now() });
}

export async function getPendingUploads() {
  const db = await getDb();
  return db.getAll('pendingUploads');
}

export async function removePendingUpload(id: string): Promise<void> {
  const db = await getDb();
  await db.delete('pendingUploads', id);
}

// --------------------------------------------------------------------------
// Folder sync
// --------------------------------------------------------------------------

/** Persist the picked local folder handle for a library. */
export async function saveSyncHandle(libraryId: string, handle: FileSystemDirectoryHandle): Promise<void> {
  const db = await getDb();
  await db.put('syncHandles', { libraryId, handle });
}

export async function getSyncHandle(libraryId: string): Promise<FileSystemDirectoryHandle | null> {
  const db = await getDb();
  const entry = await db.get('syncHandles', libraryId);
  return entry ? entry.handle : null;
}

export async function deleteSyncHandle(libraryId: string): Promise<void> {
  const db = await getDb();
  await db.delete('syncHandles', libraryId);
}

/** Load a library's whole manifest (map keyed by relPath). */
export async function getSyncManifest(libraryId: string): Promise<Map<string, SyncManifestEntry>> {
  const db = await getDb();
  const range = IDBKeyRange.bound(`${libraryId}:`, `${libraryId}:￿`);
  const entries = await db.getAll('syncManifests', range);
  return new Map(entries.map((e) => [e.relPath, e]));
}

/** Replace a library's manifest with a fresh set of entries (atomic-ish). */
export async function putSyncManifest(libraryId: string, entries: SyncManifestEntry[]): Promise<void> {
  const db = await getDb();
  const tx = db.transaction('syncManifests', 'readwrite');
  const range = IDBKeyRange.bound(`${libraryId}:`, `${libraryId}:￿`);
  let cursor = await tx.store.openCursor(range);
  while (cursor) {
    await cursor.delete();
    cursor = await cursor.continue();
  }
  for (const e of entries) {
    await tx.store.put(e, `${libraryId}:${e.relPath}`);
  }
  await tx.done;
}

export async function clearSyncManifest(libraryId: string): Promise<void> {
  await putSyncManifest(libraryId, []);
}

export async function getSyncConfig(libraryId: string): Promise<SyncConfigEntry | null> {
  const db = await getDb();
  return (await db.get('syncConfig', libraryId)) ?? null;
}

export async function getAllSyncConfigs(): Promise<SyncConfigEntry[]> {
  const db = await getDb();
  return db.getAll('syncConfig');
}

export async function putSyncConfig(config: SyncConfigEntry): Promise<void> {
  const db = await getDb();
  await db.put('syncConfig', config);
}

export async function deleteSyncConfig(libraryId: string): Promise<void> {
  const db = await getDb();
  await db.delete('syncConfig', libraryId);
}
