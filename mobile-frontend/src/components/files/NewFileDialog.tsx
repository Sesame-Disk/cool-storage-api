import React, { useState } from 'react';
import { X } from 'lucide-react';
import { getAuthToken, invalidateApiCache } from '../../lib/api';
import { serviceURL } from '../../lib/config';

interface NewFileDialogProps {
  isOpen: boolean;
  onClose: () => void;
  repoId: string;
  path: string;
  onSuccess: (fileName: string) => void;
}

const FILE_TYPES = [
  { label: 'Markdown', ext: '.md' },
  { label: 'Text', ext: '.txt' },
  // Office documents — the backend seeds a valid template on create, so these
  // open straight into the OnlyOffice editor (parity with the web frontend).
  { label: 'Word', ext: '.docx' },
  { label: 'Excel', ext: '.xlsx' },
  { label: 'PowerPoint', ext: '.pptx' },
] as const;

async function createEmptyFile(repoId: string, path: string): Promise<void> {
  const token = getAuthToken();
  const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/file/?p=${encodeURIComponent(path)}`, {
    method: 'POST',
    headers: {
      'Authorization': `Token ${token}`,
      'Accept': 'application/json',
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: new URLSearchParams({ operation: 'create' }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error_msg || 'Failed to create file');
  }
  await invalidateApiCache(`/api2/repos/${repoId}`);
}

export default function NewFileDialog({ isOpen, onClose, repoId, path, onSuccess }: NewFileDialogProps) {
  const [name, setName] = useState('');
  const [fileType, setFileType] = useState<string>('.md');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;

    const fileName = trimmed.includes('.') ? trimmed : trimmed + fileType;
    setLoading(true);
    setError('');
    try {
      const filePath = path === '/' ? `/${fileName}` : `${path}/${fileName}`;
      await createEmptyFile(repoId, filePath);
      setName('');
      onClose();
      onSuccess(fileName);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create file');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" data-testid="new-file-dialog">
      <div className="fixed inset-0 bg-black/30" onClick={onClose} />
      <div className="relative bg-white rounded-2xl shadow-xl w-full max-w-sm p-6">
        <button
          onClick={onClose}
          className="absolute top-3 right-3 min-h-[44px] min-w-[44px] flex items-center justify-center"
        >
          <X className="w-5 h-5 text-gray-400" />
        </button>

        <h3 className="text-lg font-medium text-text mb-4">New File</h3>

        <form onSubmit={handleSubmit}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="File name"
            className="w-full px-3 py-2 border border-gray-300 rounded-lg text-text focus:outline-none focus:ring-2 focus:ring-primary/50 min-h-[44px]"
            autoFocus
            data-testid="file-name-input"
          />
          <div className="flex flex-wrap gap-2 mt-3">
            {FILE_TYPES.map(ft => (
              <button
                key={ft.ext}
                type="button"
                onClick={() => setFileType(ft.ext)}
                className={`px-3 py-1.5 rounded-lg text-sm font-medium min-h-[36px] ${
                  fileType === ft.ext
                    ? 'bg-primary text-white'
                    : 'bg-gray-100 text-gray-600'
                }`}
                data-testid={`type-${ft.ext}`}
              >
                {ft.label} ({ft.ext})
              </button>
            ))}
          </div>
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
              data-testid="create-file-btn"
            >
              {loading ? 'Creating...' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
