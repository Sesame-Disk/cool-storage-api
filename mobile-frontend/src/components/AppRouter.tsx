import { useEffect, useState } from 'react';
import FileBrowser from './pages/FileBrowser';
import TrashPage from './pages/TrashPage';
import HistoryPage from './pages/HistoryPage';
import TagsPage from './pages/TagsPage';
import CustomPermissionsPage from './pages/CustomPermissionsPage';
import GroupDetail from './pages/GroupDetail';
import { serviceURL } from '../lib/config';
import { authHeaders } from '../lib/api';

// Client-side router for dynamic routes. The mobile app is a static Astro MPA,
// so paths like /libraries/:id/... and /groups/:id have no prebuilt HTML. This
// component is mounted from 404.astro (Astro dev serves it for unmatched routes;
// nginx falls back to /404.html) and renders the right island by inspecting the
// URL. Top-level routes (/libraries/, /starred/, ...) remain real static pages.

type Route =
  | { kind: 'trash' }
  | { kind: 'history' }
  | { kind: 'tags' }
  | { kind: 'permissions' }
  | { kind: 'library'; repoId: string; initialPath: string }
  | { kind: 'group'; groupId: string }
  | { kind: 'notfound' };

function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean);
  if (parts[0] === 'libraries' && parts[1]) {
    if (parts[2] === 'trash') return { kind: 'trash' };
    if (parts[2] === 'history') return { kind: 'history' };
    if (parts[2] === 'tags') return { kind: 'tags' };
    if (parts[2] === 'permissions') return { kind: 'permissions' };
    const rest = parts.slice(2).map(decodeURIComponent);
    return { kind: 'library', repoId: parts[1], initialPath: '/' + rest.join('/') };
  }
  if (parts[0] === 'groups' && parts[1]) return { kind: 'group', groupId: parts[1] };
  return { kind: 'notfound' };
}

export default function AppRouter() {
  // Parse on the client only — this island is server-rendered by Astro where
  // `window` is undefined.
  const [route, setRoute] = useState<Route | null>(null);
  const [repo, setRepo] = useState<{ repoName?: string; encrypted?: boolean } | null>(null);

  useEffect(() => {
    setRoute(parseRoute(window.location.pathname));
  }, []);

  useEffect(() => {
    if (route?.kind !== 'library') return;
    fetch(`${serviceURL()}/api2/repos/${route.repoId}/`, { headers: authHeaders() })
      .then((r) => (r.ok ? r.json() : null))
      .then((d) =>
        setRepo(d ? { repoName: d.repo_name ?? d.name, encrypted: !!d.encrypted } : {}),
      )
      .catch(() => setRepo({}));
  }, [route]);

  if (!route) return <p className="text-center text-gray-500 py-10">Loading…</p>;
  if (route.kind === 'trash') return <TrashPage />;
  if (route.kind === 'history') return <HistoryPage />;
  if (route.kind === 'tags') return <TagsPage />;
  if (route.kind === 'permissions') return <CustomPermissionsPage />;
  if (route.kind === 'group') return <GroupDetail groupId={route.groupId} />;
  if (route.kind === 'library') {
    if (repo === null) {
      return <p className="text-center text-gray-500 py-10">Loading…</p>;
    }
    return (
      <FileBrowser
        repoId={route.repoId}
        repoName={repo.repoName}
        encrypted={repo.encrypted}
        initialPath={route.initialPath}
      />
    );
  }
  return (
    <div className="flex flex-col items-center justify-center p-10 text-center" data-testid="not-found">
      <p className="text-lg font-semibold text-text dark:text-dark-text">Page not found</p>
      <a href="/libraries/" className="text-primary mt-3 min-h-[44px]">
        Go to My Libraries
      </a>
    </div>
  );
}
