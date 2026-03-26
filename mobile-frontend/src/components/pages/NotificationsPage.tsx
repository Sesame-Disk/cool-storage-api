import React, { useState, useEffect, useCallback } from 'react';
import { Bell, Share2, Users, FolderSync, Upload, Trash2, Shield, Eye, CheckCheck } from 'lucide-react';
import {
  listNotifications,
  markNotificationAsRead,
  markAllNotificationsRead,
  deleteNotification,
} from '../../lib/api';
import type { Notification } from '../../lib/models';
import { useInfiniteScroll } from '../../lib/hooks/useInfiniteScroll';
import EmptyState from '../ui/EmptyState';
import Loading from '../ui/Loading';
import PullToRefreshContainer from '../ui/PullToRefreshContainer';
import SwipeableListItem from '../ui/SwipeableListItem';

const PER_PAGE = 25;

function getNotificationIcon(type: string) {
  switch (type) {
    case 'repo_share':
    case 'repo_share_to_group':
    case 'repo_share_perm_change':
    case 'repo_share_perm_delete':
      return Share2;
    case 'add_user_to_group':
      return Users;
    case 'repo_transfer':
      return FolderSync;
    case 'file_uploaded':
    case 'folder_uploaded':
      return Upload;
    case 'deleted_files':
      return Trash2;
    case 'repo_monitor':
      return Eye;
    case 'saml_sso_failed':
      return Shield;
    default:
      return Bell;
  }
}

