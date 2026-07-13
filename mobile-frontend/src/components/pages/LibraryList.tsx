import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Library, Plus, ArrowUpDown, ChevronDown } from 'lucide-react';
import { listRepos } from '../../lib/api';
import type { Repo } from '../../lib/models';
import { bytesToSize } from '../../lib/models';
import { cacheRepos, getCachedRepos } from '../../lib/offlineDb';
import LibraryCard from '../libraries/LibraryCard';
import NewLibrarySheet from '../libraries/NewLibrarySheet';
import LibraryContextMenu from '../libraries/LibraryContextMenu';
import BottomSheet from '../ui/BottomSheet';
import SkeletonList from '../ui/SkeletonList';
import { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';
import FAB from '../ui/FAB';
import { AnimatedListItem } from '../ui/AnimatedList';
import PullToRefreshContainer from '../ui/PullToRefreshContainer';
import { AnimatePresence } from 'framer-motion';
import { getSortPreference, setSortPreference } from '../../lib/sortPreference';
import type { SortField, SortDirection } from '../../lib/sortPreference';
import { supportsFolderSync } from '../../lib/sync/capabilities';
import { useSyncStatus } from '../../lib/sync/useSyncStatus';
import { enableSync, unsync, setAutoSync, syncLibrary } from '../../lib/sync/syncEngine';

const sortOptions: { field: SortField; label: string }[] = [
  { field: 'name', label: 'Name' },
  { field: 'date', label: 'Last Modified' },
  { field: 'size', label: 'Size' },
];

function sortRepos(repos: Repo[], field: SortField, direction: SortDirection): Repo[] {
  const sorted = [...repos];
  sorted.sort((a, b) => {
    let cmp = 0;
    switch (field) {
      case 'name':
        cmp = a.repo_name.localeCompare(b.repo_name);
        break;
      case 'date':
        cmp = new Date(a.last_modified).getTime() - new Date(b.last_modified).getTime();
        break;
      case 'size':
        cmp = a.size - b.size;
        break;
    }
    return direction === 'asc' ? cmp : -cmp;
  });
  return sorted;
}

export default function LibraryList() {
  const [repos, setRepos] = useState<Repo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [sortField, setSortField] = useState<SortField>('name');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');
  const [sortSheetOpen, setSortSheetOpen] = useState(false);
  const [newLibOpen, setNewLibOpen] = useState(false);
  const [contextRepo, setContextRepo] = useState<Repo | null>(null);
  const [contextMenuOpen, setContextMenuOpen] = useState(false);
  const [showingCached, setShowingCached] = useState(false);
  const [toast, setToast] = useState('');
  const syncConfigs = useSyncStatus();

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 3000);
  };

  // Load sort preference on mount
  useEffect(() => {
    const pref = getSortPreference();
    setSortField(pref.field);
    setSortDirection(pref.direction);
  }, []);

  const fetchRepos = useCallback(async () => {
    try {
      const data = await listRepos();
      setRepos(data);
      setError('');
      setShowingCached(false);
      cacheRepos(data).catch(() => {});
    } catch (err) {
      // Try offline fallback
      try {
        const cached = await getCachedRepos();
        if (cached && cached.length > 0) {
          setRepos(cached);
          setShowingCached(true);
          setError('');
          return;
        }
      } catch {
        // ignore cache errors
      }
      setError(err instanceof Error ? err.message : 'Failed to load libraries');
    }
  }, []);

  useEffect(() => {
    fetchRepos().finally(() => setLoading(false));
  }, [fetchRepos]);

  const handleRefresh = async () => {
    await fetchRepos();
  };

  const sortedRepos = useMemo(
    () => sortRepos(repos, sortField, sortDirection),
    [repos, sortField, sortDirection],
  );

  const handleSortSelect = (field: SortField) => {
    const newDirection = field === sortField && sortDirection === 'asc' ? 'desc' : 'asc';
    setSortField(field);
    setSortDirection(newDirection);
    setSortPreference({ field, direction: newDirection });
    setSortSheetOpen(false);
  };

  const handleTap = (repo: Repo) => {
    window.location.href = `/libraries/${repo.repo_id}/`;
  };

  const handleLongPress = (repo: Repo) => {
    setContextRepo(repo);
    setContextMenuOpen(true);
  };

  const handleContextOpen = (repo: Repo) => {
    window.location.href = `/libraries/${repo.repo_id}/`;
  };

  const handleContextShare = (repo: Repo) => {
    if (navigator.share) {
      navigator.share({ title: repo.repo_name, url: `/libraries/${repo.repo_id}/` }).catch(() => {});
    }
  };

  const handleContextDetails = (_repo: Repo) => {
    // Details could be expanded later
  };

  // --- Folder sync -------------------------------------------------------
  const handleSyncStart = async (repo: Repo) => {
    if (!supportsFolderSync()) return;
    try {
      // Must run inside the user gesture that opened the context menu action.
      const handle = await (window as unknown as {
        showDirectoryPicker: (opts?: { mode?: 'read' | 'readwrite' }) => Promise<FileSystemDirectoryHandle>;
      }).showDirectoryPicker({ mode: 'read' });
      showToast(`Syncing "${repo.repo_name}"…`);
      await enableSync(repo.repo_id, repo.repo_name, handle);
      showToast(`"${repo.repo_name}" synced`);
    } catch (err) {
      // AbortError = the user dismissed the native picker; stay silent.
      if (err instanceof DOMException && err.name === 'AbortError') return;
      showToast(err instanceof Error ? err.message : 'Failed to start sync');
    }
  };

  const handleSyncNow = async (repo: Repo) => {
    showToast(`Syncing "${repo.repo_name}"…`);
    await syncLibrary(repo.repo_id, { interactive: true });
    const cfg = syncConfigs.get(repo.repo_id);
    showToast(cfg?.status === 'error' ? cfg.error || 'Sync failed' : 'Sync complete');
  };

  const handleSyncPauseResume = async (repo: Repo) => {
    const cfg = syncConfigs.get(repo.repo_id);
    const next = !(cfg?.autoSync ?? true);
    await setAutoSync(repo.repo_id, next);
    showToast(next ? 'Auto-sync resumed' : 'Auto-sync paused');
  };

  const handleUnsync = async (repo: Repo) => {
    if (!window.confirm(`Stop syncing "${repo.repo_name}"? Remote files are kept; only the local link is removed.`)) return;
    await unsync(repo.repo_id);
    showToast('Sync removed');
  };

  const currentSortLabel = sortOptions.find((o) => o.field === sortField)?.label ?? 'Name';

  if (error && repos.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <p role="alert" className="text-red-500 mb-4">{error}</p>
        <button
          onClick={() => { setLoading(true); fetchRepos().finally(() => setLoading(false)); }}
          className="text-primary font-medium min-h-[44px]"
        >
          Retry
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">My Libraries</h1>
      </div>

      {showingCached && (
        <div className="mx-4 mt-1 px-3 py-1 bg-amber-100 dark:bg-amber-900/30 text-amber-800 dark:text-amber-200 text-xs rounded" data-testid="cached-indicator">
          Showing cached data
        </div>
      )}

      {/* Sort indicator bar */}
      {!loading && repos.length > 0 && (
        <button
          onClick={() => setSortSheetOpen(true)}
          className="flex items-center gap-1 px-4 py-2 text-sm text-gray-500 dark:text-gray-400"
          data-testid="sort-button"
        >
          <ArrowUpDown size={14} />
          <span>{currentSortLabel}</span>
          <ChevronDown size={14} />
          <span className="text-xs">({sortDirection === 'asc' ? '↑' : '↓'})</span>
        </button>
      )}

      <ContentCrossfade
        isLoading={loading}
        skeleton={<SkeletonList variant="library" count={6} />}
      >
        {repos.length === 0 ? (
          <EmptyState
            icon={<Library className="w-12 h-12" />}
            title="No libraries yet"
            description="Create a library to start storing your files."
            action={{ label: 'Create a library', onClick: () => setNewLibOpen(true) }}
          />
        ) : (
          <PullToRefreshContainer onRefresh={handleRefresh}>
            <div className="flex flex-col pb-20">
              <AnimatePresence mode="popLayout">
                {sortedRepos.map((repo, index) => (
                  <AnimatedListItem key={repo.repo_id} itemKey={repo.repo_id} index={index}>
                    <LibraryCard
                      repo={repo}
                      onTap={handleTap}
                      onLongPress={handleLongPress}
                      syncStatus={syncConfigs.get(repo.repo_id)?.status}
                    />
                  </AnimatedListItem>
                ))}
              </AnimatePresence>
            </div>
          </PullToRefreshContainer>
        )}
      </ContentCrossfade>

      {/* FAB */}
      <FAB
        actions={[
          {
            icon: <Plus size={20} />,
            label: 'New Library',
            onClick: () => setNewLibOpen(true),
          },
        ]}
      />

      {/* Sort bottom sheet */}
      <BottomSheet isOpen={sortSheetOpen} onClose={() => setSortSheetOpen(false)} title="Sort by">
        <div className="flex flex-col">
          {sortOptions.map(({ field, label }) => (
            <button
              key={field}
              onClick={() => handleSortSelect(field)}
              className={`flex items-center justify-between px-2 py-3 min-h-[44px] rounded-lg ${
                sortField === field ? 'text-primary bg-primary/5' : 'text-text dark:text-dark-text'
              }`}
            >
              <span className="text-sm">{label}</span>
              {sortField === field && (
                <span className="text-xs font-medium">
                  {sortDirection === 'asc' ? 'Ascending' : 'Descending'}
                </span>
              )}
            </button>
          ))}
        </div>
      </BottomSheet>

      {/* New Library sheet */}
      <NewLibrarySheet
        isOpen={newLibOpen}
        onClose={() => setNewLibOpen(false)}
        onCreated={handleRefresh}
      />

      {/* Context menu */}
      <LibraryContextMenu
        isOpen={contextMenuOpen}
        onClose={() => setContextMenuOpen(false)}
        repo={contextRepo}
        onOpen={handleContextOpen}
        onShare={handleContextShare}
        onDetails={handleContextDetails}
        syncStatus={contextRepo ? syncConfigs.get(contextRepo.repo_id)?.status : undefined}
        autoSync={contextRepo ? syncConfigs.get(contextRepo.repo_id)?.autoSync ?? true : true}
        onSyncStart={handleSyncStart}
        onSyncNow={handleSyncNow}
        onSyncPauseResume={handleSyncPauseResume}
        onUnsync={handleUnsync}
      />

      {/* Toast */}
      {toast && (
        <div className="fixed bottom-20 left-1/2 -translate-x-1/2 bg-gray-800 text-white px-4 py-2 rounded-lg text-sm z-50" data-testid="library-toast">
          {toast}
        </div>
      )}
    </div>
  );
}
