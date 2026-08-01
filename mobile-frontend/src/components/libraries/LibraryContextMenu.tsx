import React from 'react';
import { FolderOpen, Share2, Info, FolderSync, RefreshCw, Pause, Play, Unlink, Ban } from 'lucide-react';
import BottomSheet from '../ui/BottomSheet';
import type { Repo } from '../../lib/models';
import type { SyncStatus } from '../../lib/offlineDb';
import { supportsFolderSync } from '../../lib/sync/capabilities';

interface LibraryContextMenuProps {
  isOpen: boolean;
  onClose: () => void;
  repo: Repo | null;
  onOpen: (repo: Repo) => void;
  onShare: (repo: Repo) => void;
  onDetails: (repo: Repo) => void;
  // Folder sync — optional so callers that don't wire it degrade gracefully.
  syncStatus?: SyncStatus; // undefined => not synced
  autoSync?: boolean;
  onSyncStart?: (repo: Repo) => void;
  onSyncNow?: (repo: Repo) => void;
  onSyncPauseResume?: (repo: Repo) => void;
  onUnsync?: (repo: Repo) => void;
}

const baseActions = [
  { key: 'open', label: 'Open', icon: FolderOpen, action: 'onOpen' as const },
  { key: 'share', label: 'Share', icon: Share2, action: 'onShare' as const },
  { key: 'details', label: 'Details', icon: Info, action: 'onDetails' as const },
];

const rowClass =
  'flex items-center gap-3 px-2 py-3 min-h-[44px] text-text dark:text-dark-text hover:bg-gray-50 dark:hover:bg-dark-border rounded-lg w-full';

export default function LibraryContextMenu({
  isOpen,
  onClose,
  repo,
  onOpen,
  onShare,
  onDetails,
  syncStatus,
  autoSync,
  onSyncStart,
  onSyncNow,
  onSyncPauseResume,
  onUnsync,
}: LibraryContextMenuProps) {
  const handlers = { onOpen, onShare, onDetails };
  const canSync = supportsFolderSync();
  const isSynced = syncStatus !== undefined;

  const run = (fn?: (repo: Repo) => void) => {
    if (repo && fn) fn(repo);
    onClose();
  };

  return (
    <BottomSheet isOpen={isOpen} onClose={onClose} title={repo?.repo_name}>
      <div className="flex flex-col">
        {baseActions.map(({ key, label, icon: Icon, action }) => (
          <button
            key={key}
            onClick={() => {
              if (repo) handlers[action](repo);
              onClose();
            }}
            className={rowClass}
          >
            <Icon size={20} />
            <span className="text-sm">{label}</span>
          </button>
        ))}

        {/* Folder sync — hidden entirely where the File System Access API
            isn't available (iOS Safari / Firefox), with a single disabled row
            so the capability gap is visible rather than silent. */}
        <div className="my-1 border-t border-gray-100 dark:border-dark-border" />
        {!canSync ? (
          <div
            className="flex items-center gap-3 px-2 py-3 min-h-[44px] text-gray-400 cursor-not-allowed"
            data-testid="sync-unavailable"
          >
            <Ban size={20} />
            <span className="text-sm">Folder sync unavailable on this browser</span>
          </div>
        ) : !isSynced ? (
          <button className={rowClass} data-testid="sync-start" onClick={() => run(onSyncStart)}>
            <FolderSync size={20} />
            <span className="text-sm">Sync this folder…</span>
          </button>
        ) : (
          <>
            <button className={rowClass} data-testid="sync-now" onClick={() => run(onSyncNow)}>
              <RefreshCw size={20} />
              <span className="text-sm">Sync now</span>
            </button>
            <button
              className={rowClass}
              data-testid="sync-pause-resume"
              onClick={() => run(onSyncPauseResume)}
            >
              {autoSync ? <Pause size={20} /> : <Play size={20} />}
              <span className="text-sm">{autoSync ? 'Pause syncing' : 'Resume syncing'}</span>
            </button>
            <button
              className={`${rowClass} text-red-600 dark:text-red-400`}
              data-testid="sync-unsync"
              onClick={() => run(onUnsync)}
            >
              <Unlink size={20} />
              <span className="text-sm">Unsync</span>
            </button>
          </>
        )}
      </div>
    </BottomSheet>
  );
}
