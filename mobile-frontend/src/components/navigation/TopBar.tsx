import React from 'react';
import { Search } from 'lucide-react';
import Logo from '../common/Logo';

interface TopBarProps {
  title: string;
  avatarUrl?: string;
  userName?: string;
}

export default function TopBar({ title, avatarUrl, userName }: TopBarProps) {
  const initials = userName
    ? userName.charAt(0).toUpperCase()
    : '?';

  return (
    <header
      className="fixed top-0 left-0 right-0 z-50 flex h-14 items-center justify-between border-b border-gray-200 bg-white px-4 shadow-sm dark:bg-dark-surface dark:border-dark-border dark:text-dark-text"
      style={{ paddingTop: 'env(safe-area-inset-top)' }}
    >
      <div className="flex items-center gap-2 min-w-0">
        {/* Brand logo — same config-driven mechanism as the web frontend. */}
        <Logo height={28} className="flex shrink-0 items-center" />
        <h1 className="text-lg font-medium truncate">{title}</h1>
      </div>
      <div className="flex items-center gap-2">
        <a
          href="/search/"
          className="flex h-10 w-10 items-center justify-center rounded-full"
          aria-label="Search"
        >
          <Search size={22} />
        </a>
        <a
          href="/more/"
          className="flex h-8 w-8 items-center justify-center rounded-full bg-gray-200 overflow-hidden"
          aria-label="Profile"
        >
          {avatarUrl ? (
            <img src={avatarUrl} alt="" className="h-full w-full object-cover" />
          ) : (
            <span className="text-sm font-medium text-gray-600">{initials}</span>
          )}
        </a>
      </div>
    </header>
  );
}
