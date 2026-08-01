import React, { useState } from 'react';
import { FolderSync, RefreshCw, Ban } from 'lucide-react';
import { supportsFolderSync } from '../../lib/sync/capabilities';
import { useSyncStatus } from '../../lib/sync/useSyncStatus';
import { syncLibrary, syncAllAuto, setAutoSync, unsync } from '../../lib/sync/syncEngine';
import SyncStatusBadge from '../libraries/SyncStatusBadge';
import EmptyState from '../ui/EmptyState';

function relativeTime(ts: number | null): string {
  if (!ts) return 'never';
  const secs = Math.round((Date.now() - ts) / 1000);
  if (secs < 60) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.round(hrs / 24)}d ago`;
}

export default function SyncStatusPage() {
  const configs = useSyncStatus();
  const [busy, setBusy] = useState(false);
  const list = Array.from(configs.values()).sort((a, b) => a.name.localeCompare(b.name));

  if (!supportsFolderSync()) {
    return (
      <div className="p-4">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text mb-4">Folder Sync</h1>
        <div
          className="flex items-start gap-3 rounded-lg border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 text-sm text-gray-600 dark:text-gray-300"
          data-testid="sync-unsupported"
        >
          <Ban className="w-5 h-5 flex-shrink-0 text-gray-400" />
          <p>
            Folder sync isn't available on this browser. It needs the File System Access API,
            supported on Chrome, Edge and Android — but not iOS Safari or Firefox. Open the app
            in a supported browser to sync local folders.
          </p>
        </div>
      </div>
    );
  }

  const handleSyncAll = async () => {
    setBusy(true);
    try {
      await syncAllAuto();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="p-4">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Folder Sync</h1>
        {list.length > 0 && (
          <button
            onClick={handleSyncAll}
            disabled={busy}
            className="flex items-center gap-1 text-sm text-primary font-medium min-h-[44px] disabled:opacity-50"
            data-testid="sync-all"
          >
            <RefreshCw size={16} className={busy ? 'animate-spin' : ''} />
            Sync now
          </button>
        )}
      </div>

      {list.length === 0 ? (
        <EmptyState
          icon={<FolderSync className="w-12 h-12" />}
          title="No synced folders"
          description="Long-press a library and choose “Sync this folder…” to keep a local folder mirrored to it."
        />
      ) : (
        <div className="flex flex-col gap-2" data-testid="sync-list">
          {list.map((cfg) => (
            <div
              key={cfg.libraryId}
              className="rounded-lg border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3"
              data-testid="sync-item"
            >
              <div className="flex items-center gap-2">
                <span className="flex-1 min-w-0">
                  <span className="block text-sm font-medium text-text dark:text-dark-text truncate">
                    {cfg.name}
                  </span>
                  <span className="block text-xs text-gray-500 dark:text-gray-400">
                    {cfg.status === 'error'
                      ? cfg.error || 'Sync error'
                      : `Synced ${relativeTime(cfg.lastSyncAt)}`}
                  </span>
                </span>
                <SyncStatusBadge status={cfg.status} withLabel />
              </div>
              <div className="mt-2 flex items-center gap-4">
                <button
                  onClick={() => syncLibrary(cfg.libraryId, { interactive: true })}
                  className="text-xs text-primary min-h-[44px]"
                >
                  Sync now
                </button>
                <button
                  onClick={() => setAutoSync(cfg.libraryId, !cfg.autoSync)}
                  className="text-xs text-gray-600 dark:text-gray-300 min-h-[44px]"
                >
                  {cfg.autoSync ? 'Pause' : 'Resume'}
                </button>
                <button
                  onClick={() => {
                    if (window.confirm(`Stop syncing "${cfg.name}"? Remote files are kept.`)) {
                      unsync(cfg.libraryId);
                    }
                  }}
                  className="text-xs text-red-600 dark:text-red-400 min-h-[44px]"
                >
                  Unsync
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
