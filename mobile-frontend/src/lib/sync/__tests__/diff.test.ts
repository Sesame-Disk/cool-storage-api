import { describe, it, expect } from 'vitest';
import { computeSyncPlan } from '../diff';
import type { LocalEntry } from '../diff';
import type { SyncManifestEntry } from '../../offlineDb';

function prevEntry(partial: Partial<SyncManifestEntry> & { relPath: string }): SyncManifestEntry {
  return {
    libraryId: 'lib',
    isDir: false,
    size: 0,
    mtime: 0,
    contentHash: null,
    remoteId: null,
    state: 'synced',
    lastSyncedAt: 0,
    ...partial,
  };
}

function prevMap(entries: SyncManifestEntry[]): Map<string, SyncManifestEntry> {
  return new Map(entries.map((e) => [e.relPath, e]));
}

function localMap(entries: LocalEntry[]): Map<string, LocalEntry> {
  return new Map(entries.map((e) => [e.relPath, e]));
}

describe('computeSyncPlan', () => {
  it('uploads a brand-new file', () => {
    const plan = computeSyncPlan(
      prevMap([]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 10, mtime: 100 }]),
    );
    expect(plan.uploads.map((u) => u.relPath)).toEqual(['a.txt']);
    expect(plan.mkdirs).toEqual([]);
    expect(plan.deletes).toEqual([]);
  });

  it('mkdirs a brand-new folder but not an existing one', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'existing', isDir: true })]),
      localMap([
        { relPath: 'existing', isDir: true, size: 0, mtime: 0 },
        { relPath: 'fresh', isDir: true, size: 0, mtime: 0 },
      ]),
    );
    expect(plan.mkdirs).toEqual(['fresh']);
  });

  it('re-uploads a file whose size changed', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'a.txt', size: 10, mtime: 100 })]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 20, mtime: 100 }]),
    );
    expect(plan.uploads.map((u) => u.relPath)).toEqual(['a.txt']);
  });

  it('skips a file with identical size and mtime (no hashing)', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'a.txt', size: 10, mtime: 100 })]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 10, mtime: 100 }]),
    );
    expect(plan.uploads).toEqual([]);
  });

  it('skips when mtime moved but content hash proves the content is unchanged', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'a.txt', size: 10, mtime: 100, contentHash: 'deadbeef' })]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 10, mtime: 200, contentHash: 'deadbeef' }]),
    );
    expect(plan.uploads).toEqual([]);
  });

  it('re-uploads when mtime moved and the content hash differs', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'a.txt', size: 10, mtime: 100, contentHash: 'deadbeef' })]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 10, mtime: 200, contentHash: 'feedface' }]),
    );
    expect(plan.uploads.map((u) => u.relPath)).toEqual(['a.txt']);
  });

  it('re-uploads when mtime moved and no hash is available to prove equality', () => {
    const plan = computeSyncPlan(
      prevMap([prevEntry({ relPath: 'a.txt', size: 10, mtime: 100, contentHash: null })]),
      localMap([{ relPath: 'a.txt', isDir: false, size: 10, mtime: 200 }]),
    );
    expect(plan.uploads.map((u) => u.relPath)).toEqual(['a.txt']);
  });

  it('deletes files and folders that vanished locally', () => {
    const plan = computeSyncPlan(
      prevMap([
        prevEntry({ relPath: 'gone.txt' }),
        prevEntry({ relPath: 'olddir', isDir: true }),
      ]),
      localMap([]),
    );
    // Same depth -> order among siblings is not significant; assert as a set.
    expect(plan.deletes).toEqual(
      expect.arrayContaining([
        { relPath: 'olddir', isDir: true },
        { relPath: 'gone.txt', isDir: false },
      ]),
    );
    expect(plan.deletes).toHaveLength(2);
  });

  it('orders mkdirs parent-first and uploads shallow-first', () => {
    const plan = computeSyncPlan(
      prevMap([]),
      localMap([
        { relPath: 'a/b/c', isDir: true, size: 0, mtime: 0 },
        { relPath: 'a', isDir: true, size: 0, mtime: 0 },
        { relPath: 'a/b', isDir: true, size: 0, mtime: 0 },
        { relPath: 'a/b/c/deep.txt', isDir: false, size: 1, mtime: 1 },
        { relPath: 'top.txt', isDir: false, size: 1, mtime: 1 },
      ]),
    );
    expect(plan.mkdirs).toEqual(['a', 'a/b', 'a/b/c']);
    expect(plan.uploads.map((u) => u.relPath)).toEqual(['top.txt', 'a/b/c/deep.txt']);
  });

  it('orders deletes deepest-first so folders empty before removal', () => {
    const plan = computeSyncPlan(
      prevMap([
        prevEntry({ relPath: 'a', isDir: true }),
        prevEntry({ relPath: 'a/b', isDir: true }),
        prevEntry({ relPath: 'a/b/file.txt' }),
      ]),
      localMap([]),
    );
    expect(plan.deletes).toEqual([
      { relPath: 'a/b/file.txt', isDir: false },
      { relPath: 'a/b', isDir: true },
      { relPath: 'a', isDir: true },
    ]);
  });

  it('produces an empty plan when nothing changed', () => {
    const prev = prevMap([
      prevEntry({ relPath: 'dir', isDir: true }),
      prevEntry({ relPath: 'dir/a.txt', size: 5, mtime: 50 }),
    ]);
    const local = localMap([
      { relPath: 'dir', isDir: true, size: 0, mtime: 0 },
      { relPath: 'dir/a.txt', isDir: false, size: 5, mtime: 50 },
    ]);
    const plan = computeSyncPlan(prev, local);
    expect(plan).toEqual({ mkdirs: [], uploads: [], deletes: [] });
  });
});
