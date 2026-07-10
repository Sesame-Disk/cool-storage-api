import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import FilePreview from '../FilePreview';
import type { Dirent } from '../../../lib/models';

import * as api from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  getFileDownloadLink: vi.fn().mockResolvedValue('https://example.com/download/file'),
}));

// Mock all viewer components to simplify testing
vi.mock('../ImageViewer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="image-viewer">{fileName}</div>,
}));
vi.mock('../VideoPlayer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="video-player">{fileName}</div>,
}));
vi.mock('../AudioPlayer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="audio-player">{fileName}</div>,
}));
vi.mock('../TextViewer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="text-viewer">{fileName}</div>,
}));
vi.mock('../MarkdownViewer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="markdown-viewer">{fileName}</div>,
}));
vi.mock('../CodeViewer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="code-viewer">{fileName}</div>,
}));
vi.mock('../PDFViewer', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="pdf-viewer">{fileName}</div>,
}));
vi.mock('../GenericFileView', () => ({
  default: ({ fileName }: { fileName: string }) => <div data-testid="generic-file-view">{fileName}</div>,
}));

const makeDirent = (name: string): Dirent => ({
  id: `id-${name}`,
  type: 'file',
  name,
  size: 1024,
  mtime: 1700000000,
  permission: 'rw',
});

const defaultProps = {
  repoId: 'repo-1',
  path: '/',
  onClose: vi.fn(),
  onToast: vi.fn(),
};

describe('FilePreview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('selects ImageViewer for .jpg files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('photo.jpg')} />);
    await waitFor(() => {
      expect(screen.getByTestId('image-viewer')).toBeInTheDocument();
    });
  });

  it('selects ImageViewer for .png files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('screenshot.png')} />);
    await waitFor(() => {
      expect(screen.getByTestId('image-viewer')).toBeInTheDocument();
    });
  });

  it('selects VideoPlayer for .mp4 files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('video.mp4')} />);
    await waitFor(() => {
      expect(screen.getByTestId('video-player')).toBeInTheDocument();
    });
  });

  it('selects TextViewer for .txt files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('readme.txt')} />);
    await waitFor(() => {
      expect(screen.getByTestId('text-viewer')).toBeInTheDocument();
    });
  });

  it('selects MarkdownViewer for .md files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('README.md')} />);
    await waitFor(() => {
      expect(screen.getByTestId('markdown-viewer')).toBeInTheDocument();
    });
  });

  it('selects MarkdownViewer for .markdown files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('NOTES.markdown')} />);
    await waitFor(() => {
      expect(screen.getByTestId('markdown-viewer')).toBeInTheDocument();
    });
  });

  it('selects AudioPlayer for .mp3 files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('song.mp3')} />);
    await waitFor(() => {
      expect(screen.getByTestId('audio-player')).toBeInTheDocument();
    });
  });

  it('selects AudioPlayer for .flac files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('track.flac')} />);
    await waitFor(() => {
      expect(screen.getByTestId('audio-player')).toBeInTheDocument();
    });
  });

  it('selects CodeViewer for .py files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('main.py')} />);
    await waitFor(() => {
      expect(screen.getByTestId('code-viewer')).toBeInTheDocument();
    });
  });

  it('selects PDFViewer for .pdf files', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('document.pdf')} />);
    await waitFor(() => {
      expect(screen.getByTestId('pdf-viewer')).toBeInTheDocument();
    });
  });

  it('selects GenericFileView for unknown file types', async () => {
    render(<FilePreview {...defaultProps} file={makeDirent('archive.unknown')} />);
    await waitFor(() => {
      expect(screen.getByTestId('generic-file-view')).toBeInTheDocument();
    });
  });

  it('shows loading state initially', () => {
    // Use a never-resolving promise to keep loading state
    vi.mocked(api.getFileDownloadLink).mockReturnValue(new Promise(() => {}));
    render(<FilePreview {...defaultProps} file={makeDirent('test.txt')} />);
    expect(screen.getByText('Loading preview...')).toBeInTheDocument();
  });
});
