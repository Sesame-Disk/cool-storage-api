import { describe, it, expect, beforeEach } from 'vitest';
import 'fake-indexeddb/auto';
import {
  saveSyncHandle,
  getSyncHandle,
  deleteSyncHandle,
  getSyncManifest,
  putSyncManifest,
  clearSyncManifest,
  getSyncConfig,
  putSyncConfig,
  getAllSyncConfigs,
  deleteSyncConfig,
} from '../../offlineDb';
import type { SyncManifestEntry, SyncConfigEntry } from '../../offlineDb';

function entry(relPath: string, over: Partial<SyncManifestEntry> = {}): SyncManifestEntry {
  return {
    libraryId: 'lib-1',
    relPath,
    isDir: false,
    size: 1,
    mtime: 1,
    contentHash: null,
    remoteId: null,
    state: 'synced',
    lastSyncedAt: 1,
    ...over,
  };
}

const config: SyncConfigEntry = {
  libraryId: 'lib-1',
  name: 'Docs',
  autoSync: true,
  status: 'idle',
  lastSyncAt: null,
  error: null,
};

describe('offlineDb folder-sync helpers', () => {
  beforeEach(async () => {
    await clearSyncManifest('lib-1');
    await clearSyncManifest('lib-2');
    await deleteSyncConfig('lib-1');
    await deleteSyncConfig('lib-2');
    await deleteSyncHandle('lib-1');
  });

  it('stores and retrieves a folder handle', async () => {
    const fakeHandle = { name: 'my-folder', kind: 'directory' } as unknown as FileSystemDirectoryHandle;
    await saveSyncHandle('lib-1', fakeHandle);
    const got = await getSyncHandle('lib-1');
    expect(got).toMatchObject({ name: 'my-folder', kind: 'directory' });
    await deleteSyncHandle('lib-1');
    expect(await getSyncHandle('lib-1')).toBeNull();
  });

  it('writes and reads back a per-library manifest keyed by relPath', async () => {
    await putSyncManifest('lib-1', [entry('a.txt'), entry('sub', { isDir: true }), entry('sub/b.txt')]);
    const manifest = await getSyncManifest('lib-1');
    expect(manifest.size).toBe(3);
    expect(manifest.get('sub')?.isDir).toBe(true);
    expect(manifest.get('sub/b.txt')?.relPath).toBe('sub/b.txt');
  });

  it('partitions manifests per library', async () => {
    await putSyncManifest('lib-1', [entry('only-1.txt')]);
    await putSyncManifest('lib-2', [entry('only-2.txt', { libraryId: 'lib-2' })]);
    expect([...(await getSyncManifest('lib-1')).keys()]).toEqual(['only-1.txt']);
    expect([...(await getSyncManifest('lib-2')).keys()]).toEqual(['only-2.txt']);
  });

  it('replaces the manifest wholesale (drops removed entries)', async () => {
    await putSyncManifest('lib-1', [entry('a.txt'), entry('b.txt')]);
    await putSyncManifest('lib-1', [entry('a.txt')]);
    const manifest = await getSyncManifest('lib-1');
    expect([...manifest.keys()]).toEqual(['a.txt']);
  });

  it('clears a manifest', async () => {
    await putSyncManifest('lib-1', [entry('a.txt')]);
    await clearSyncManifest('lib-1');
    expect((await getSyncManifest('lib-1')).size).toBe(0);
  });

  it('creates, reads, lists and deletes sync config', async () => {
    await putSyncConfig(config);
    expect(await getSyncConfig('lib-1')).toMatchObject({ name: 'Docs', autoSync: true });
    await putSyncConfig({ ...config, libraryId: 'lib-2', name: 'Photos' });
    const all = await getAllSyncConfigs();
    expect(all.map((c) => c.libraryId).sort()).toEqual(['lib-1', 'lib-2']);
    await deleteSyncConfig('lib-1');
    expect(await getSyncConfig('lib-1')).toBeNull();
  });
});
