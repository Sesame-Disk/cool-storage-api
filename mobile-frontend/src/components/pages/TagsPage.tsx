import React, { useState, useEffect, useCallback } from 'react';
import { Tag as TagIcon, Trash2, ChevronLeft, Check } from 'lucide-react';
import {
  listRepoTags,
  createRepoTag,
  updateRepoTag,
  deleteRepoTag,
} from '../../lib/api/tags';
import type { RepoTag } from '../../lib/api/tags';
import SwipeableListItem from '../ui/SwipeableListItem';
import SkeletonList, { ContentCrossfade } from '../ui/SkeletonList';
import EmptyState from '../ui/EmptyState';

// Palette matches the web frontend's TAG_COLORS (frontend/src/constants).
const TAG_COLORS = [
  '#FBD44A', '#EAA775', '#F4667C', '#DC82D2', '#9860E5', '#9F8CF1',
  '#59CB74', '#ADDF84', '#89D2EA', '#4ECCCB', '#46A1FD', '#C2C2C2',
];

function repoIdFromUrl(): string {
  // /libraries/<repoId>/tags
  const parts = window.location.pathname.split('/').filter(Boolean);
  const idx = parts.indexOf('libraries');
  return idx >= 0 ? (parts[idx + 1] ?? '') : '';
}

