import React, { useEffect, useRef, useState } from 'react';
import { X, Download } from 'lucide-react';
import { getOnlyOfficeConfig } from '../../lib/api';
import { getFileDownloadLink } from '../../lib/api';
import { downloadFile } from '../../lib/share';

interface OnlyOfficeViewerProps {
  repoId: string;
  /** Full path to the document within the library, e.g. "/docs/report.docx". */
  filePath: string;
  fileName: string;
  onClose: () => void;
  onToast?: (msg: string) => void;
}

// The OnlyOffice document server injects a global `DocsAPI` once api.js loads.
declare global {
  interface Window {
    DocsAPI?: {
      DocEditor: new (id: string, config: Record<string, unknown>) => {
        destroyEditor?: () => void;
      };
    };
  }
}

const EDITOR_ELEMENT_ID = 'onlyoffice-editor-surface';

/** Load the OnlyOffice api.js once; resolve when window.DocsAPI is available. */
function loadDocsApi(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    if (window.DocsAPI) {
      resolve();
      return;
    }
    const existing = document.querySelector<HTMLScriptElement>(
      `script[data-onlyoffice-src="${src}"]`,
    );
    if (existing) {
      existing.addEventListener('load', () => resolve());
      existing.addEventListener('error', () => reject(new Error('Failed to load OnlyOffice')));
      // If it already loaded before this mount, DocsAPI is set above.
      if (window.DocsAPI) resolve();
      return;
    }
    const script = document.createElement('script');
    script.src = src;
    script.async = true;
    script.dataset.onlyofficeSrc = src;
    script.addEventListener('load', () => resolve());
    script.addEventListener('error', () =>
      reject(new Error('Could not reach the OnlyOffice document server')),
    );
    document.body.appendChild(script);
  });
}

/**
 * Embedded OnlyOffice document editor/viewer — brings office documents
 * (.docx/.xlsx/.pptx/…) to the mobile PWA at parity with the web frontend.
 * Fetches the signed editor config from the backend, loads the document
 * server's api.js, and mounts a DocEditor. On any failure it degrades to a
 * download action so the file is never a dead end.
 */
export default function OnlyOfficeViewer({
  repoId,
  filePath,
  fileName,
  onClose,
  onToast,
}: OnlyOfficeViewerProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const editorRef = useRef<{ destroyEditor?: () => void } | null>(null);

  useEffect(() => {
    let cancelled = false;

    (async () => {
      setLoading(true);
      setError('');
      try {
        const { doc, api_js_url } = await getOnlyOfficeConfig(repoId, filePath);
        if (cancelled) return;
        await loadDocsApi(api_js_url);
        if (cancelled) return;
        if (!window.DocsAPI) throw new Error('OnlyOffice failed to initialize');
        // Mount the editor. DocEditor renders into the element by id.
        editorRef.current = new window.DocsAPI.DocEditor(
          EDITOR_ELEMENT_ID,
          doc as unknown as Record<string, unknown>,
        );
        setLoading(false);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Failed to open document');
        setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
      try {
        editorRef.current?.destroyEditor?.();
      } catch {
        // editor may not have fully mounted — nothing to clean up
      }
      editorRef.current = null;
    };
  }, [repoId, filePath]);

  const handleDownload = async () => {
    try {
      const url = await getFileDownloadLink(repoId, filePath);
      downloadFile(url, fileName);
    } catch {
      onToast?.('Could not download file');
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-white flex flex-col" data-testid="onlyoffice-viewer">
      {/* Top bar */}
      <div className="flex items-center justify-between p-2 border-b border-gray-200">
        <button
          onClick={onClose}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-600"
          aria-label="Close"
        >
          <X className="w-6 h-6" />
        </button>
        <p className="text-text text-sm truncate mx-2 flex-1 text-center font-medium">{fileName}</p>
        <button
          onClick={handleDownload}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-600"
          aria-label="Download"
        >
          <Download className="w-5 h-5" />
        </button>
      </div>

      {/* Editor surface (DocsAPI mounts here) */}
      <div className="flex-1 relative">
        <div id={EDITOR_ELEMENT_ID} className="absolute inset-0" data-testid="onlyoffice-editor" />

        {loading && !error && (
          <div
            className="absolute inset-0 flex items-center justify-center bg-white"
            data-testid="onlyoffice-loading"
          >
            <p className="text-gray-500">Opening document…</p>
          </div>
        )}

        {error && (
          <div
            className="absolute inset-0 flex flex-col items-center justify-center bg-white p-8 text-center"
            data-testid="onlyoffice-error"
          >
            <p className="text-red-500 mb-2 font-medium">Can’t open in the editor</p>
            <p className="text-gray-500 text-sm mb-6 break-words max-w-sm">{error}</p>
            <button
              onClick={handleDownload}
              className="bg-primary text-white px-6 py-3 rounded-lg text-base font-medium flex items-center gap-2"
            >
              <Download className="w-5 h-5" />
              Download instead
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
