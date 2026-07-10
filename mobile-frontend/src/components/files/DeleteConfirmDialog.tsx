import React, { useState } from 'react';
import BottomSheet from '../ui/BottomSheet';
import { deleteFile, deleteDir } from '../../lib/api';
import type { Dirent } from '../../lib/models';

interface DeleteConfirmDialogProps {
  isOpen: boolean;
  onClose: () => void;
  items: Dirent[];
  repoId: string;
  path: string;
  onSuccess: () => void;
}

export default function DeleteConfirmDialog({ isOpen, onClose, items, repoId, path, onSuccess }: DeleteConfirmDialogProps) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const message = items.length === 1
    ? `Delete "${items[0].name}"?`
    : `Delete ${items.length} items?`;

  const handleDelete = async () => {
    setLoading(true);
    setError('');
    try {
      for (const item of items) {
        const fullPath = path === '/' ? `/${item.name}` : `${path}/${item.name}`;
        if (item.type === 'dir') {
          await deleteDir(repoId, fullPath);
        } else {
          await deleteFile(repoId, fullPath);
        }
      }
      onSuccess();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <BottomSheet isOpen={isOpen} onClose={onClose} title="Confirm Delete">
      <p className="text-text mb-4">{message}</p>
      {error && (
        <p role="alert" className="text-red-500 text-sm mb-3">{error}</p>
      )}
      <div className="flex gap-3">
        <button
          onClick={onClose}
          disabled={loading}
          className="flex-1 border border-gray-300 rounded-lg py-3 min-h-[44px] font-medium"
        >
          Cancel
        </button>
        <button
          onClick={handleDelete}
          disabled={loading}
          className="flex-1 bg-red-500 text-white rounded-lg py-3 min-h-[44px] font-medium disabled:opacity-50"
          data-testid="delete-confirm"
        >
          {loading ? 'Deleting...' : 'Delete'}
        </button>
      </div>
    </BottomSheet>
  );
}
