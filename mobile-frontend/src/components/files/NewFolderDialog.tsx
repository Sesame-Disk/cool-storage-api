import React, { useState } from 'react';
import { X } from 'lucide-react';
import { getAuthToken, invalidateApiCache } from '../../lib/api';
import { serviceURL } from '../../lib/config';

interface NewFolderDialogProps {
  isOpen: boolean;
  onClose: () => void;
  repoId: string;
  path: string;
  onSuccess: (folderName: string) => void;
}

async function createFolder(repoId: string, path: string): Promise<void> {
  const token = getAuthToken();
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(path)}`, {
    method: 'POST',
    headers: {
      'Authorization': `Token ${token}`,
      'Accept': 'application/json',
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: new URLSearchParams({ operation: 'mkdir' }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to create folder');
  }
  // Drop the cached directory listing so the new folder shows on refresh
  // (the service worker caches GET /api2/... stale-while-revalidate).
  await invalidateApiCache(`/api2/repos/${repoId}`);
}

export default function NewFolderDialog({ isOpen, onClose, repoId, path, onSuccess }: NewFolderDialogProps) {
  const [name, setName] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;

    setLoading(true);
    setError('');
    try {
      const folderPath = path === '/' ? `/${trimmed}` : `${path}/${trimmed}`;
      await createFolder(repoId, folderPath);
      setName('');
      onClose();
      onSuccess(trimmed);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create folder');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" data-testid="new-folder-dialog">
      <div className="fixed inset-0 bg-black/30" onClick={onClose} />
      <div className="relative bg-white rounded-2xl shadow-xl w-full max-w-sm p-6">
        <button
          onClick={onClose}
          className="absolute top-3 right-3 min-h-[44px] min-w-[44px] flex items-center justify-center"
        >
          <X className="w-5 h-5 text-gray-400" />
        </button>

        <h3 className="text-lg font-medium text-text mb-4">New Folder</h3>

        <form onSubmit={handleSubmit}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Folder name"
            className="w-full px-3 py-2 border border-gray-300 rounded-lg text-text focus:outline-none focus:ring-2 focus:ring-primary/50 min-h-[44px]"
            autoFocus
            data-testid="folder-name-input"
          />
          {error && <p className="text-sm text-red-500 mt-2">{error}</p>}
          <div className="flex gap-2 mt-4">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 py-2 text-gray-500 rounded-lg font-medium min-h-[44px]"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!name.trim() || loading}
              className="flex-1 py-2 bg-primary text-white rounded-lg font-medium disabled:opacity-50 min-h-[44px]"
              data-testid="create-folder-btn"
            >
              {loading ? 'Creating...' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
