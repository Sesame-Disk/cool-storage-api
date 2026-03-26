import React, { useState, useEffect, useCallback } from 'react';
import TopBar from './navigation/TopBar';
import BottomNav from './navigation/BottomNav';
import { OfflineBanner } from './ui';
import OperationsIndicator from './operations/OperationsIndicator';

// Page components
import LibraryList from './pages/LibraryList';
import FileBrowser from './pages/FileBrowser';
import FileHistory from './pages/FileHistory';
import TrashPage from './pages/TrashPage';
import SharedLibraries from './pages/SharedLibraries';
import GroupList from './pages/GroupList';
import GroupDetail from './pages/GroupDetail';
import StarredFiles from './pages/StarredFiles';
import MorePage from './pages/MorePage';
import ActivityFeed from './pages/ActivityFeed';
import SearchPage from './pages/SearchPage';
import NotificationsPage from './pages/NotificationsPage';
import ShareAdminPage from './pages/ShareAdmin';
import LinkedDevicesPage from './pages/LinkedDevicesPage';

// Auth
import { getAuthToken } from '../lib/api';

interface Route {
  page: string;
  params: Record<string, string>;
  title: string;
}

function matchRoute(pathname: string): Route {
  const search = new URLSearchParams(window.location.search);

  // /libraries/:repoId/history
  let m = pathname.match(/^\/libraries\/([a-zA-Z0-9_-]+)\/history\/?$/);
  if (m) {
    return {
      page: 'file-history',
      params: {
        repoId: m[1],
        path: search.get('path') || '/',
        fileName: search.get('fileName') || 'File',
      },
      title: 'File History',
    };
  }

  // /libraries/:repoId/trash
  m = pathname.match(/^\/libraries\/([a-zA-Z0-9_-]+)\/trash\/?$/);
  if (m) return { page: 'trash', params: { repoId: m[1] }, title: 'Trash' };

  // /libraries/:repoId/[...path]
  m = pathname.match(/^\/libraries\/([a-zA-Z0-9_-]+)\/?(.*)$/);
  if (m) {
    const restPath = m[2] ? `/${m[2].replace(/\/$/, '')}` : '/';
    return { page: 'file-browser', params: { repoId: m[1], path: restPath }, title: 'Files' };
  }

  // /groups/:groupId
  m = pathname.match(/^\/groups\/(\d+)\/?$/);
  if (m) return { page: 'group-detail', params: { groupId: m[1] }, title: 'Group' };

  // Static routes
  const staticRoutes: Record<string, { page: string; title: string }> = {
    '/libraries': { page: 'libraries', title: 'My Libraries' },
    '/shared': { page: 'shared', title: 'Shared Libraries' },
    '/groups': { page: 'groups', title: 'Groups' },
    '/starred': { page: 'starred', title: 'Starred Files' },
    '/more': { page: 'more', title: 'Settings' },
    '/activity': { page: 'activity', title: 'Activity' },
    '/search': { page: 'search', title: 'Search' },
    '/notifications': { page: 'notifications', title: 'Notifications' },
    '/share-admin': { page: 'share-admin', title: 'My Shares' },
    '/devices': { page: 'devices', title: 'Linked Devices' },
  };

  // Normalize: remove trailing slash
  const normalized = pathname.replace(/\/$/, '') || '/';
  const found = staticRoutes[normalized];
  if (found) return { ...found, params: {} };

  // Default: redirect to libraries
  return { page: 'libraries', params: {}, title: 'My Libraries' };
}

function renderPage(route: Route): React.ReactNode {
  switch (route.page) {
    case 'libraries':
      return <LibraryList />;
    case 'file-browser':
      return <FileBrowser repoId={route.params.repoId} initialPath={route.params.path} />;
    case 'file-history':
      return <FileHistory repoId={route.params.repoId} path={route.params.path} fileName={route.params.fileName} />;
    case 'trash':
      return <TrashPage repoId={route.params.repoId} />;
    case 'shared':
      return <SharedLibraries />;
    case 'groups':
      return <GroupList />;
    case 'group-detail':
      return <GroupDetail groupId={route.params.groupId} />;
    case 'starred':
      return <StarredFiles />;
    case 'more':
      return <MorePage />;
    case 'activity':
      return <ActivityFeed />;
    case 'search':
      return <SearchPage />;
    case 'notifications':
      return <NotificationsPage />;
    case 'share-admin':
      return <ShareAdminPage />;
    case 'devices':
      return <LinkedDevicesPage />;
    default:
      return <LibraryList />;
  }
}

/**
 * Main SPA application shell.
 * Handles client-side routing, auth guard, and shared layout (TopBar, BottomNav).
 */
export default function App() {
  const [route, setRoute] = useState<Route>(() => matchRoute(window.location.pathname));

  const handleNavigation = useCallback(() => {
    setRoute(matchRoute(window.location.pathname));
  }, []);

  useEffect(() => {
    // Listen for browser back/forward
    window.addEventListener('popstate', handleNavigation);
    return () => window.removeEventListener('popstate', handleNavigation);
  }, [handleNavigation]);

  // Auth guard
  useEffect(() => {
    const devBypass = !window.app?.config?.serviceURL;
    const token = getAuthToken();
    if (!token && !devBypass) {
      window.location.href = '/login/';
    }
  }, []);

  return (
    <div className="flex flex-col min-h-screen bg-[#f5f5f5]">
      <TopBar title={route.title} />
      <OfflineBanner />
      <main className="flex-1 pt-14 pb-16">
        {renderPage(route)}
      </main>
      <OperationsIndicator />
      <BottomNav currentPath={window.location.pathname} />
    </div>
  );
}
