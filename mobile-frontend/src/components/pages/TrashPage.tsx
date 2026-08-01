import React, { useState, useEffect, useCallback } from 'react';
import { Trash2, RotateCcw, File, Folder, ChevronLeft } from 'lucide-react';
import { listTrash, restoreTrashItem, cleanTrash } from '../../lib/api/trash';
import type { TrashItem } from '../../lib/api/trash';
import { bytesToSize } from '../../lib/models';

// Trash deleted_time is an ISO 8601 string (not a unix timestamp like mtime).
function formatDeleted(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? '' : d.toLocaleString();
}
import SwipeableListItem from '../ui/SwipeableListItem';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

function repoIdFromUrl(): string {
  // /libraries/<repoId>/trash
  const parts = window.location.pathname.split('/').filter(Boolean);
  const idx = parts.indexOf('libraries');
  return idx >= 0 ? (parts[idx + 1] ?? '') : '';
}

export default function TrashPage() {
  const [repoId, setRepoId] = useState('');
  const [items, setItems] = useState<TrashItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [confirmClean, setConfirmClean] = useState(false);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const fetchTrash = useCallback(async (id: string) => {
    try {
      const page = await listTrash(id);
      setItems(page.items);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load trash');
    }
  }, []);

  useEffect(() => {
    const id = repoIdFromUrl();
    setRepoId(id);
    fetchTrash(id).finally(() => setLoading(false));
  }, [fetchTrash]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchTrash(repoId);
    setRefreshing(false);
  };

  const handleRestore = async (item: TrashItem) => {
    try {
      await restoreTrashItem(repoId, item);
      setItems((prev) =>
        prev.filter((t) => !(t.obj_name === item.obj_name && t.parent_dir === item.parent_dir && t.commit_id === item.commit_id)),
      );
      showToast(`Restored ${item.obj_name}`);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Restore failed');
    }
  };

  const handleClean = async () => {
    setConfirmClean(false);
    try {
      await cleanTrash(repoId);
      setItems([]);
      showToast('Trash emptied');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to empty trash');
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="trash-page">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <a href={`/libraries/${repoId}/`} aria-label="Back" className="p-1 -ml-1 text-gray-500 min-h-[44px] flex items-center">
            <ChevronLeft className="w-6 h-6" />
          </a>
          <h1 className="text-xl font-semibold text-text dark:text-dark-text truncate">Trash</h1>
        </div>
        <div className="flex items-center gap-3">
          <button onClick={handleRefresh} disabled={refreshing} className="text-sm text-primary font-medium min-h-[44px]">
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
          {items.length > 0 && (
            <button
              onClick={() => setConfirmClean(true)}
              data-testid="trash-clean"
              className="text-sm text-red-500 font-medium min-h-[44px]"
            >
              Empty
            </button>
          )}
        </div>
      </div>

      {error && items.length === 0 ? (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <p role="alert" className="text-red-500 mb-4">{error}</p>
          <button onClick={handleRefresh} className="text-primary font-medium min-h-[44px]">Retry</button>
        </div>
      ) : (
        <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
          {items.length === 0 ? (
            <EmptyState
              icon={<Trash2 className="w-12 h-12" />}
              title="Trash is empty"
              description="Deleted files and folders in this library will appear here and can be restored."
            />
          ) : (
            <div className="flex flex-col pb-20" data-testid="trash-list">
              {items.map((item) => (
                <SwipeableListItem
                  key={`${item.commit_id}:${item.parent_dir}${item.obj_name}`}
                  rightActions={[
                    {
                      icon: <RotateCcw className="w-5 h-5" />,
                      label: 'Restore',
                      color: '#10b981',
                      onClick: () => handleRestore(item),
                    },
                  ]}
                >
                  <div className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border">
                    {item.is_dir ? (
                      <Folder className="w-10 h-10 text-yellow-500 fill-yellow-100 flex-shrink-0" />
                    ) : (
                      <File className="w-10 h-10 text-gray-400 flex-shrink-0" />
                    )}
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-text dark:text-dark-text truncate">{item.obj_name}</p>
                      <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{item.parent_dir}</p>
                      <p className="text-xs text-gray-400 dark:text-gray-500">
                        {!item.is_dir && `${bytesToSize(item.size)} · `}
                        Deleted {formatDeleted(item.deleted_time)}
                      </p>
                    </div>
                    <button
                      onClick={() => handleRestore(item)}
                      aria-label={`Restore ${item.obj_name}`}
                      className="text-primary text-sm font-medium px-2 min-h-[44px]"
                    >
                      Restore
                    </button>
                  </div>
                </SwipeableListItem>
              ))}
            </div>
          )}
        </ContentCrossfade>
      )}

      {confirmClean && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4" onClick={() => setConfirmClean(false)}>
          <div className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4" onClick={(e) => e.stopPropagation()} data-testid="trash-clean-confirm">
            <p className="font-semibold text-text dark:text-dark-text">Empty trash?</p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">This permanently deletes all items in this library's trash. This cannot be undone.</p>
            <div className="mt-4 flex gap-3">
              <button onClick={() => setConfirmClean(false)} className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]">Cancel</button>
              <button onClick={handleClean} data-testid="trash-clean-confirm-yes" className="flex-1 rounded-full bg-red-500 text-white py-2 font-medium min-h-[44px]">Empty trash</button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div className="fixed bottom-20 inset-x-0 mx-auto max-w-xs rounded-full bg-black/80 text-white text-sm text-center py-2 px-4 z-50" role="status">
          {toast}
        </div>
      )}
    </div>
  );
}
