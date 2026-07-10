import React, { useState, useEffect } from 'react';
import BottomSheet from '../ui/BottomSheet';
import { renameFile, renameDir } from '../../lib/api';
import { getPageOptions } from '../../lib/config';
import type { Dirent } from '../../lib/models';

interface RenameDialogProps {
  isOpen: boolean;
  onClose: () => void;
  dirent: Dirent | null;
  repoId: string;
  path: string;
  onSuccess: () => void;
}

export default function RenameDialog({ isOpen, onClose, dirent, repoId, path, onSuccess }: RenameDialogProps) {
  const [name, setName] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (dirent && isOpen) {
      setName(dirent.name);
      setError('');
    }
  }, [dirent, isOpen]);

  if (!dirent) return null;

  const maxLength = getPageOptions().maxFileName;

  const validate = (value: string): string => {
    if (!value.trim()) return 'Name cannot be empty';
    if (value.includes('/')) return 'Name cannot contain slashes';
    if (value.length > maxLength) return `Name cannot exceed ${maxLength} characters`;
    return '';
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    const validationError = validate(trimmed);
    if (validationError) {
      setError(validationError);
      return;
    }

    setLoading(true);
    setError('');
    try {
      const fullPath = path === '/' ? `/${dirent.name}` : `${path}/${dirent.name}`;
      if (dirent.type === 'dir') {
        await renameDir(repoId, fullPath, trimmed);
      } else {
        await renameFile(repoId, fullPath, trimmed);
      }
      onSuccess();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Rename failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <BottomSheet isOpen={isOpen} onClose={onClose} title="Rename">
      <form onSubmit={handleSave}>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full border border-gray-300 rounded-lg px-4 py-3 min-h-[44px] text-base focus:outline-none focus:ring-2 focus:ring-primary mb-3"
          autoFocus
          aria-label="New name"
          data-testid="rename-input"
        />
        {error && (
          <p role="alert" className="text-red-500 text-sm mb-3">{error}</p>
        )}
        <button
          type="submit"
          disabled={loading || !name.trim()}
          className="w-full bg-primary-button text-white rounded-lg py-3 min-h-[44px] font-medium disabled:opacity-50"
          data-testid="rename-submit"
        >
          {loading ? 'Saving...' : 'Save'}
        </button>
      </form>
    </BottomSheet>
  );
}
