import { useState, useEffect, useCallback } from 'react';
import { Trash2, RotateCcw, Database, ChevronLeft } from 'lucide-react';
import { listDeletedRepos, restoreDeletedRepo } from '../../lib/api/deleted-repos';
import type { DeletedRepo } from '../../lib/api/deleted-repos';
import { bytesToSize } from '../../lib/models';
import SwipeableListItem from '../ui/SwipeableListItem';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

function formatDeleted(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? '' : d.toLocaleString();
}

export default function DeletedLibrariesPage() {
  const [repos, setRepos] = useState<DeletedRepo[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const fetchRepos = useCallback(async () => {
    try {
      setRepos(await listDeletedRepos());
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load deleted libraries');
    }
  }, []);

  useEffect(() => {
    fetchRepos().finally(() => setLoading(false));
  }, [fetchRepos]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchRepos();
    setRefreshing(false);
  };

  const handleRestore = async (repo: DeletedRepo) => {
    try {
      await restoreDeletedRepo(repo.repo_id);
      setRepos((prev) => prev.filter((r) => r.repo_id !== repo.repo_id));
      showToast(`Restored ${repo.repo_name}`);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Restore failed');
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="deleted-libs-page">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between">
        <div className="flex items-center gap-2 min-w-0">
          <a href="/libraries/" aria-label="Back" className="p-1 -ml-1 text-gray-500 min-h-[44px] flex items-center">
            <ChevronLeft className="w-6 h-6" />
          </a>
          <h1 className="text-xl font-semibold text-text dark:text-dark-text truncate">Deleted Libraries</h1>
        </div>
        <button onClick={handleRefresh} disabled={refreshing} className="text-sm text-primary font-medium min-h-[44px]">
          {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      {error && repos.length === 0 ? (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <p role="alert" className="text-red-500 mb-4">{error}</p>
          <button onClick={handleRefresh} className="text-primary font-medium min-h-[44px]">Retry</button>
        </div>
      ) : (
        <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={4} />}>
          {repos.length === 0 ? (
            <EmptyState
              icon={<Trash2 className="w-12 h-12" />}
              title="No deleted libraries"
              description="Libraries you delete can be restored here for a limited time."
            />
          ) : (
            <div className="flex flex-col pb-20" data-testid="deleted-libs-list">
              {repos.map((repo) => (
                <SwipeableListItem
                  key={repo.repo_id}
                  rightActions={[
                    {
                      icon: <RotateCcw className="w-5 h-5" />,
                      label: 'Restore',
                      color: '#10b981',
                      onClick: () => handleRestore(repo),
                    },
                  ]}
                >
                  <div className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border">
                    <Database className="w-10 h-10 text-primary flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-text dark:text-dark-text truncate">{repo.repo_name}</p>
                      <p className="text-xs text-gray-400 dark:text-gray-500">
                        {bytesToSize(repo.size)} · Deleted {formatDeleted(repo.del_time)}
                      </p>
                    </div>
                    <button
                      onClick={() => handleRestore(repo)}
                      aria-label={`Restore ${repo.repo_name}`}
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

      {toast && (
        <div className="fixed bottom-20 inset-x-0 mx-auto max-w-xs rounded-full bg-black/80 text-white text-sm text-center py-2 px-4 z-50" role="status">
          {toast}
        </div>
      )}
    </div>
  );
}
