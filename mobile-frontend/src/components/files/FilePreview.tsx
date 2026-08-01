import React, { useState, useEffect } from 'react';
import { getFileDownloadLink } from '../../lib/api';
import { getViewerType, isImageFile } from '../../lib/utils';
import type { Dirent } from '../../lib/models';
import ImageViewer from './ImageViewer';
import VideoPlayer from './VideoPlayer';
import AudioPlayer from './AudioPlayer';
import TextViewer from './TextViewer';
import MarkdownViewer from './MarkdownViewer';
import CodeViewer from './CodeViewer';
import PDFViewer from './PDFViewer';
import OnlyOfficeViewer from './OnlyOfficeViewer';
import GenericFileView from './GenericFileView';

interface FilePreviewProps {
  repoId: string;
  path: string;
  file: Dirent;
  onClose: () => void;
  onToast?: (msg: string) => void;
  siblingFiles?: Dirent[];
  onNavigateToFile?: (file: Dirent) => void;
}

export default function FilePreview({
  repoId,
  path,
  file,
  onClose,
  onToast,
  siblingFiles = [],
  onNavigateToFile,
}: FilePreviewProps) {
  const [downloadUrl, setDownloadUrl] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fullPath = path === '/' ? `/${file.name}` : `${path}/${file.name}`;

  useEffect(() => {
    setLoading(true);
    setError('');
    getFileDownloadLink(repoId, fullPath)
      .then(url => {
        setDownloadUrl(url);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message);
        setLoading(false);
      });
  }, [repoId, fullPath]);

  if (loading) {
    return (
      <div className="fixed inset-0 z-50 bg-black/40 flex items-center justify-center">
        <div className="bg-white rounded-lg p-6 text-center">
          <p className="text-gray-500">Loading preview...</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="fixed inset-0 z-50 bg-black/40 flex items-center justify-center" onClick={onClose}>
        <div className="bg-white rounded-lg p-6 text-center" onClick={e => e.stopPropagation()}>
          <p className="text-red-500 mb-4">{error}</p>
          <button onClick={onClose} className="text-primary font-medium">Close</button>
        </div>
      </div>
    );
  }

  const viewerType = getViewerType(file.name);

  // Build sibling image list for image navigation
  const siblingImages = siblingFiles
    .filter(f => f.type === 'file' && isImageFile(f.name))
    .map(f => ({ name: f.name, url: '' })); // URLs loaded on navigate

  const currentImageIndex = siblingImages.findIndex(img => img.name === file.name);

  const handleImageNavigate = (index: number) => {
    const target = siblingFiles.find(f => f.name === siblingImages[index].name);
    if (target && onNavigateToFile) {
      onNavigateToFile(target);
    }
  };

  switch (viewerType) {
    case 'image':
      return (
        <ImageViewer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
          siblingImages={siblingImages}
          currentIndex={currentImageIndex >= 0 ? currentImageIndex : 0}
          onNavigate={handleImageNavigate}
          onToast={onToast}
        />
      );
    case 'video':
      return (
        <VideoPlayer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
        />
      );
    case 'audio':
      return (
        <AudioPlayer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
        />
      );
    case 'markdown':
      return (
        <MarkdownViewer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
          onToast={onToast}
        />
      );
    case 'text':
      return (
        <TextViewer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
          onToast={onToast}
        />
      );
    case 'code':
      return (
        <CodeViewer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
          onToast={onToast}
        />
      );
    case 'pdf':
      return (
        <PDFViewer
          url={downloadUrl}
          fileName={file.name}
          onClose={onClose}
        />
      );
    case 'office':
      return (
        <OnlyOfficeViewer
          repoId={repoId}
          filePath={fullPath}
          fileName={file.name}
          onClose={onClose}
          onToast={onToast}
        />
      );
    default:
      return (
        <GenericFileView
          url={downloadUrl}
          fileName={file.name}
          fileSize={file.size}
          onClose={onClose}
          onToast={onToast}
        />
      );
  }
}