function getNotificationMessage(notification: Notification): string {
  const detail = notification.detail;
  const type = notification.type;

  switch (type) {
    case 'repo_share': {
      const from = (detail.share_from_user_name as string) || 'Someone';
      const repo = (detail.repo_name as string) || 'a library';
      const path = detail.path as string;
      return path === '/'
        ? `${from} shared library "${repo}" with you`
        : `${from} shared folder "${repo}" with you`;
    }
    case 'repo_share_to_group': {
      const from = (detail.share_from_user_name as string) || 'Someone';
      const repo = (detail.repo_name as string) || 'a library';
      const group = (detail.group_name as string) || 'a group';
      return `${from} shared "${repo}" to group "${group}"`;
    }
    case 'repo_share_perm_change': {
      const from = (detail.share_from_user_name as string) || 'Someone';
      const repo = (detail.repo_name as string) || 'a library';
      const perm = (detail.permission as string) || '';
      return `${from} changed permission of "${repo}" to ${perm}`;
    }
    case 'repo_share_perm_delete': {
      const from = (detail.share_from_user_name as string) || 'Someone';
      const repo = (detail.repo_name as string) || 'a library';
      return `${from} cancelled sharing of "${repo}"`;
    }
    case 'add_user_to_group': {
      const staff = (detail.group_staff_name as string) || 'Someone';
      const group = (detail.group_name as string) || 'a group';
      return `${staff} added you to group "${group}"`;
    }
    case 'repo_transfer': {
      const from = (detail.transfer_from_user_name as string) || 'Someone';
      const repo = (detail.repo_name as string) || 'a library';
      return `${from} transferred library "${repo}" to you`;
    }
    case 'file_uploaded': {
      const file = (detail.file_name as string) || 'a file';
      const folder = (detail.folder_name as string) || '';
      return folder ? `File "${file}" uploaded to "${folder}"` : `File "${file}" uploaded`;
    }
    case 'folder_uploaded': {
      const folder = (detail.folder_name as string) || 'a folder';
      const parent = (detail.parent_dir_name as string) || '';
      return parent ? `Folder "${folder}" uploaded to "${parent}"` : `Folder "${folder}" uploaded`;
    }
    case 'repo_monitor': {
      const user = (detail.op_user_name as string) || 'Someone';
      const op = (detail.op_type as string) || 'modified';
      const repo = (detail.repo_name as string) || 'a library';
      return `${user} ${op}d files in "${repo}"`;
    }
    case 'deleted_files': {
      const repo = (detail.repo_name as string) || 'a library';
      return `Library "${repo}" has recently deleted many files`;
    }
    case 'saml_sso_failed':
      return (detail.error_msg as string) || 'SSO authentication failed';
    default:
      return 'New notification';
  }
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

export default function NotificationsPage() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(true);
  const [page, setPage] = useState(1);

  const fetchNotifications = useCallback(async (pageNum: number, replace = false) => {
    try {
      const data = await listNotifications(pageNum, PER_PAGE);
      const items = data.notification_list || [];
      setNotifications(prev => replace ? items : [...prev, ...items]);
      setHasMore(items.length >= PER_PAGE);
    } catch {
      if (replace) setNotifications([]);
      setHasMore(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    fetchNotifications(1, true).finally(() => setLoading(false));
  }, [fetchNotifications]);

  const handleLoadMore = useCallback(async () => {
    const nextPage = page + 1;
    setPage(nextPage);
    await fetchNotifications(nextPage);
  }, [page, fetchNotifications]);

  const handleRefresh = useCallback(async () => {
    setPage(1);
    await fetchNotifications(1, true);
    infiniteScroll.reset();
  }, [fetchNotifications]);

  const infiniteScroll = useInfiniteScroll({
    onLoadMore: handleLoadMore,
    hasMore,
  });

  const handleMarkAsRead = useCallback(async (notification: Notification) => {
    if (notification.seen) return;
    await markNotificationAsRead(notification.id);
    setNotifications(prev =>
      prev.map(n => n.id === notification.id ? { ...n, seen: true } : n)
    );
  }, []);

  const handleDelete = useCallback(async (notificationId: number) => {
    await deleteNotification(notificationId);
    setNotifications(prev => prev.filter(n => n.id !== notificationId));
  }, []);

  const handleMarkAllRead = useCallback(async () => {
    await markAllNotificationsRead();
    setNotifications(prev => prev.map(n => ({ ...n, seen: true })));
  }, []);

  if (loading) {
    return <Loading message="Loading notifications..." />;
  }

  if (notifications.length === 0) {
    return (
      <PullToRefreshContainer onRefresh={handleRefresh}>
        <EmptyState
          icon={<Bell size={48} />}
          title="No notifications"
          description="You're all caught up!"
        />
      </PullToRefreshContainer>
    );
  }

  return (
    <PullToRefreshContainer onRefresh={handleRefresh}>
      <div data-testid="notifications-page" className="flex flex-col">
        <div className="flex items-center justify-end px-4 py-2">
          <button
            onClick={handleMarkAllRead}
            data-testid="mark-all-read-btn"
            className="flex items-center gap-1.5 text-sm text-primary font-medium py-1 px-2 rounded-md active:bg-primary/10"
          >
            <CheckCheck size={16} />
            Mark all read
          </button>
        </div>

        <div className="flex flex-col">
          {notifications.map(notification => {
            const Icon = getNotificationIcon(notification.type);
            const message = getNotificationMessage(notification);
            const timeAgo = formatTimeAgo(notification.timestamp);

            return (
              <SwipeableListItem
                key={notification.id}
                rightActions={[{
                  icon: <Trash2 size={16} />,
                  label: 'Delete',
                  color: '#ef4444',
                  onClick: () => handleDelete(notification.id),
                }]}
              >
                <button
                  onClick={() => handleMarkAsRead(notification)}
                  data-testid="notification-item"
                  className={`w-full flex items-start gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border text-left ${
                    notification.seen
                      ? 'bg-white dark:bg-dark-surface'
                      : 'bg-primary/5 dark:bg-primary/10'
                  }`}
                >
                  <div className={`mt-0.5 flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center ${
                    notification.seen
                      ? 'bg-gray-100 dark:bg-gray-700 text-gray-400 dark:text-gray-500'
                      : 'bg-primary/10 text-primary'
                  }`}>
                    <Icon size={16} />
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className={`text-sm leading-snug ${
                      notification.seen
                        ? 'text-gray-500 dark:text-gray-400'
                        : 'text-text dark:text-dark-text font-medium'
                    }`}>
                      {message}
                    </p>
                    <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">{timeAgo}</p>
                  </div>
                  {!notification.seen && (
                    <div
                      data-testid="unread-indicator"
                      className="mt-2 flex-shrink-0 w-2 h-2 rounded-full bg-primary"
                    />
                  )}
                </button>
              </SwipeableListItem>
            );
          })}
        </div>

        {hasMore && (
          <div ref={infiniteScroll.sentinelRef as React.Ref<HTMLDivElement>} data-testid="scroll-sentinel" className="py-4">
            <Loading message="Loading more..." />
          </div>
        )}
      </div>
    </PullToRefreshContainer>
  );
}
