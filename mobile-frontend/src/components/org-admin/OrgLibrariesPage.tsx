import React, { useEffect, useState, useCallback } from 'react';
import { FolderGit2, Trash2, Lock } from 'lucide-react';
import { getOrgId, listOrgRepos, deleteOrgRepo } from '../../lib/api/org-admin';
import type { OrgRepo } from '../../lib/api/org-admin';
import { bytesToSize } from '../../lib/models';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

export default function OrgLibrariesPage() {
  const [repos, setRepos] = useState<OrgRepo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [confirm, setConfirm] = useState<OrgRepo | null>(null);
  const [deleting, setDeleting] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const orgId = await getOrgId();
      setRepos(await listOrgRepos(orgId));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load libraries');
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  const handleDelete = async () => {
    if (!confirm) return;
    setDeleting(true);
    try {
      const orgId = await getOrgId();
      await deleteOrgRepo(orgId, confirm.repo_id);
      setRepos((prev) => prev.filter((r) => r.repo_id !== confirm.repo_id));
      setConfirm(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete library');
    } finally {
      setDeleting(false);
    }
  };

  if (error && repos.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="org-libraries-page">
        <p role="alert" className="text-red-500">{error}</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full" data-testid="org-libraries-page">
      <div className="px-4 pt-2 pb-1">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Libraries</h1>
      </div>

      {error && repos.length > 0 && (
        <p role="alert" className="px-4 text-sm text-red-500">{error}</p>
      )}

      <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="library" count={5} />}>
        {repos.length === 0 ? (
          <EmptyState icon={<FolderGit2 className="w-12 h-12" />} title="No libraries" />
        ) : (
          <div className="flex flex-col pb-20" data-testid="org-libraries-list">
            {repos.map((r) => (
              <div
                key={r.repo_id}
                data-testid="org-library-item"
                data-repo-id={r.repo_id}
                className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
              >
                <div className="w-10 h-10 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0">
                  <FolderGit2 className="w-5 h-5" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-text dark:text-dark-text truncate flex items-center gap-1">
                    {r.encrypted && <Lock className="w-3 h-3 flex-shrink-0" />}
                    <span className="truncate">{r.repo_name}</span>
                  </p>
                  <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
                    {r.owner_name || r.owner_email}
                  </p>
                  <p className="text-xs text-gray-400 dark:text-gray-500">{bytesToSize(r.size)}</p>
                </div>
                <button
                  onClick={() => setConfirm(r)}
                  aria-label={`Delete library ${r.repo_name}`}
                  data-testid="org-library-delete"
                  className="text-red-500 p-2 min-h-[44px] min-w-[44px] flex items-center justify-center"
                >
                  <Trash2 className="w-5 h-5" />
                </button>
              </div>
            ))}
          </div>
        )}
      </ContentCrossfade>

      {confirm && (
        <div
          className="fixed inset-0 bg-black/50 flex items-center justify-center z-[60] p-4"
          data-testid="org-library-delete-dialog"
        >
          <div className="bg-white dark:bg-dark-surface rounded-lg p-6 max-w-sm w-full shadow-xl">
            <h3 className="text-lg font-semibold text-text dark:text-dark-text mb-2">Delete Library</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">
              Delete “{confirm.repo_name}”? This permanently removes it for its owner. This action
              cannot be undone.
            </p>
            <div className="flex gap-3 justify-end">
              <button
                onClick={() => setConfirm(null)}
                data-testid="org-library-delete-cancel"
                className="px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 min-h-[44px]"
              >
                Cancel
              </button>
              <button
                onClick={handleDelete}
                disabled={deleting}
                data-testid="org-library-delete-confirm"
                className="px-4 py-2 text-sm font-medium text-white bg-red-500 rounded-lg min-h-[44px] disabled:opacity-50"
              >
                {deleting ? 'Deleting…' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
