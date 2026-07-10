import React, { useState, useEffect, useCallback } from 'react';
import { History, RotateCcw, ChevronLeft } from 'lucide-react';
import { getRepoHistory, revertRepoToCommit } from '../../lib/api/history';
import type { RepoCommit } from '../../lib/api/history';

// Commit `time` is an ISO 8601 string (not a unix timestamp like mtime).
function formatCommitTime(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? '' : d.toLocaleString();
}

function authorName(commit: RepoCommit): string {
  if (commit.second_parent_id) return 'Merge';
  return commit.name || commit.email || 'Unknown';
}

import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

function repoIdFromUrl(): string {
  // /libraries/<repoId>/history
  const parts = window.location.pathname.split('/').filter(Boolean);
  const idx = parts.indexOf('libraries');
  return idx >= 0 ? (parts[idx + 1] ?? '') : '';
}

export default function HistoryPage() {
  const [repoId, setRepoId] = useState('');
  const [commits, setCommits] = useState<RepoCommit[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [confirmRevert, setConfirmRevert] = useState<RepoCommit | null>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const fetchHistory = useCallback(async (id: string) => {
    try {
      const page = await getRepoHistory(id, 1, 100);
      setCommits(page.commits);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load history');
    }
  }, []);

  useEffect(() => {
    const id = repoIdFromUrl();
    setRepoId(id);
    fetchHistory(id).finally(() => setLoading(false));
  }, [fetchHistory]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchHistory(repoId);
    setRefreshing(false);
  };

  const handleRevert = async () => {
    const commit = confirmRevert;
    setConfirmRevert(null);
    if (!commit) return;
    try {
      await revertRepoToCommit(repoId, commit.commit_id);
      showToast('Library restored to this version');
      await fetchHistory(repoId);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Restore failed');
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="history-page">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <a href={`/libraries/${repoId}/`} aria-label="Back" className="p-1 -ml-1 text-gray-500 min-h-[44px] flex items-center">
            <ChevronLeft className="w-6 h-6" />
          </a>
          <h1 className="text-xl font-semibold text-text dark:text-dark-text truncate">History</h1>
        </div>
        <div className="flex items-center gap-3">
          <button onClick={handleRefresh} disabled={refreshing} className="text-sm text-primary font-medium min-h-[44px]">
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      {error && commits.length === 0 ? (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <p role="alert" className="text-red-500 mb-4">{error}</p>
          <button onClick={handleRefresh} className="text-primary font-medium min-h-[44px]">Retry</button>
        </div>
      ) : (
        <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
          {commits.length === 0 ? (
            <EmptyState
              icon={<History className="w-12 h-12" />}
              title="No history yet"
              description="Modifications to this library will appear here as versions you can restore."
            />
          ) : (
            <div className="flex flex-col pb-20" data-testid="history-list">
              {commits.map((commit, index) => {
                const isCurrent = index === 0;
                return (
                  <div
                    key={commit.commit_id}
                    className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
                  >
                    <RotateCcw className="w-8 h-8 text-gray-400 flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-text dark:text-dark-text truncate">
                        {commit.description || 'Modified library'}
                      </p>
                      <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
                        {authorName(commit)}
                      </p>
                      <p className="text-xs text-gray-400 dark:text-gray-500">
                        {formatCommitTime(commit.time)}
                      </p>
                    </div>
                    {isCurrent ? (
                      <span className="text-xs text-gray-400 dark:text-gray-500 px-2">Current</span>
                    ) : (
                      <button
                        onClick={() => setConfirmRevert(commit)}
                        aria-label={`Restore this version from ${formatCommitTime(commit.time)}`}
                        className="text-primary text-sm font-medium px-2 min-h-[44px]"
                      >
                        Restore this version
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </ContentCrossfade>
      )}

      {confirmRevert && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4" onClick={() => setConfirmRevert(null)}>
          <div className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4" onClick={(e) => e.stopPropagation()} data-testid="history-revert-confirm">
            <p className="font-semibold text-text dark:text-dark-text">Restore this version?</p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              This reverts the entire library to its state at {formatCommitTime(confirmRevert.time)}. A new version is created, so you can revert again if needed.
            </p>
            <div className="mt-4 flex gap-3">
              <button onClick={() => setConfirmRevert(null)} className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]">Cancel</button>
              <button onClick={handleRevert} data-testid="history-revert-confirm-yes" className="flex-1 rounded-full bg-primary text-white py-2 font-medium min-h-[44px]">Restore version</button>
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
