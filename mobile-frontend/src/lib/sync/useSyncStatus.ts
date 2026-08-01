import { useState, useEffect } from 'react';
import { getAllSyncConfigs } from '../offlineDb';
import type { SyncConfigEntry } from '../offlineDb';
import { subscribeSync } from './syncEngine';

/** Live map of libraryId -> sync config, kept current via the engine's
 * status notifications. Empty until the first read resolves. */
export function useSyncStatus(): Map<string, SyncConfigEntry> {
  const [configs, setConfigs] = useState<Map<string, SyncConfigEntry>>(new Map());

  useEffect(() => {
    let active = true;
    const apply = (list: SyncConfigEntry[]) => {
      if (active) setConfigs(new Map(list.map((c) => [c.libraryId, c])));
    };
    getAllSyncConfigs().then(apply).catch(() => {});
    const unsub = subscribeSync(apply);
    return () => {
      active = false;
      unsub();
    };
  }, []);

  return configs;
}
