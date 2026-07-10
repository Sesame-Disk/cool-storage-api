import React from 'react';
import { Monitor, ExternalLink, ArrowLeft } from 'lucide-react';
import { DESKTOP_ROUTES, desktopHref } from './desktopRoutes';
import type { DesktopScreen } from './desktopRoutes';

interface OpenInDesktopProps {
  /** Which desktop org-admin screen to link to. */
  screen: DesktopScreen;
  /** Human-readable feature name (e.g. "Statistics"). */
  title: string;
  /** Short explanation of why it's desktop-only. */
  description?: string;
}

/**
 * Placeholder page for heavy org-admin screens the mobile app does not build
 * natively. Renders a message plus a link to the desktop web app's /org/... route.
 */
export default function OpenInDesktop({ screen, title, description }: OpenInDesktopProps) {
  const href = desktopHref(DESKTOP_ROUTES[screen]);
  return (
    <div
      className="flex flex-col items-center justify-center min-h-[60vh] px-6 text-center gap-6"
      data-testid="open-in-desktop-page"
    >
      <div className="text-gray-400 dark:text-gray-500">
        <Monitor size={56} />
      </div>
      <div>
        <h1 className="text-xl font-semibold text-text dark:text-dark-text mb-2">{title}</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400 max-w-xs mx-auto">
          {description ||
            'This section is only available in the desktop version. Open it there to continue.'}
        </p>
      </div>
      <a
        href={href}
        data-testid="open-in-desktop"
        className="inline-flex items-center justify-center gap-2 px-5 py-3 bg-primary text-white rounded-lg text-sm font-medium min-h-[44px]"
      >
        <ExternalLink size={18} />
        Open in the desktop version
      </a>
      <a
        href="/org/"
        className="inline-flex items-center gap-1 text-sm text-primary font-medium min-h-[44px]"
        data-testid="open-in-desktop-back"
      >
        <ArrowLeft size={16} />
        Back to Org Admin
      </a>
    </div>
  );
}
