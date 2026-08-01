import React, { useEffect, useState, useCallback } from 'react';
import { Link2, Trash2 } from 'lucide-react';
import { getOrgId, listOrgShareLinks, deleteOrgShareLink } from '../../lib/api/org-admin';
import type { OrgShareLink } from '../../lib/api/org-admin';
import { formatDate } from '../../lib/models';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

export default function OrgShareLinksPage() {
  const [links, setLinks] = useState<OrgShareLink[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [confirm, setConfirm] = useState<OrgShareLink | null>(null);
  const [revoking, setRevoking] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const orgId = await getOrgId();
      setLinks(await listOrgShareLinks(orgId));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load share links');
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  const handleRevoke = async () => {
    if (!confirm) return;
    setRevoking(true);
    try {
      const orgId = await getOrgId();
      await deleteOrgShareLink(orgId, confirm.token);
      setLinks((prev) => prev.filter((l) => l.token !== confirm.token));
      setConfirm(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to revoke share link');
    } finally {
      setRevoking(false);
    }
  };

  const linkDate = (l: OrgShareLink) => {
    const t = Date.parse(l.ctime || l.created_time);
    return Number.isNaN(t) ? '' : formatDate(t / 1000);
  };
  const name = (l: OrgShareLink) => l.obj_name || l.name || l.path;

  if (error && links.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="org-sharelinks-page">
        <p role="alert" className="text-red-500">{error}</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full" data-testid="org-sharelinks-page">
      <div className="px-4 pt-2 pb-1">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Share Links</h1>
      </div>

      {error && links.length > 0 && (
        <p role="alert" className="px-4 text-sm text-red-500">{error}</p>
      )}

      <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
        {links.length === 0 ? (
          <EmptyState icon={<Link2 className="w-12 h-12" />} title="No share links" />
        ) : (
          <div className="flex flex-col pb-20" data-testid="org-sharelinks-list">
            {links.map((l) => (
              <div
                key={l.token}
                data-testid="org-sharelink-item"
                data-token={l.token}
                className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
              >
                <Link2 className="w-10 h-10 text-primary flex-shrink-0" />
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-text dark:text-dark-text truncate">{name(l)}</p>
                  <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
                    {l.creator_name || l.creator_email || l.owner_email}
                  </p>
                  <p className="text-xs text-gray-400 dark:text-gray-500">
                    {linkDate(l) && `Created ${linkDate(l)} · `}
                    {l.view_count ?? l.view_cnt ?? 0} views
                    {l.is_expired ? ' · Expired' : ''}
                  </p>
                </div>
                <button
                  onClick={() => setConfirm(l)}
                  aria-label={`Revoke share link ${name(l)}`}
                  data-testid="org-sharelink-revoke"
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
          data-testid="org-sharelink-revoke-dialog"
        >
          <div className="bg-white dark:bg-dark-surface rounded-lg p-6 max-w-sm w-full shadow-xl">
            <h3 className="text-lg font-semibold text-text dark:text-dark-text mb-2">Revoke Share Link</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">
              Revoke this share link? Anyone with the URL will lose access. This cannot be undone.
            </p>
            <div className="flex gap-3 justify-end">
              <button
                onClick={() => setConfirm(null)}
                data-testid="org-sharelink-revoke-cancel"
                className="px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 min-h-[44px]"
              >
                Cancel
              </button>
              <button
                onClick={handleRevoke}
                disabled={revoking}
                data-testid="org-sharelink-revoke-confirm"
                className="px-4 py-2 text-sm font-medium text-white bg-red-500 rounded-lg min-h-[44px] disabled:opacity-50"
              >
                {revoking ? 'Revoking…' : 'Revoke'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
