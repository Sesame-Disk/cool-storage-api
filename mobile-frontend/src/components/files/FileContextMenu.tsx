import React from 'react';
import { Star, Share2, Pencil, Copy, FolderInput, Download, Info, Trash2 } from 'lucide-react';
import BottomSheet from '../ui/BottomSheet';
import type { Dirent } from '../../lib/models';

interface FileContextMenuProps {
  isOpen: boolean;
  onClose: () => void;
  dirent: Dirent | null;
  repoId: string;
  path: string;
  onStar: () => void;
  onShare: () => void;
  onRename: () => void;
  onCopy: () => void;
  onMove: () => void;
  onDownload: () => void;
  onDetails: () => void;
  onDelete: () => void;
}

interface MenuItemProps {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean;
  testId?: string;
}

function MenuItem({ icon, label, onClick, danger, testId }: MenuItemProps) {
  return (
    <button
      onClick={onClick}
      data-testid={testId}
      className={`flex items-center gap-3 w-full px-4 py-3 min-h-[44px] text-left hover:bg-gray-50 ${danger ? 'text-red-500' : 'text-text'}`}
    >
      {icon}
      <span className="text-base">{label}</span>
    </button>
  );
}

export default function FileContextMenu({
  isOpen,
  onClose,
  dirent,
  onStar,
  onShare,
  onRename,
  onCopy,
  onMove,
  onDownload,
  onDetails,
  onDelete,
}: FileContextMenuProps) {
  if (!dirent) return null;

  const handleAction = (action: () => void) => {
    onClose();
    action();
  };

  return (
    <BottomSheet isOpen={isOpen} onClose={onClose} title={dirent.name}>
      <div className="flex flex-col -mx-6 -mb-6">
        <MenuItem
          icon={<Star className="w-5 h-5" />}
          label={dirent.starred ? 'Unstar' : 'Star'}
          onClick={() => handleAction(onStar)}
          testId="context-menu-star"
        />
        <MenuItem
          icon={<Share2 className="w-5 h-5" />}
          label="Share"
          onClick={() => handleAction(onShare)}
          testId="context-menu-share"
        />
        <MenuItem
          icon={<Pencil className="w-5 h-5" />}
          label="Rename"
          onClick={() => handleAction(onRename)}
          testId="context-menu-rename"
        />
        <MenuItem
          icon={<Copy className="w-5 h-5" />}
          label="Copy"
          onClick={() => handleAction(onCopy)}
          testId="context-menu-copy"
        />
        <MenuItem
          icon={<FolderInput className="w-5 h-5" />}
          label="Move"
          onClick={() => handleAction(onMove)}
          testId="context-menu-move"
        />
        <MenuItem
          icon={<Download className="w-5 h-5" />}
          label="Download"
          onClick={() => handleAction(onDownload)}
          testId="context-menu-download"
        />
        <MenuItem
          icon={<Info className="w-5 h-5" />}
          label="Details"
          onClick={() => handleAction(onDetails)}
          testId="context-menu-details"
        />
        <MenuItem
          icon={<Trash2 className="w-5 h-5" />}
          label="Delete"
          onClick={() => handleAction(onDelete)}
          danger
          testId="context-menu-delete"
        />
      </div>
    </BottomSheet>
  );
}
