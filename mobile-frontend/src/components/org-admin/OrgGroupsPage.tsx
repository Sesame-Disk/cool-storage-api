import React, { useEffect, useState, useCallback } from 'react';
import { UsersRound } from 'lucide-react';
import { getOrgId, listOrgGroups } from '../../lib/api/org-admin';
import type { OrgGroup } from '../../lib/api/org-admin';
import { formatDate } from '../../lib/models';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

export default function OrgGroupsPage() {
  const [groups, setGroups] = useState<OrgGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fetchData = useCallback(async () => {
    try {
      const orgId = await getOrgId();
      setGroups(await listOrgGroups(orgId));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load groups');
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  if (error && groups.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="org-groups-page">
        <p role="alert" className="text-red-500">{error}</p>
      </div>
    );
  }

  const groupDate = (g: OrgGroup) => {
    const t = Date.parse(g.ctime);
    return Number.isNaN(t) ? '' : formatDate(t / 1000);
  };

  return (
    <div className="flex flex-col h-full" data-testid="org-groups-page">
      <div className="px-4 pt-2 pb-1">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Groups</h1>
      </div>

      <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
        {groups.length === 0 ? (
          <EmptyState icon={<UsersRound className="w-12 h-12" />} title="No groups" />
        ) : (
          <div className="flex flex-col pb-20" data-testid="org-groups-list">
            {groups.map((g) => (
              <div
                key={g.id}
                data-testid="org-group-item"
                data-group-id={g.id}
                className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
              >
                <div className="w-10 h-10 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0">
                  <UsersRound className="w-5 h-5" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-text dark:text-dark-text truncate">
                    {g.group_name}
                  </p>
                  <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
                    Owner: {g.creator_name || g.creator_email || 'unknown'}
                  </p>
                  {groupDate(g) && (
                    <p className="text-xs text-gray-400 dark:text-gray-500">Created {groupDate(g)}</p>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </ContentCrossfade>
    </div>
  );
}
