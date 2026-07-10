// Desktop (web app) route targets for org-admin screens that the mobile app
// does NOT implement natively. Each mobile "Open in desktop" page links to the
// corresponding web-app React route under /org/...
//
// DESKTOP_URL is the base origin of the desktop web app. It defaults to '' so
// links resolve as relative paths on the SAME origin (e.g. "/org/useradmin").
// Set it to an absolute origin (e.g. "https://app.example.com") if the desktop
// app is served from a different host.
export const DESKTOP_URL = '';

/** Build an absolute-or-relative desktop URL for a web-app path. */
export function desktopHref(path: string): string {
  const clean = path.startsWith('/') ? path : `/${path}`;
  return `${DESKTOP_URL}${clean}`;
}

// Web-app React-router paths (see frontend/src/pages/org-admin/index.js).
export const DESKTOP_ROUTES = {
  statistics: '/org/statistics-admin/file/',
  logs: '/org/logadmin',
  devices: '/org/deviceadmin/desktop-devices/',
  departments: '/org/departmentadmin',
  subscription: '/org/subscription',
  saml: '/org/samlconfig/',
  // Native equivalents (kept here for reference / cross-linking):
  users: '/org/useradmin',
  groups: '/org/groupadmin',
  repos: '/org/repoadmin',
  links: '/org/publinkadmin',
  settings: '/org/web-settings',
} as const;

export type DesktopScreen = keyof typeof DESKTOP_ROUTES;
