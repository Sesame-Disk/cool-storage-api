import React, { useState, useEffect } from 'react';
import { Sun, Moon, Monitor, Link2, ChevronRight, Activity, ArrowUpDown, LogOut, Info, HelpCircle, SlidersHorizontal, Trash2, Building2, FolderSync } from 'lucide-react';
import { useTheme } from '../../lib/hooks/useTheme';
import type { ThemeOption } from '../../lib/hooks/useTheme';
import { getAccountInfo, logout } from '../../lib/api';
import type { AccountInfo } from '../../lib/api';
import Avatar from '../ui/Avatar';
import StorageUsageBar from '../settings/StorageUsageBar';
import SortPreferenceSheet from '../settings/SortPreferenceSheet';

const themeOptions: { value: ThemeOption; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
];

export default function MorePage() {
  const { theme, setTheme } = useTheme();
  const [account, setAccount] = useState<AccountInfo | null>(null);
  const [loggingOut, setLoggingOut] = useState(false);
  const [sortSheetOpen, setSortSheetOpen] = useState(false);

  useEffect(() => {
    getAccountInfo().then(setAccount).catch(() => {});
  }, []);

  // Show the Org Admin entry only for org admins (account info `is_org_staff`,
  // which the backend returns as 0/1). Truthy for either 1 or true.
  const isOrgAdmin = Boolean(account?.is_org_staff);

  const handleLogout = async () => {
    setLoggingOut(true);
    try {
      await logout();
    } catch {
      // Still redirect on failure since token is cleared
    }
    window.location.href = '/login/';
  };

  return (
    <div className="flex flex-col p-4 gap-6">
      <h1 className="text-xl font-medium text-text dark:text-dark-text">Settings</h1>

      {/* Profile section */}
      {account && (
        <div>
          <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Profile</h2>
          <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border p-4">
            <div className="flex items-center gap-3 mb-4">
              <Avatar name={account.name || account.email} src={account.avatar_url} size="lg" />
              <div className="flex-1 min-w-0">
                <p className="text-base font-medium text-text dark:text-dark-text truncate" data-testid="profile-name">
                  {account.name || 'User'}
                </p>
                <p className="text-sm text-gray-500 dark:text-gray-400 truncate" data-testid="profile-email">
                  {account.email}
                </p>
              </div>
            </div>
            <StorageUsageBar used={account.usage} total={account.total} />
          </div>
        </div>
      )}

      {/* Features */}
      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Features</h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden" data-testid="more-features">
          <a
            href="/settings/"
            className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text border-b border-gray-200 dark:border-dark-border"
            data-testid="more-link-settings"
          >
            <SlidersHorizontal size={20} />
            <span className="flex-1">Settings</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
          <a
            href="/deleted-libraries/"
            className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text border-b border-gray-200 dark:border-dark-border"
            data-testid="more-link-deleted-libraries"
          >
            <Trash2 size={20} />
            <span className="flex-1">Deleted Libraries</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
          <a
            href="/sync/"
            className={`w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text ${
              isOrgAdmin ? 'border-b border-gray-200 dark:border-dark-border' : ''
            }`}
            data-testid="more-link-sync"
          >
            <FolderSync size={20} />
            <span className="flex-1">Folder Sync</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
          {isOrgAdmin && (
            <a
              href="/org/"
              className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text"
              data-testid="more-link-org-admin"
            >
              <Building2 size={20} />
              <span className="flex-1">Org Admin</span>
              <ChevronRight size={16} className="text-gray-400" />
            </a>
          )}
        </div>
      </div>

      {/* Navigation */}
      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Navigation</h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
          <a
            href="/activity/"
            className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text border-b border-gray-200 dark:border-dark-border"
          >
            <Activity size={20} />
            <span className="flex-1">Activity</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
          <a
            href="/share-admin/"
            className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text border-b border-gray-200 dark:border-dark-border"
          >
            <Link2 size={20} />
            <span className="flex-1">My Shares</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
          <button
            onClick={() => setSortSheetOpen(true)}
            className="w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text"
          >
            <ArrowUpDown size={20} />
            <span className="flex-1 text-left">Sort Preference</span>
            <ChevronRight size={16} className="text-gray-400" />
          </button>
        </div>
      </div>

      {/* Theme selector */}
      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Appearance</h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
          {themeOptions.map(({ value, label, icon: Icon }) => (
            <button
              key={value}
              onClick={() => setTheme(value)}
              className={`w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-left border-b last:border-b-0 border-gray-200 dark:border-dark-border ${
                theme === value
                  ? 'text-primary bg-primary/5'
                  : 'text-text dark:text-dark-text'
              }`}
            >
              <Icon size={20} />
              <span className="flex-1">{label}</span>
              {theme === value && (
                <span className="text-primary text-sm font-medium">Active</span>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* About */}
      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">About</h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
          <div className="flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text border-b border-gray-200 dark:border-dark-border">
            <Info size={20} />
            <span className="flex-1">Version</span>
            <span className="text-sm text-gray-400">1.0.0</span>
          </div>
          <a
            href="https://help.seafile.com"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text"
          >
            <HelpCircle size={20} />
            <span className="flex-1">Help</span>
            <ChevronRight size={16} className="text-gray-400" />
          </a>
        </div>
      </div>

      {/* Logout */}
      <button
        onClick={handleLogout}
        disabled={loggingOut}
        className="w-full py-3 text-red-500 font-medium bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border min-h-[44px] disabled:opacity-50"
        data-testid="logout-button"
      >
        <span className="flex items-center justify-center gap-2">
          <LogOut size={20} />
          {loggingOut ? 'Logging out...' : 'Log Out'}
        </span>
      </button>

      {/* Sort preference sheet */}
      <SortPreferenceSheet isOpen={sortSheetOpen} onClose={() => setSortSheetOpen(false)} />
    </div>
  );
}
