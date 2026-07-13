import React from 'react';
import { Check, RefreshCw, PauseCircle, AlertCircle } from 'lucide-react';
import type { SyncStatus } from '../../lib/offlineDb';

interface SyncStatusBadgeProps {
  status: SyncStatus;
  withLabel?: boolean;
}

const MAP: Record<SyncStatus, { icon: typeof Check; className: string; label: string } | null> = {
  idle: { icon: RefreshCw, className: 'text-gray-400', label: 'Waiting to sync' },
  syncing: { icon: RefreshCw, className: 'text-primary animate-spin', label: 'Syncing…' },
  synced: { icon: Check, className: 'text-green-600 dark:text-green-500', label: 'Synced' },
  paused: { icon: PauseCircle, className: 'text-gray-400', label: 'Paused' },
  error: { icon: AlertCircle, className: 'text-red-500', label: 'Sync error' },
};

/** Seafile-style per-library sync indicator (green check / spinner / paused /
 * error). Renders nothing for an unknown status. */
export default function SyncStatusBadge({ status, withLabel = false }: SyncStatusBadgeProps) {
  const entry = MAP[status];
  if (!entry) return null;
  const { icon: Icon, className, label } = entry;
  return (
    <span
      className={`inline-flex items-center gap-1 ${className}`}
      data-testid="sync-status-badge"
      data-sync-status={status}
      title={label}
    >
      <Icon className="w-4 h-4 flex-shrink-0" aria-label={label} />
      {withLabel && <span className="text-xs">{label}</span>}
    </span>
  );
}
