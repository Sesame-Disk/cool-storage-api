import React, { useState, useEffect, useCallback, useRef } from 'react';
import { FolderOpen, File, Folder, MoreVertical, ChevronRight, Star, Upload as UploadIcon, History as HistoryIcon, Trash2, Tag as TagIcon, Shield } from 'lucide-react';
import { listDir, starFile, unstarFile } from '../../lib/api';
import { bytesToSize, formatDate } from '../../lib/models';
import type { Dirent } from '../../lib/models';
import FileContextMenu from '../files/FileContextMenu';
import RenameDialog from '../files/RenameDialog';
import DeleteConfirmDialog from '../files/DeleteConfirmDialog';
import FolderPicker from '../files/FolderPicker';
import FileDetailsSheet from '../files/FileDetailsSheet';
import MultiSelectBar from '../files/MultiSelectBar';
import FilePreview from '../files/FilePreview';
import UploadButton from '../files/UploadButton';
import UploadProgressSheet from '../files/UploadProgressSheet';
import UploadConflictDialog from '../files/UploadConflictDialog';
import type { ConflictResolution } from '../files/UploadConflictDialog';
import NewFolderDialog from '../files/NewFolderDialog';
import NewFileDialog from '../files/NewFileDialog';
import { uploadManager } from '../../lib/upload';
import type { UploadFile } from '../../lib/upload';
import { getFileDownloadLink } from '../../lib/api';
import { downloadFile } from '../../lib/share';
import ShareSheet from '../share/ShareSheet';
import DecryptDialog from '../libraries/DecryptDialog';
import { isRepoDecrypted } from '../../lib/encryption';
import { cacheDirents, getCachedDirents } from '../../lib/offlineDb';

interface FileBrowserProps {
  repoId?: string;
  repoName?: string;
  encrypted?: boolean;
  initialPath?: string;
}

