import React, { useState, useEffect, useCallback } from 'react';
import { ChevronRight, Folder } from 'lucide-react';
import BottomSheet from '../ui/BottomSheet';
import { listRepos, listDir, moveFile, copyFile, moveDir, copyDir } from '../../lib/api';
import type { Dirent, Repo } from '../../lib/models';

type PickerMode = 'move' | 'copy';

interface FolderPickerProps {
  isOpen: boolean;
  onClose: () => void;
  items: Dirent[];
  srcRepoId: string;
  srcPath: string;
  mode: PickerMode;
  onSuccess: () => void;
}

interface BreadcrumbItem {
  label: string;
  repoId: string;
  path: string;
}

export default function FolderPicker({ isOpen, onClose, items, srcRepoId, srcPath, mode, onSuccess }: FolderPickerProps) {
  const [repos, setRepos] = useState<Repo[]>([]);
  const [dirs, setDirs] = useState<Dirent[]>([]);
  const [selectedRepoId, setSelectedRepoId] = useState<string | null>(null);
  const [currentPath, setCurrentPath] = useState('/');
  const [breadcrumbs, setBreadcrumbs] = useState<BreadcrumbItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [error, setError] = useState('');

  const loadRepos = useCallback(async () => {
    setLoading(true);
    try {
      const data = await listRepos();
      setRepos(data);
    } catch {
      setError('Failed to load libraries');
    } finally {
      setLoading(false);
    }
  }, []);

  const loadDir = useCallback(async (repoId: string, path: string) => {
    setLoading(true);
    try {
      const data = await listDir(repoId, path);
      setDirs(data.filter(d => d.type === 'dir'));
    } catch {
      setError('Failed to load directory');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isOpen) {
      setSelectedRepoId(null);
      setCurrentPath('/');
      setBreadcrumbs([]);
      setError('');
      loadRepos();
    }
  }, [isOpen, loadRepos]);

  const navigateToRepo = (repo: Repo) => {
    setSelectedRepoId(repo.repo_id);
    setCurrentPath('/');
    setBreadcrumbs([{ label: repo.repo_name, repoId: repo.repo_id, path: '/' }]);
    loadDir(repo.repo_id, '/');
  };

  const navigateToDir = (dir: Dirent) => {
    if (!selectedRepoId) return;
    const newPath = currentPath === '/' ? `/${dir.name}` : `${currentPath}/${dir.name}`;
    setCurrentPath(newPath);
    setBreadcrumbs(prev => [...prev, { label: dir.name, repoId: selectedRepoId, path: newPath }]);
    loadDir(selectedRepoId, newPath);
  };

  const navigateToBreadcrumb = (index: number) => {
    if (index < 0) {
      setSelectedRepoId(null);
      setCurrentPath('/');
      setBreadcrumbs([]);
      return;
    }
    const crumb = breadcrumbs[index];
    setSelectedRepoId(crumb.repoId);
    setCurrentPath(crumb.path);
    setBreadcrumbs(prev => prev.slice(0, index + 1));
    loadDir(crumb.repoId, crumb.path);
  };

  const handleExecute = async () => {
    if (!selectedRepoId) return;
    setExecuting(true);
    setError('');
    try {
      for (const item of items) {
        const fullSrcPath = srcPath === '/' ? `/${item.name}` : `${srcPath}/${item.name}`;
        if (mode === 'move') {
          if (item.type === 'dir') {
            await moveDir(srcRepoId, fullSrcPath, selectedRepoId, currentPath);
          } else {
            await moveFile(srcRepoId, fullSrcPath, selectedRepoId, currentPath);
          }
        } else {
          if (item.type === 'dir') {
            await copyDir(srcRepoId, fullSrcPath, selectedRepoId, currentPath);
          } else {
            await copyFile(srcRepoId, fullSrcPath, selectedRepoId, currentPath);
          }
        }
      }
      onSuccess();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : `${mode === 'move' ? 'Move' : 'Copy'} failed`);
    } finally {
      setExecuting(false);
    }
  };

  const actionLabel = mode === 'move' ? 'Move here' : 'Copy here';

  return (
    <BottomSheet isOpen={isOpen} onClose={onClose} title={`Select destination`} fullScreen>
      <div className="flex flex-col h-full">
        {/* Breadcrumbs */}
        <div className="flex items-center gap-1 px-4 py-2 border-b border-gray-100 overflow-x-auto text-sm">
          <button
            onClick={() => navigateToBreadcrumb(-1)}
            className="text-primary whitespace-nowrap min-h-[32px]"
          >
            Libraries
          </button>
          {breadcrumbs.map((crumb, i) => (
            <React.Fragment key={i}>
              <ChevronRight className="w-4 h-4 text-gray-400 flex-shrink-0" />
              <button
                onClick={() => navigateToBreadcrumb(i)}
                className={`whitespace-nowrap min-h-[32px] ${i === breadcrumbs.length - 1 ? 'text-text font-medium' : 'text-primary'}`}
              >
                {crumb.label}
              </button>
            </React.Fragment>
          ))}
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto">
          {loading && <p className="text-center text-gray-500 py-8">Loading...</p>}
          {error && <p className="text-center text-red-500 py-4">{error}</p>}

          {!loading && !selectedRepoId && repos.map(repo => (
            <button
              key={repo.repo_id}
              onClick={() => navigateToRepo(repo)}
              className="flex items-center gap-3 w-full px-4 py-3 min-h-[44px] hover:bg-gray-50 text-left"
              data-testid="folder-picker-repo"
              data-repo-id={repo.repo_id}
            >
              <Folder className="w-5 h-5 text-primary flex-shrink-0" />
              <span className="text-base text-text truncate">{repo.repo_name}</span>
            </button>
          ))}

          {!loading && selectedRepoId && dirs.length === 0 && (
            <p className="text-center text-gray-500 py-8">No subfolders</p>
          )}

          {!loading && selectedRepoId && dirs.map(dir => (
            <button
              key={dir.name}
              onClick={() => navigateToDir(dir)}
              className="flex items-center gap-3 w-full px-4 py-3 min-h-[44px] hover:bg-gray-50 text-left"
              data-testid="folder-picker-dir"
              data-name={dir.name}
            >
              <Folder className="w-5 h-5 text-primary flex-shrink-0" />
              <span className="text-base text-text truncate">{dir.name}</span>
              <ChevronRight className="w-4 h-4 text-gray-400 ml-auto flex-shrink-0" />
            </button>
          ))}
        </div>

        {/* Action button */}
        {selectedRepoId && (
          <div className="p-4 border-t border-gray-200">
            <button
              onClick={handleExecute}
              disabled={executing}
              className="w-full bg-primary-button text-white rounded-lg py-3 min-h-[44px] font-medium disabled:opacity-50"
              data-testid="folder-picker-confirm"
            >
              {executing ? 'Processing...' : actionLabel}
            </button>
          </div>
        )}
      </div>
    </BottomSheet>
  );
}
