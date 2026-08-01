import React, { useEffect, useState } from 'react';
import {
  Users,
  UsersRound,
  FolderGit2,
  Link2,
  Settings,
  BarChart3,
  ScrollText,
  MonitorSmartphone,
  Building2,
  CreditCard,
  KeyRound,
  ChevronRight,
  Monitor,
} from 'lucide-react';
import { getAccountInfo } from '../../lib/api';
import type { AccountInfo } from '../../lib/api';

interface NavEntry {
  href: string;
  label: string;
  icon: typeof Users;
  testid: string;
  desktopOnly?: boolean;
}

const CORE: NavEntry[] = [
  { href: '/org/users/', label: 'Users', icon: Users, testid: 'org-nav-users' },
  { href: '/org/groups/', label: 'Groups', icon: UsersRound, testid: 'org-nav-groups' },
  { href: '/org/libraries/', label: 'Libraries', icon: FolderGit2, testid: 'org-nav-libraries' },
  { href: '/org/share-links/', label: 'Share Links', icon: Link2, testid: 'org-nav-share-links' },
  { href: '/org/settings/', label: 'Settings', icon: Settings, testid: 'org-nav-settings' },
];

const DESKTOP: NavEntry[] = [
  { href: '/org/statistics/', label: 'Statistics', icon: BarChart3, testid: 'org-nav-statistics', desktopOnly: true },
  { href: '/org/logs/', label: 'Audit Logs', icon: ScrollText, testid: 'org-nav-logs', desktopOnly: true },
  { href: '/org/devices/', label: 'Devices', icon: MonitorSmartphone, testid: 'org-nav-devices', desktopOnly: true },
  { href: '/org/departments/', label: 'Departments', icon: Building2, testid: 'org-nav-departments', desktopOnly: true },
  { href: '/org/subscription/', label: 'Subscription', icon: CreditCard, testid: 'org-nav-subscription', desktopOnly: true },
  { href: '/org/saml/', label: 'SAML / SSO', icon: KeyRound, testid: 'org-nav-saml', desktopOnly: true },
];

function NavRow({ entry, last }: { entry: NavEntry; last: boolean }) {
  const Icon = entry.icon;
  return (
    <a
      href={entry.href}
      data-testid={entry.testid}
      className={`w-full flex items-center gap-3 px-4 py-3 min-h-[44px] text-text dark:text-dark-text ${
        last ? '' : 'border-b border-gray-200 dark:border-dark-border'
      }`}
    >
      <Icon size={20} />
      <span className="flex-1">{entry.label}</span>
      {entry.desktopOnly && <Monitor size={14} className="text-gray-400" />}
      <ChevronRight size={16} className="text-gray-400" />
    </a>
  );
}

export default function OrgAdminHome() {
  const [account, setAccount] = useState<AccountInfo | null>(null);

  useEffect(() => {
    getAccountInfo().then(setAccount).catch(() => {});
  }, []);

  return (
    <div className="flex flex-col p-4 gap-6" data-testid="org-admin-home">
      <div>
        <h1 className="text-xl font-medium text-text dark:text-dark-text">Org Admin</h1>
        {account?.institution && (
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1" data-testid="org-admin-org-id">
            {account.institution}
          </p>
        )}
      </div>

      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Manage</h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
          {CORE.map((e, i) => (
            <NavRow key={e.testid} entry={e} last={i === CORE.length - 1} />
          ))}
        </div>
      </div>

      <div>
        <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">
          Open in the desktop version
        </h2>
        <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border overflow-hidden">
          {DESKTOP.map((e, i) => (
            <NavRow key={e.testid} entry={e} last={i === DESKTOP.length - 1} />
          ))}
        </div>
      </div>
    </div>
  );
}