function ColorSwatches({
  value,
  onChange,
}: {
  value: string;
  onChange: (color: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="Tag color">
      {TAG_COLORS.map((c) => (
        <button
          key={c}
          type="button"
          aria-label={`Color ${c}`}
          aria-pressed={value === c}
          onClick={() => onChange(c)}
          className="w-7 h-7 rounded-full flex items-center justify-center border border-black/10"
          style={{ backgroundColor: c }}
        >
          {value === c && <Check className="w-4 h-4 text-white" />}
        </button>
      ))}
    </div>
  );
}

export default function TagsPage() {
  const [repoId, setRepoId] = useState('');
  const [tags, setTags] = useState<RepoTag[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');

  // Add-tag form.
  const [newName, setNewName] = useState('');
  const [newColor, setNewColor] = useState(TAG_COLORS[0]);
  const [adding, setAdding] = useState(false);

  // Rename modal.
  const [editing, setEditing] = useState<RepoTag | null>(null);
  const [editName, setEditName] = useState('');
  const [editColor, setEditColor] = useState(TAG_COLORS[0]);
  const [savingEdit, setSavingEdit] = useState(false);

  // Delete confirm.
  const [confirmDelete, setConfirmDelete] = useState<RepoTag | null>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  };

  const fetchTags = useCallback(async (id: string) => {
    try {
      setTags(await listRepoTags(id));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load tags');
    }
  }, []);

  useEffect(() => {
    const id = repoIdFromUrl();
    setRepoId(id);
    fetchTags(id).finally(() => setLoading(false));
  }, [fetchTags]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchTags(repoId);
    setRefreshing(false);
  };

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newName.trim();
    if (!name || adding) return;
    setAdding(true);
    try {
      const tag = await createRepoTag(repoId, name, newColor);
      setTags((prev) => [...prev, tag]);
      setNewName('');
      setNewColor(TAG_COLORS[0]);
      showToast(`Added ${tag.name}`);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to add tag');
    } finally {
      setAdding(false);
    }
  };

  const openEdit = (tag: RepoTag) => {
    setEditing(tag);
    setEditName(tag.name);
    setEditColor(tag.color);
  };

  const handleSaveEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editing) return;
    const name = editName.trim();
    if (!name || savingEdit) return;
    setSavingEdit(true);
    try {
      const updated = await updateRepoTag(repoId, editing.id, { name, color: editColor });
      setTags((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
      setEditing(null);
      showToast('Tag updated');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to update tag');
    } finally {
      setSavingEdit(false);
    }
  };

  const handleDelete = async () => {
    if (!confirmDelete) return;
    const tag = confirmDelete;
    setConfirmDelete(null);
    try {
      await deleteRepoTag(repoId, tag.id);
      setTags((prev) => prev.filter((t) => t.id !== tag.id));
      showToast(`Deleted ${tag.name}`);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete tag');
    }
  };

  return (
    <div className="flex flex-col h-full" data-testid="tags-page">
      <div className="px-4 pt-2 pb-1 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <a href={`/libraries/${repoId}/`} aria-label="Back" className="p-1 -ml-1 text-gray-500 min-h-[44px] flex items-center">
            <ChevronLeft className="w-6 h-6" />
          </a>
          <h1 className="text-xl font-semibold text-text dark:text-dark-text truncate">Tags</h1>
        </div>
        <button onClick={handleRefresh} disabled={refreshing} className="text-sm text-primary font-medium min-h-[44px]">
          {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      {/* Add-tag form */}
      <form onSubmit={handleAdd} className="px-4 py-3 border-b border-gray-100 dark:border-dark-border flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <span className="w-6 h-6 rounded-full flex-shrink-0 border border-black/10" style={{ backgroundColor: newColor }} />
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="New tag name"
            data-testid="tag-add-name"
            className="flex-1 min-w-0 rounded-lg border border-gray-300 dark:border-dark-border bg-transparent px-3 py-2 text-sm text-text dark:text-dark-text"
          />
          <button
            type="submit"
            disabled={adding || !newName.trim()}
            data-testid="tag-add-submit"
            className="rounded-full bg-primary text-white text-sm font-medium px-4 py-2 min-h-[44px] disabled:opacity-50"
          >
            Add
          </button>
        </div>
        <ColorSwatches value={newColor} onChange={setNewColor} />
      </form>

      {error && tags.length === 0 ? (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <p role="alert" className="text-red-500 mb-4">{error}</p>
          <button onClick={handleRefresh} className="text-primary font-medium min-h-[44px]">Retry</button>
        </div>
      ) : (
        <ContentCrossfade isLoading={loading} skeleton={<SkeletonList variant="file" count={5} />}>
          {tags.length === 0 ? (
            <EmptyState
              icon={<TagIcon className="w-12 h-12" />}
              title="No tags yet"
              description="Create tags to organize and label files in this library."
            />
          ) : (
            <div className="flex flex-col pb-20" data-testid="tags-list">
              {tags.map((tag) => (
                <SwipeableListItem
                  key={tag.id}
                  rightActions={[
                    {
                      icon: <Trash2 className="w-5 h-5" />,
                      label: 'Delete',
                      color: '#ef4444',
                      onClick: () => setConfirmDelete(tag),
                    },
                  ]}
                >
                  <div
                    className="flex items-center gap-3 px-4 py-3 min-h-[56px] border-b border-gray-100 dark:border-dark-border"
                    data-testid="tag-item"
                  >
                    <span
                      className="w-8 h-8 rounded-full flex-shrink-0 border border-black/10"
                      style={{ backgroundColor: tag.color }}
                      aria-hidden="true"
                    />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-text dark:text-dark-text truncate">{tag.name}</p>
                      {tag.fileCount > 0 && (
                        <p className="text-xs text-gray-400 dark:text-gray-500">
                          {tag.fileCount} {tag.fileCount === 1 ? 'file' : 'files'}
                        </p>
                      )}
                    </div>
                    <button
                      onClick={() => openEdit(tag)}
                      aria-label={`Rename ${tag.name}`}
                      className="text-primary text-sm font-medium px-2 min-h-[44px]"
                    >
                      Rename
                    </button>
                    <button
                      onClick={() => setConfirmDelete(tag)}
                      data-testid="tag-delete"
                      aria-label={`Delete ${tag.name}`}
                      className="text-red-500 text-sm font-medium px-2 min-h-[44px]"
                    >
                      Delete
                    </button>
                  </div>
                </SwipeableListItem>
              ))}
            </div>
          )}
        </ContentCrossfade>
      )}

      {/* Rename / recolor modal */}
      {editing && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4" onClick={() => setEditing(null)}>
          <form
            onSubmit={handleSaveEdit}
            className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4 flex flex-col gap-3"
            onClick={(e) => e.stopPropagation()}
            data-testid="tag-edit"
          >
            <p className="font-semibold text-text dark:text-dark-text">Edit tag</p>
            <input
              type="text"
              value={editName}
              onChange={(e) => setEditName(e.target.value)}
              placeholder="Tag name"
              data-testid="tag-edit-name"
              className="rounded-lg border border-gray-300 dark:border-dark-border bg-transparent px-3 py-2 text-sm text-text dark:text-dark-text"
            />
            <ColorSwatches value={editColor} onChange={setEditColor} />
            <div className="mt-2 flex gap-3">
              <button type="button" onClick={() => setEditing(null)} className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]">Cancel</button>
              <button
                type="submit"
                disabled={savingEdit || !editName.trim()}
                data-testid="tag-edit-save"
                className="flex-1 rounded-full bg-primary text-white py-2 font-medium min-h-[44px] disabled:opacity-50"
              >
                Save
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Delete confirm */}
      {confirmDelete && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4" onClick={() => setConfirmDelete(null)}>
          <div className="w-full max-w-sm rounded-2xl bg-white dark:bg-neutral-800 p-4" onClick={(e) => e.stopPropagation()} data-testid="tag-delete-confirm">
            <p className="font-semibold text-text dark:text-dark-text">Delete tag?</p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              This permanently removes the tag "{confirmDelete.name}" from this library. This cannot be undone.
            </p>
            <div className="mt-4 flex gap-3">
              <button onClick={() => setConfirmDelete(null)} className="flex-1 rounded-full border border-gray-300 dark:border-dark-border py-2 font-medium min-h-[44px]">Cancel</button>
              <button onClick={handleDelete} data-testid="tag-delete-confirm-yes" className="flex-1 rounded-full bg-red-500 text-white py-2 font-medium min-h-[44px]">Delete</button>
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
