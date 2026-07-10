import React, { useState, useEffect, useCallback } from 'react';
import { ShieldCheck, ChevronLeft, Trash2 } from 'lucide-react';
import {
  listCustomPermissions,
  createCustomPermission,
  deleteCustomPermission,
  EMPTY_PERMISSION,
} from '../../lib/api/custom-permissions';
import type { CustomPermission, CustomPermissionOptions } from '../../lib/api/custom-permissions';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

function repoIdFromUrl(): string {
  // /libraries/<repoId>/permissions
  const parts = window.location.pathname.split('/').filter(Boolean);
  const idx = parts.indexOf('libraries');
  return idx >= 0 ? (parts[idx + 1] ?? '') : '';
}

// Toggles surfaced in the add form. Mirrors the web custom-permission-editor.
const TOGGLES: { key: keyof CustomPermissionOptions; label: string }[] = [
  { key: 'download', label: 'Download' },
  { key: 'upload', label: 'Upload' },
  { key: 'modify', label: 'Modify' },
];

function summarize(permission: Partial<CustomPermissionOptions>): string {
  const enabled = Object.entries(permission)
    .filter(([, v]) => v === true)
    .map(([k]) => k);
  return enabled.length ? enabled.join(', ') : 'No permissions';
}

export default function CustomPermissionsPage() {
  const [repoId, setRepoId] = useState('');
  const [items, setItems] = useState<CustomPermission[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');

  const [showAdd, setShowAdd] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [perm, setPerm] = useState<CustomPermissionOptions>({ ...EMPTY_PERMISSION });
  const [submitting, setSubmitting] = useState(false);

  const [confirmDelete, setConfirmDelete] = useState<CustomPermission | null>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const fetchPerms = useCallback(async (id: string) => {
    try {
      const list = await listCustomPermissions(id);
      setItems(list);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load custom permissions');
    }
  }, []);

  useEffect(() => {
    const id = repoIdFromUrl();
    setRepoId(id);
    fetchPerms(id).finally(() => setLoading(false));
  }, [fetchPerms]);

  const resetForm = () => {
    setName('');
    setDescription('');
    setPerm({ ...EMPTY_PERMISSION });
  };

  const handleCreate = async () => {
    if (!name.trim()) {
      showToast('Name is required');
      return;
    }
    setSubmitting(true);
    try {
      const created = await createCustomPermission(repoId, {
        name: name.trim(),
        description: description.trim(),
        permission: perm,
      });
      setItems((prev) => [created, ...prev]);
      setShowAdd(false);
      resetForm();
      showToast('Permission created');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to create permission');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    const target = confirmDelete;
    if (!target) return;
    setConfirmDelete(null);
    try {
      await deleteCustomPermission(repoId, target.id);
      setItems((prev) => prev.filter((p) => p.id !== target.id));
      showToast('Permission deleted');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete permission');
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="perms-page">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <a href={`/libraries/${repoId}/`} aria-label="Back" className="p-1 -ml-1 text-gray-500 min-h-[44px] flex items-center">
            <ChevronLeft className="w-6 h-6" />
          </a>
          <h1 className="text-xl font-semibold text-text dark:text-dark-text truncate">Permissions</h1>
        </div>
        <button
          onClick={() => setShowAdd(true)}
          data-testid="perm-add-open"
          className="text-sm text-primary font-medium min-h-[44px]"
        >
          Add
        </button>
      </div>

      {error && items.length === 0 ? (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <p role="alert" className="text-red-500 mb-4">{error}</p>
          <button onClick={() => fetchPerms(repoId)} className="text-primary font-medium min-h-[44px]">Retry</button>
        </div>
      ) : (
        <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={4} />}>
          {items.length === 0 ? (
            <EmptyState
              icon={<ShieldCheck className="w-12 h-12" />}
              title="No custom permissions"
              description="Create custom share-permission profiles to control what recipients can do with this library."
            />
          ) : (
            <div className="flex flex-col pb-20" data-testid="perms-list">
              {items.map((item) => (
                <div
                  key={item.id}
                  data-testid="perm-item"
                  className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
                >
                  <ShieldCheck className="w-10 h-10 text-primary flex-shrink-0" />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-text dark:text-dark-text truncate">{item.name}</p>
                    {item.description && (
                      <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{item.description}</p>
                    )}
                    <p className="text-xs text-gray-400 dark:text-gray-500 truncate">{summarize(item.permission)}</p>
                  </div>
                  <button
                    onClick={() => setConfirmDelete(item)}
                    data-testid="perm-delete"
                    aria-label={`Delete ${item.name}`}
                    className="text-red-500 p-2 min-h-[44px]"
                  >
                    <Trash2 className="w-5 h-5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </ContentCrossfade>
      )}

      {showAdd && (
        <div
          className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4"
          onClick={() => !submitting && setShowAdd(false)}
        >
          <div
            className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4"
            onClick={(e) => e.stopPropagation()}
            data-testid="perm-add-form"
          >
            <p className="font-semibold text-text dark:text-dark-text mb-3">Add permission</p>
            <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Name</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              data-testid="perm-add-name"
              className="w-full rounded-lg border border-gray-300 dark:border-dark-border bg-transparent px-3 py-2 mb-3 text-text dark:text-dark-text"
              placeholder="e.g. Reviewer"
            />
            <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Description</label>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              data-testid="perm-add-description"
              className="w-full rounded-lg border border-gray-300 dark:border-dark-border bg-transparent px-3 py-2 mb-3 text-text dark:text-dark-text"
              placeholder="Optional"
            />
            <div className="mb-4 flex flex-col gap-2">
              {TOGGLES.map((t) => (
                <label key={t.key} className="flex items-center justify-between min-h-[44px]">
                  <span className="text-sm text-text dark:text-dark-text">{t.label}</span>
                  <input
                    type="checkbox"
                    checked={perm[t.key]}
                    onChange={(e) => setPerm((prev) => ({ ...prev, [t.key]: e.target.checked }))}
                    data-testid={`perm-add-${t.key}`}
                    className="w-5 h-5"
                  />
                </label>
              ))}
            </div>
            <div className="flex gap-3">
              <button
                onClick={() => setShowAdd(false)}
                disabled={submitting}
                className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]"
              >
                Cancel
              </button>
              <button
                onClick={handleCreate}
                disabled={submitting}
                data-testid="perm-add-submit"
                className="flex-1 rounded-full bg-primary text-white py-2 font-medium min-h-[44px]"
              >
                {submitting ? 'Saving…' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {confirmDelete && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4" onClick={() => setConfirmDelete(null)}>
          <div className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4" onClick={(e) => e.stopPropagation()} data-testid="perm-delete-confirm">
            <p className="font-semibold text-text dark:text-dark-text">Delete permission?</p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              This permanently deletes the "{confirmDelete.name}" custom permission profile. This cannot be undone.
            </p>
            <div className="mt-4 flex gap-3">
              <button onClick={() => setConfirmDelete(null)} className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]">Cancel</button>
              <button onClick={handleDelete} data-testid="perm-delete-confirm-yes" className="flex-1 rounded-full bg-red-500 text-white py-2 font-medium min-h-[44px]">Delete</button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div className="fixed bottom-20 inset-x-0 mx-auto max-w-xs rounded-full bg-black/80 text-white text-sm text-center py-2 px-4 z-50" role="status">
          {toast}
        </div>
      )}
    </div>
  );
}
