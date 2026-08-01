import { useState, useEffect } from 'react';
import { ChevronLeft, Check } from 'lucide-react';
import { getAccountInfo, updateAccountInfo } from '../../lib/api';
import type { AccountInfo } from '../../lib/api';
import Avatar from '../ui/Avatar';
import StorageUsageBar from '../settings/StorageUsageBar';

export default function SettingsPage() {
  const [account, setAccount] = useState<AccountInfo | null>(null);
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');

  useEffect(() => {
    getAccountInfo()
      .then((info) => {
        setAccount(info);
        setName(info.name ?? '');
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load account'));
  }, []);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const dirty = account != null && name.trim() !== (account.name ?? '');

  const handleSave = async () => {
    if (!dirty) return;
    setSaving(true);
    setError('');
    try {
      const updated = await updateAccountInfo({ name: name.trim() });
      setAccount(updated);
      setName(updated.name ?? '');
      showToast('Profile updated');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update profile');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col p-4 gap-6" data-testid="settings-page">
      <div className="flex items-center gap-2">
        <a
          href="/more/"
          className="p-1 -ml-1 text-text dark:text-dark-text"
          aria-label="Back"
          data-testid="settings-back"
        >
          <ChevronLeft size={24} />
        </a>
        <h1 className="text-xl font-medium text-text dark:text-dark-text">Settings</h1>
      </div>

      {error && (
        <div className="text-sm text-red-500" data-testid="settings-error">
          {error}
        </div>
      )}

      {account && (
        <>
          {/* Profile / display name */}
          <div>
            <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Profile</h2>
            <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border p-4 flex flex-col gap-4">
              <div className="flex items-center gap-3">
                <Avatar name={account.name || account.email} src={account.avatar_url} size="lg" />
                <div className="flex-1 min-w-0">
                  <p
                    className="text-sm text-gray-500 dark:text-gray-400 truncate"
                    data-testid="settings-email"
                  >
                    {account.email}
                  </p>
                  {account.is_staff && (
                    <p
                      className="text-xs text-primary font-medium"
                      data-testid="settings-role"
                    >
                      Administrator
                    </p>
                  )}
                </div>
              </div>

              <div className="flex flex-col gap-1">
                <label
                  htmlFor="settings-name"
                  className="text-sm font-medium text-text dark:text-dark-text"
                >
                  Display name
                </label>
                <input
                  id="settings-name"
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="w-full px-3 py-2 min-h-[44px] rounded-lg border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-bg text-text dark:text-dark-text focus:outline-none focus:ring-2 focus:ring-primary"
                  data-testid="settings-name-input"
                />
                <button
                  onClick={handleSave}
                  disabled={!dirty || saving}
                  className="mt-2 self-end px-4 py-2 min-h-[44px] rounded-lg bg-primary text-white font-medium disabled:opacity-50"
                  data-testid="settings-name-save"
                >
                  <span className="flex items-center gap-2">
                    <Check size={16} />
                    {saving ? 'Saving...' : 'Save'}
                  </span>
                </button>
              </div>
            </div>
          </div>

          {/* Storage / quota */}
          <div>
            <h2 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Storage</h2>
            <div className="bg-white dark:bg-dark-surface rounded-lg border border-gray-200 dark:border-dark-border p-4">
              <StorageUsageBar used={account.usage} total={account.total} />
            </div>
          </div>
        </>
      )}

      {toast && (
        <div
          className="fixed bottom-20 left-1/2 -translate-x-1/2 bg-gray-900 text-white text-sm px-4 py-2 rounded-lg z-[60]"
          data-testid="settings-toast"
        >
          {toast}
        </div>
      )}
    </div>
  );
}
