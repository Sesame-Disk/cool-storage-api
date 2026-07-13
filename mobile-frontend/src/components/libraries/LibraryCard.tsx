import React, { useRef } from 'react';
import { Library, Lock } from 'lucide-react';
import type { Repo } from '../../lib/models';
import { bytesToSize } from '../../lib/models';
import type { SyncStatus } from '../../lib/offlineDb';
import SyncStatusBadge from './SyncStatusBadge';

interface LibraryCardProps {
  repo: Repo;
  onTap: (repo: Repo) => void;
  onLongPress?: (repo: Repo) => void;
  syncStatus?: SyncStatus;
}

export default function LibraryCard({ repo, onTap, onLongPress, syncStatus }: LibraryCardProps) {
  const longPressTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const handlePointerDown = () => {
    if (!onLongPress) return;
    longPressTimer.current = setTimeout(() => {
      onLongPress(repo);
    }, 500);
  };

  const handlePointerUp = () => {
    if (longPressTimer.current) {
      clearTimeout(longPressTimer.current);
      longPressTimer.current = null;
    }
  };

  const lastModified = repo.last_modified
    ? new Date(repo.last_modified).toLocaleDateString()
    : '';

  return (
    <button
      onClick={() => onTap(repo)}
      onPointerDown={handlePointerDown}
      onPointerUp={handlePointerUp}
      onPointerLeave={handlePointerUp}
      className="w-full flex items-center gap-3 px-4 py-3 min-h-[56px] hover:bg-gray-50 dark:hover:bg-dark-border active:bg-gray-50 dark:active:bg-dark-border text-left border-b border-gray-100 dark:border-dark-border"
    >
      <Library className="w-10 h-10 text-primary flex-shrink-0" />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text dark:text-dark-text truncate">{repo.repo_name}</p>
        <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
          {repo.owner_name}
        </p>
        <p className="text-xs text-gray-400 dark:text-gray-500">
          {bytesToSize(repo.size)}{lastModified ? ` · ${lastModified}` : ''}
        </p>
      </div>
      {repo.encrypted && <Lock className="w-4 h-4 text-gray-400 flex-shrink-0" />}
      {syncStatus && <SyncStatusBadge status={syncStatus} />}
    </button>
  );
}