export default function FileBrowser({ repoId, repoName, encrypted, initialPath = '/' }: FileBrowserProps) {
  const [path, setPath] = useState(initialPath);
  const [items, setItems] = useState<Dirent[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [decryptOpen, setDecryptOpen] = useState(false);
  const [needsDecrypt, setNeedsDecrypt] = useState(false);

  // Context menu
  const [contextItem, setContextItem] = useState<Dirent | null>(null);
  const [contextMenuOpen, setContextMenuOpen] = useState(false);

  // Dialogs
  const [renameOpen, setRenameOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [folderPickerOpen, setFolderPickerOpen] = useState(false);
  const [folderPickerMode, setFolderPickerMode] = useState<'move' | 'copy'>('move');

  // Share sheet
  const [shareOpen, setShareOpen] = useState(false);

  // File preview
  const [previewFile, setPreviewFile] = useState<Dirent | null>(null);

  // Multi-select
  const [multiSelect, setMultiSelect] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const longPressTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Upload
  const [uploadFiles, setUploadFiles] = useState<UploadFile[]>([]);
  const [uploadProgressOpen, setUploadProgressOpen] = useState(false);
  const [conflictOpen, setConflictOpen] = useState(false);
  const [conflictFileName, setConflictFileName] = useState('');
  const [conflictFileCount, setConflictFileCount] = useState(0);
  const [pendingConflictResolve, setPendingConflictResolve] = useState<((resolution: ConflictResolution, applyToAll: boolean) => void) | null>(null);

  // New folder/file dialogs
  const [newFolderOpen, setNewFolderOpen] = useState(false);
  const [newFileOpen, setNewFileOpen] = useState(false);

  // Subscribe to upload manager events
  useEffect(() => {
    const unsubscribe = uploadManager.subscribe((event) => {
      setUploadFiles(uploadManager.getQueue());
      if (event.type === 'completed') {
        loadDirectory();
      }
    });
    return unsubscribe;
  }, []);

  const handleUploadStarted = useCallback(() => {
    setUploadProgressOpen(true);
  }, []);

  const handleCancelUpload = useCallback((fileId: string) => {
    uploadManager.cancelFile(fileId);
  }, []);

  const handleCancelAllUploads = useCallback(() => {
    uploadManager.cancelAll();
  }, []);

  const handleConflictResolve = useCallback((resolution: ConflictResolution, applyToAll: boolean) => {
    setConflictOpen(false);
    pendingConflictResolve?.(resolution, applyToAll);
    setPendingConflictResolve(null);
  }, [pendingConflictResolve]);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 3000);
  };

  const loadDirectory = useCallback(async () => {
    if (!repoId) return;
    if (encrypted && !isRepoDecrypted(repoId)) {
      setNeedsDecrypt(true);
      setDecryptOpen(true);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const data = await listDir(repoId, path);
      setItems(data);
      setNeedsDecrypt(false);
      cacheDirents(repoId, path, data).catch(() => {});
    } catch (err) {
      // Try offline fallback
      try {
        const cached = await getCachedDirents(repoId, path);
        if (cached && cached.length > 0) {
          setItems(cached);
          setNeedsDecrypt(false);
          setLoading(false);
          return;
        }
      } catch {
        // ignore cache errors
      }
      setError(err instanceof Error ? err.message : 'Failed to load directory');
    } finally {
      setLoading(false);
    }
  }, [repoId, path, encrypted]);

  useEffect(() => {
    loadDirectory();
  }, [loadDirectory]);

  const refresh = () => {
    loadDirectory();
    setMultiSelect(false);
    setSelectedIds(new Set());
  };

  // Long press handlers
  const handlePointerDown = (item: Dirent) => {
    longPressTimer.current = setTimeout(() => {
      if (!multiSelect) {
        setMultiSelect(true);
        setSelectedIds(new Set([item.id]));
      }
    }, 500);
  };

  const handlePointerUp = () => {
    if (longPressTimer.current) {
      clearTimeout(longPressTimer.current);
      longPressTimer.current = null;
    }
  };

  const handleItemTap = (item: Dirent) => {
    if (multiSelect) {
      setSelectedIds(prev => {
        const next = new Set(prev);
        if (next.has(item.id)) {
          next.delete(item.id);
        } else {
          next.add(item.id);
        }
        if (next.size === 0) setMultiSelect(false);
        return next;
      });
      return;
    }
    if (item.type === 'dir') {
      setPath(path === '/' ? `/${item.name}` : `${path}/${item.name}`);
    } else {
      setPreviewFile(item);
    }
  };

  const openContextMenu = (item: Dirent, e: React.MouseEvent) => {
    e.stopPropagation();
    setContextItem(item);
    setContextMenuOpen(true);
  };

  // Context menu actions
  const handleStar = async () => {
    if (!contextItem || !repoId) return;
    const fullPath = path === '/' ? `/${contextItem.name}` : `${path}/${contextItem.name}`;
    try {
      if (contextItem.starred) {
        await unstarFile(repoId, fullPath);
        showToast('Unstarred');
      } else {
        await starFile(repoId, fullPath);
        showToast('Starred');
      }
      refresh();
    } catch {
      showToast('Failed to update star');
    }
  };

  const handleShare = () => setShareOpen(true);
  const handleDownload = async () => {
    if (!contextItem || !repoId) return;
    const fullPath = path === '/' ? `/${contextItem.name}` : `${path}/${contextItem.name}`;
    try {
      const url = await getFileDownloadLink(repoId, fullPath);
      downloadFile(url, contextItem.name);
    } catch {
      showToast('Failed to download');
    }
  };

  const handleRename = () => setRenameOpen(true);
  const handleDelete = () => setDeleteOpen(true);
  const handleDetails = () => setDetailsOpen(true);

  const handleCopy = () => {
    setFolderPickerMode('copy');
    setFolderPickerOpen(true);
  };
  const handleMove = () => {
    setFolderPickerMode('move');
    setFolderPickerOpen(true);
  };

  // Multi-select actions
  const selectedItems = items.filter(i => selectedIds.has(i.id));

  const handleMultiDelete = () => {
    setDeleteOpen(true);
  };

  const handleMultiMove = () => {
    setFolderPickerMode('move');
    setFolderPickerOpen(true);
  };

  const handleMultiCopy = () => {
    setFolderPickerMode('copy');
    setFolderPickerOpen(true);
  };

  const handleMultiShare = () => showToast('Multi-item sharing not supported. Select a single item to share.');

  // Breadcrumb navigation
  const pathParts = path === '/' ? [] : path.split('/').filter(Boolean);

  if (!repoId) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <FolderOpen className="w-12 h-12 text-gray-300 mb-4" />
        <h1 className="text-xl font-medium text-text">File Browser</h1>
        <p className="text-gray-500 mt-2">Select a library to browse files</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Breadcrumbs */}
      <div className="flex items-center gap-1 px-4 py-2 border-b border-gray-100 overflow-x-auto text-sm">
        <button onClick={() => setPath('/')} className="text-primary whitespace-nowrap min-h-[32px]">
          Root
        </button>
        {pathParts.map((part, i) => {
          const partPath = '/' + pathParts.slice(0, i + 1).join('/');
          const isLast = i === pathParts.length - 1;
          return (
            <React.Fragment key={partPath}>
              <ChevronRight className="w-4 h-4 text-gray-400 flex-shrink-0" />
              <button
                onClick={() => setPath(partPath)}
                className={`whitespace-nowrap min-h-[32px] ${isLast ? 'text-text font-medium' : 'text-primary'}`}
              >
                {part}
              </button>
            </React.Fragment>
          );
        })}
        {repoId && (
          <div className="ml-auto flex items-center gap-4 pl-3 flex-shrink-0">
            <a
              href={`/libraries/${repoId}/tags`}
              aria-label="Library tags"
              data-testid="library-tags-link"
              className="text-gray-500 min-h-[32px] flex items-center"
            >
              <TagIcon className="w-5 h-5" />
            </a>
            <a
              href={`/libraries/${repoId}/permissions`}
              aria-label="Library custom permissions"
              data-testid="library-permissions-link"
              className="text-gray-500 min-h-[32px] flex items-center"
            >
              <Shield className="w-5 h-5" />
            </a>
            <a
              href={`/libraries/${repoId}/history`}
              aria-label="Library history"
              data-testid="library-history-link"
              className="text-gray-500 min-h-[32px] flex items-center"
            >
              <HistoryIcon className="w-5 h-5" />
            </a>
            <a
              href={`/libraries/${repoId}/trash`}
              aria-label="Library trash"
              data-testid="library-trash-link"
              className="text-gray-500 min-h-[32px] flex items-center"
            >
              <Trash2 className="w-5 h-5" />
            </a>
          </div>
        )}
      </div>

      {/* Loading / Error */}
      {loading && <p className="text-center text-gray-500 py-8">Loading...</p>}
      {error && <p className="text-center text-red-500 py-4">{error}</p>}

      {/* File list */}
      {!loading && items.length === 0 && (
        <div className="flex flex-col items-center justify-center p-8 text-center">
          <FolderOpen className="w-12 h-12 text-gray-300 mb-4" />
          <p className="text-gray-500">This folder is empty</p>
        </div>
      )}

      <div className="flex-1 overflow-auto" data-testid="file-list">
        {items.map(item => {
          const Icon = item.type === 'dir' ? Folder : File;
          const isSelected = selectedIds.has(item.id);
          return (
            <div
              key={item.id}
              data-testid="file-item"
              data-name={item.name}
              className={`flex items-center gap-3 px-4 py-3 min-h-[44px] hover:bg-gray-50 cursor-pointer ${isSelected ? 'bg-primary/10' : ''}`}
              onClick={() => handleItemTap(item)}
              onPointerDown={() => handlePointerDown(item)}
              onPointerUp={handlePointerUp}
              onPointerLeave={handlePointerUp}
            >
              {multiSelect && (
                <input
                  type="checkbox"
                  checked={isSelected}
                  readOnly
                  className="w-5 h-5 flex-shrink-0"
                  aria-label={`Select ${item.name}`}
                />
              )}
              <Icon className="w-5 h-5 text-primary flex-shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-text text-base truncate">{item.name}</p>
                <p className="text-gray-400 text-xs">
                  {item.type === 'file' ? bytesToSize(item.size) : ''}{' '}
                  {formatDate(item.mtime)}
                </p>
              </div>
              {item.starred && <Star className="w-4 h-4 text-yellow-500 fill-yellow-500 flex-shrink-0" />}
              {!multiSelect && (
                <button
                  onClick={(e) => openContextMenu(item, e)}
                  className="min-h-[44px] min-w-[44px] flex items-center justify-center flex-shrink-0"
                  aria-label={`More options for ${item.name}`}
                >
                  <MoreVertical className="w-5 h-5 text-gray-400" />
                </button>
              )}
            </div>
          );
        })}
      </div>

      {/* Toast */}
      {toast && (
        <div className="fixed bottom-20 left-1/2 -translate-x-1/2 bg-gray-800 text-white px-4 py-2 rounded-lg text-sm z-50">
          {toast}
        </div>
      )}

      {/* Multi-select bar */}
      {multiSelect && (
        <MultiSelectBar
          selectedItems={selectedItems}
          totalItems={items.length}
          onSelectAll={() => setSelectedIds(new Set(items.map(i => i.id)))}
          onDeselectAll={() => { setSelectedIds(new Set()); setMultiSelect(false); }}
          onShare={handleMultiShare}
          onMove={handleMultiMove}
          onCopy={handleMultiCopy}
          onDelete={handleMultiDelete}
        />
      )}

      {/* Context menu */}
      <FileContextMenu
        isOpen={contextMenuOpen}
        onClose={() => setContextMenuOpen(false)}
        dirent={contextItem}
        repoId={repoId}
        path={path}
        onStar={handleStar}
        onShare={handleShare}
        onRename={handleRename}
        onCopy={handleCopy}
        onMove={handleMove}
        onDownload={handleDownload}
        onDetails={handleDetails}
        onDelete={handleDelete}
      />

      {/* Rename dialog */}
      <RenameDialog
        isOpen={renameOpen}
        onClose={() => setRenameOpen(false)}
        dirent={contextItem}
        repoId={repoId}
        path={path}
        onSuccess={() => { refresh(); showToast('Renamed successfully'); }}
      />

      {/* Delete confirmation */}
      <DeleteConfirmDialog
        isOpen={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        items={multiSelect ? selectedItems : (contextItem ? [contextItem] : [])}
        repoId={repoId}
        path={path}
        onSuccess={() => { refresh(); showToast('Deleted successfully'); }}
      />

      {/* Folder picker */}
      <FolderPicker
        isOpen={folderPickerOpen}
        onClose={() => setFolderPickerOpen(false)}
        items={multiSelect ? selectedItems : (contextItem ? [contextItem] : [])}
        srcRepoId={repoId}
        srcPath={path}
        mode={folderPickerMode}
        onSuccess={() => { refresh(); showToast(`${folderPickerMode === 'move' ? 'Moved' : 'Copied'} successfully`); }}
      />

      {/* File details */}
      <FileDetailsSheet
        isOpen={detailsOpen}
        onClose={() => setDetailsOpen(false)}
        dirent={contextItem}
        path={path}
      />

      {/* Share sheet */}
      {contextItem && (
        <ShareSheet
          isOpen={shareOpen}
          onClose={() => setShareOpen(false)}
          repoId={repoId}
          path={path}
          isDir={contextItem.type === 'dir'}
          itemName={contextItem.name}
          onToast={showToast}
        />
      )}

      {/* File preview */}
      {previewFile && (
        <FilePreview
          repoId={repoId}
          path={path}
          file={previewFile}
          onClose={() => setPreviewFile(null)}
          onToast={showToast}
          siblingFiles={items.filter(i => i.type === 'file')}
          onNavigateToFile={(file) => setPreviewFile(file)}
        />
      )}

      {/* Upload button (FAB) */}
      {!multiSelect && repoId && (
        <UploadButton
          repoId={repoId}
          path={path}
          onUploadStarted={handleUploadStarted}
          onNewFolder={() => setNewFolderOpen(true)}
          onNewFile={() => setNewFileOpen(true)}
        />
      )}

      {/* Upload progress sheet */}
      <UploadProgressSheet
        isOpen={uploadProgressOpen}
        onClose={() => setUploadProgressOpen(false)}
      />

      {/* Upload conflict dialog */}
      <UploadConflictDialog
        isOpen={conflictOpen}
        onClose={() => { setConflictOpen(false); setPendingConflictResolve(null); }}
        fileName={conflictFileName}
        remainingCount={conflictFileCount}
        onResolve={handleConflictResolve}
      />

      {/* New folder dialog */}
      {repoId && (
        <NewFolderDialog
          isOpen={newFolderOpen}
          onClose={() => setNewFolderOpen(false)}
          repoId={repoId}
          path={path}
          onSuccess={(folderName) => { refresh(); showToast(`Folder "${folderName}" created`); }}
        />
      )}

      {/* New file dialog */}
      {repoId && (
        <NewFileDialog
          isOpen={newFileOpen}
          onClose={() => setNewFileOpen(false)}
          repoId={repoId}
          path={path}
          onSuccess={(fileName) => { refresh(); showToast(`File "${fileName}" created`); }}
        />
      )}

      {/* Upload indicator */}
      {uploadFiles.some(f => f.status === 'uploading' || f.status === 'queued') && !uploadProgressOpen && (
        <button
          onClick={() => setUploadProgressOpen(true)}
          className="fixed bottom-36 right-4 z-30 bg-primary text-white rounded-full px-3 py-2 shadow-lg flex items-center gap-2 text-sm"
        >
          <UploadIcon className="w-4 h-4" />
          <span>Uploading...</span>
        </button>
      )}

      {/* Decrypt dialog for encrypted repos */}
      {encrypted && repoId && (
        <DecryptDialog
          isOpen={decryptOpen}
          onClose={() => setDecryptOpen(false)}
          repoId={repoId}
          repoName={repoName || 'Library'}
          onSuccess={() => {
            setNeedsDecrypt(false);
            loadDirectory();
          }}
        />
      )}
    </div>
  );
}
