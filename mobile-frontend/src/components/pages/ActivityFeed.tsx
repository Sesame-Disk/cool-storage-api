import React, { useState, useEffect, useCallback } from 'react';
import { Activity as ActivityIcon } from 'lucide-react';
import { listActivities } from '../../lib/api';
import type { Activity } from '../../lib/models';
import { useInfiniteScroll } from '../../lib/hooks/useInfiniteScroll';
import ActivityItem, { getDayKey } from '../activity/ActivityItem';
import EmptyState from '../ui/EmptyState';
import Loading from '../ui/Loading';
import PullToRefreshContainer from '../ui/PullToRefreshContainer';

export default function ActivityFeed() {
  const [activities, setActivities] = useState<Activity[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(true);
  const [page, setPage] = useState(1);

  const fetchActivities = useCallback(async (pageNum: number, replace = false) => {
    try {
      const { events, more } = await listActivities(pageNum);
      setActivities(prev => replace ? events : [...prev, ...events]);
      setHasMore(more);
    } catch {
      if (replace) setActivities([]);
      setHasMore(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    fetchActivities(1, true).finally(() => setLoading(false));
  }, [fetchActivities]);

  const handleLoadMore = useCallback(async () => {
    const nextPage = page + 1;
    setPage(nextPage);
    await fetchActivities(nextPage);
  }, [page, fetchActivities]);

  const handleRefresh = useCallback(async () => {
    setPage(1);
    await fetchActivities(1, true);
    infiniteScroll.reset();
  }, [fetchActivities]);

  const infiniteScroll = useInfiniteScroll({
    onLoadMore: handleLoadMore,
    hasMore,
  });

  if (loading) {
    return <Loading message="Loading activities..." />;
  }

  if (activities.length === 0) {
    return (
      <EmptyState
        icon={<ActivityIcon size={48} />}
        title="No recent activity"
      />
    );
  }

  let lastDayKey = '';

  return (
    <PullToRefreshContainer onRefresh={handleRefresh}>
      <div className="pb-20" data-testid="activity-feed">
        {activities.map((activity, index) => {
          const dayKey = getDayKey(activity.time);
          const showDivider = dayKey !== lastDayKey;
          lastDayKey = dayKey;

          return (
            <React.Fragment key={`${activity.commit_id}-${activity.name}-${index}`}>
              {showDivider && (
                <div
                  className="sticky top-0 bg-gray-50 dark:bg-dark-surface px-4 py-2 text-xs font-semibold text-gray-500 dark:text-gray-400 border-b border-gray-100 dark:border-gray-800"
                  data-testid="day-divider"
                >
                  {dayKey}
                </div>
              )}
              <ActivityItem activity={activity} />
            </React.Fragment>
          );
        })}
        <div
          ref={infiniteScroll.sentinelRef as React.RefObject<HTMLDivElement>}
          className="h-4"
          data-testid="scroll-sentinel"
        />
        {infiniteScroll.loading && (
          <div className="flex justify-center py-4">
            <div className="text-sm text-gray-400">Loading more...</div>
          </div>
        )}
      </div>
    </PullToRefreshContainer>
  );
}
