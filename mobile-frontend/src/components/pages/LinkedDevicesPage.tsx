import React, { useState, useEffect, useCallback } from 'react';
import { Monitor, Smartphone, Globe, Trash2, Laptop } from 'lucide-react';
import { listLinkedDevices, unlinkDevice } from '../../lib/api';
import type { LinkedDevice } from '../../lib/models';
import EmptyState from '../ui/EmptyState';
import Loading from '../ui/Loading';
import PullToRefreshContainer from '../ui/PullToRefreshContainer';
import SwipeableListItem from '../ui/SwipeableListItem';
import BottomSheet from '../ui/BottomSheet';

function getPlatformIcon(platform: string) {
  const p = platform.toLowerCase();
  if (p.includes('windows') || p.includes('linux') || p.includes('mac') || p.includes('desktop')) {
    return Monitor;
  }
  if (p.includes('ios') || p.includes('android') || p.includes('phone') || p.includes('mobile')) {
    return Smartphone;
  }
  if (p.includes('browser') || p.includes('web')) {
    return Globe;
  }
  return Laptop;
}

function formatTimeAgo(timestamp: string): string {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diffMs = now - then;
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'Just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDays = Math.floor(diffHr / 24);
  if (diffDays < 30) return `${diffDays}d ago`;
  return new Date(timestamp).toLocaleDateString();
}

export default function LinkedDevicesPage() {
  const [devices, setDevices] = useState<LinkedDevice[]>([]);
  const [loading, setLoading] = useState(true);
  const [confirmDevice, setConfirmDevice] = useState<LinkedDevice | null>(null);
  const [unlinking, setUnlinking] = useState(false);

  const fetchDevices = useCallback(async () => {
    try {
      const data = await listLinkedDevices();
      setDevices(data);
    } catch {
      setDevices([]);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    fetchDevices().finally(() => setLoading(false));
  }, [fetchDevices]);

  const handleRefresh = useCallback(async () => {
    await fetchDevices();
  }, [fetchDevices]);

  const handleUnlink = useCallback(async (device: LinkedDevice) => {
    setUnlinking(true);
    try {
      await unlinkDevice(device.platform, device.device_id, true);
      setDevices(prev => prev.filter(d => d.device_id !== device.device_id));
    } finally {
      setUnlinking(false);
      setConfirmDevice(null);
    }
  }, []);

  const handleSwipeUnlink = useCallback((device: LinkedDevice) => {
    setConfirmDevice(device);
  }, []);

  if (loading) {
    return <Loading message="Loading devices..." />;
  }

  if (devices.length === 0) {
    return (
      <PullToRefreshContainer onRefresh={handleRefresh}>
        <EmptyState
          icon={<Monitor size={48} />}
          title="No linked devices"
          description="You have not accessed your files with any client yet."
        />
      </PullToRefreshContainer>
    );
  }

  return (
    <PullToRefreshContainer onRefresh={handleRefresh}>
      <div data-testid="linked-devices-page" className="flex flex-col">
        <div className="flex flex-col">
          {devices.map(device => {
            const Icon = getPlatformIcon(device.platform);
            const timeAgo = formatTimeAgo(device.last_accessed);

            return (
              <SwipeableListItem
                key={device.device_id}
                rightActions={[{
                  icon: <Trash2 size={16} />,
                  label: 'Unlink',
                  color: '#ef4444',
                  onClick: () => handleSwipeUnlink(device),
                }]}
              >
                <div
                  data-testid="device-item"
                  className="w-full flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border bg-white dark:bg-dark-surface"
                >
                  <div className="flex-shrink-0 w-10 h-10 rounded-full bg-gray-100 dark:bg-gray-700 flex items-center justify-center text-gray-500 dark:text-gray-400">
                    <Icon size={20} />
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-text dark:text-dark-text truncate" data-testid="device-name">
                      {device.device_name}
                    </p>
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 mt-0.5">
                      <span className="text-xs text-gray-500 dark:text-gray-400" data-testid="device-platform">
                        {device.platform}
                      </span>
                      {device.client_version && (
                        <span className="text-xs text-gray-400 dark:text-gray-500" data-testid="device-version">
                          v{device.client_version}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-2 mt-0.5">
                      <span className="text-xs text-gray-400 dark:text-gray-500" data-testid="device-ip">
                        {device.last_login_ip}
                      </span>
                      <span className="text-xs text-gray-400 dark:text-gray-500">
                        {timeAgo}
                      </span>
                    </div>
                  </div>
                </div>
              </SwipeableListItem>
            );
          })}
        </div>
      </div>

      {/* Confirm unlink dialog */}
      <BottomSheet
        isOpen={!!confirmDevice}
        onClose={() => setConfirmDevice(null)}
        title="Unlink Device"
      >
        <div className="px-4 pb-6">
          <p className="text-sm text-gray-600 dark:text-gray-300 mb-4">
            Are you sure you want to unlink <strong>{confirmDevice?.device_name}</strong>?
            This device will no longer be able to access your files.
          </p>
          <div className="flex gap-3">
            <button
              onClick={() => setConfirmDevice(null)}
              className="flex-1 py-3 rounded-lg border border-gray-200 dark:border-dark-border text-text dark:text-dark-text font-medium min-h-[44px]"
              data-testid="cancel-unlink-btn"
            >
              Cancel
            </button>
            <button
              onClick={() => confirmDevice && handleUnlink(confirmDevice)}
              disabled={unlinking}
              className="flex-1 py-3 rounded-lg bg-red-500 text-white font-medium min-h-[44px] disabled:opacity-50"
              data-testid="confirm-unlink-btn"
            >
              {unlinking ? 'Unlinking...' : 'Unlink'}
            </button>
          </div>
        </div>
      </BottomSheet>
    </PullToRefreshContainer>
  );
}
