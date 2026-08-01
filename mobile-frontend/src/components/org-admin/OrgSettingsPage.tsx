import React, { useEffect, useState, useCallback } from 'react';
import { getOrgId, getOrgWebSettings } from '../../lib/api/org-admin';
import type { OrgWebSettings } from '../../lib/api/org-admin';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';

export default function OrgSettingsPage() {
  const [settings, setSettings] = useState<OrgWebSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fetchData = useCallback(async () => {
    try {
      const orgId = await getOrgId();
      setSettings(await getOrgWebSettings(orgId));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load settings');
    }
  }, []);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  if (error && !settings) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="org-settings-page">
        <p role="alert" className="text-red-500">{error}</p>
      </div>
    );
  }

  const Row = ({ label, value }: { label: string; value: string }) => (
    <div className="flex flex-col gap-1 px-4 py-3 border-b border-gray-100 dark:border-dark-border last:border-b-0">
      <span className="text-xs text-gray-500 dark:text-gray-400">{label}</span>
      <span className="text-sm text-text dark:text-dark-text break-words">{value || '—'}</span>
    </div>
  );

  return (
    <div className="flex flex-col h-full" data-testid="org-settings-page">
      <div className="px-4 pt-2 pb-1">
        <h1 className="text-xl font-semibold text-text dark:text-dark-text">Organization Settings</h1>
        <p className="text-xs text-gray-500 dark:text-gray-400">
          Read-only — edit these in the desktop version.
        </p>
      </div>

      <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={3} />}>
        {settings && (
          <div className="mx-4 mt-2 bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
            <Row label="Organization name" value={settings.org_name} />
            <Row label="Allowed file extensions" value={settings.file_ext_white_list} />
            <Row label="Logo path" value={settings.logo_path} />
          </div>
        )}
      </ContentCrossfade>
    </div>
  );
}
