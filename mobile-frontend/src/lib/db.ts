/**
 * SQLite WASM database initialization.
 *
 * Uses @subframe7536/sqlite-wasm to provide persistent client-side SQL storage.
 * Prefers IDB-backed SQLite (best compatibility on main thread), with graceful
 * fallback to in-memory when WASM cannot load at all.
 *
 * Note: OPFS requires a Web Worker and is not used on the main thread.
 * The IDB backend (IDBBatchAtomicVFS) provides good persistence for all
 * browsers including Safari Private Browsing.
 */

import { initSQLite, useMemoryStorage, isIdbSupported } from '@subframe7536/sqlite-wasm';
import type { SQLiteDB } from '@subframe7536/sqlite-wasm';

export type { SQLiteDB };

export type DbTier = 'idb' | 'memory' | 'none';

export interface DbInitResult {
  db: SQLiteDB | null;
  tier: DbTier;
}

const SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS uploads (
  id TEXT PRIMARY KEY,
  repo_id TEXT NOT NULL,
  dir TEXT NOT NULL,
  file_name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  error TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  blob_key TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_status ON uploads(status);
CREATE INDEX IF NOT EXISTS idx_uploads_repo_dir ON uploads(repo_id, dir);
CREATE INDEX IF NOT EXISTS idx_uploads_created ON uploads(created_at);

CREATE TABLE IF NOT EXISTS share_ops (
  id TEXT PRIMARY KEY,
  repo_id TEXT NOT NULL,
  op_type TEXT NOT NULL,
  payload TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  error TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_share_ops_status ON share_ops(status);
CREATE INDEX IF NOT EXISTS idx_share_ops_repo ON share_ops(repo_id);
CREATE INDEX IF NOT EXISTS idx_share_ops_created ON share_ops(created_at);

CREATE TABLE IF NOT EXISTS cached_repos (
  key TEXT PRIMARY KEY,
  data TEXT NOT NULL,
  cached_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cached_dirents (
  key TEXT PRIMARY KEY,
  data TEXT NOT NULL,
  cached_at INTEGER NOT NULL
);
`;

let initPromise: Promise<DbInitResult> | null = null;

/**
 * Initialize SQLite WASM database. Tries IDB-backed storage first,
 * then in-memory SQLite, then returns null.
 * Result is cached; subsequent calls return the same promise.
 */
export function initDb(): Promise<DbInitResult> {
  if (initPromise) return initPromise;
  initPromise = doInit();
  return initPromise;
}

async function doInit(): Promise<DbInitResult> {
  // Skip WASM initialization in SSR / Node.js environments
  if (typeof window === 'undefined') {
    return { db: null, tier: 'none' };
  }

  // Try IDB-backed SQLite (good persistence, works on main thread)
  if (isIdbSupported()) {
    try {
      const { useIdbStorage } = await import('@subframe7536/sqlite-wasm/idb');
      const db = await initSQLite(useIdbStorage('sesamefs-queue.db'));
      await applyPragmasAndSchema(db);
      console.log('[db] SQLite WASM initialized with IDB backend');
      return { db, tier: 'idb' };
    } catch (err) {
      console.warn('[db] IDB SQLite init failed, trying memory fallback:', err);
    }
  }

  // Try in-memory SQLite (no persistence, but SQL still works)
  try {
    const opts = await useMemoryStorage();
    const db = await initSQLite(opts);
    await applyPragmasAndSchema(db);
    console.log('[db] SQLite WASM initialized with memory backend (no persistence)');
    return { db, tier: 'memory' };
  } catch (err) {
    console.warn('[db] Memory SQLite init failed, falling back to pure JS backend:', err);
  }

  return { db: null, tier: 'none' };
}

async function applyPragmasAndSchema(db: SQLiteDB): Promise<void> {
  await db.run('PRAGMA journal_mode = WAL;');
  await db.run('PRAGMA synchronous = NORMAL;');
  // Run schema in individual statements (some drivers don't support multi-statement)
  for (const stmt of SCHEMA_SQL.split(';').map(s => s.trim()).filter(Boolean)) {
    await db.run(stmt + ';');
  }
}

/**
 * Reset the cached init promise. Used in tests.
 */
export function resetDb(): void {
  initPromise = null;
}

/**
 * Create a fresh in-memory SQLite database for testing.
 * Does not go through the caching layer.
 */
export async function createTestDb(): Promise<SQLiteDB> {
  const db = await initSQLite(useMemoryStorage());
  await applyPragmasAndSchema(db);
  return db;
}
