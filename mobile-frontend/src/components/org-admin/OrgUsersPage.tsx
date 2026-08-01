import React, { useEffect, useState, useCallback } from 'react';
import { Users } from 'lucide-react';
import { getOrgId, listOrgUsers } from '../../lib/api/org-admin';
import type { OrgUser } from '../../lib/api/org-admin';
import { bytesToSize } from '../../lib/models';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

export default function OrgUsersPage() {
  const [users, setUsers] = useState<OrgUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fetchData = useCallback(async () => {
    try {
      const orgId = await getOrgId();
      const page = await listOrgUsers(orgId);
      setUsers(page.users);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load users');
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  if (error && users.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="org-users-page">
        <p role="alert" className="text-red-500">{error}</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full" data-testid="org-users-page">
      <div className="px-4 pt-2 pb-1">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Users</h1>
        <p className="text-xs text-gray-500 dark:text-gray-400">
          Read-only — user accounts are managed centrally.
        </p>
      </div>

      <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
        {users.length === 0 ? (
          <EmptyState icon={<Users className="w-12 h-12" />} title="No users" />
        ) : (
          <div className="flex flex-col pb-20" data-testid="org-users-list">
            {users.map((u) => (
              <div
                key={u.email}
                data-testid="org-user-item"
                data-email={u.email}
                className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
              >
                <div className="w-10 h-10 rounded-full bg-primary/10 text-primary flex items-center justify-center flex-shrink-0">
                  <Users className="w-5 h-5" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-text dark:text-dark-text truncate">
                    {u.name || u.email}
                  </p>
                  <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{u.email}</p>
                  <p className="text-xs text-gray-400 dark:text-gray-500">
                    {bytesToSize(u.quota_usage)}
                    {u.quota_total > 0 ? ` / ${bytesToSize(u.quota_total)}` : ''}
                    {u.is_org_staff ? ' · Admin' : ''}
                  </p>
                </div>
                <span
                  className={`text-xs font-medium px-2 py-1 rounded-full ${
                    u.is_active
                      ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                      : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
                  }`}
                >
                  {u.is_active ? 'Active' : 'Inactive'}
                </span>
              </div>
            ))}
          </div>
        )}
      </ContentCrossfade>
    </div>
  );
}
