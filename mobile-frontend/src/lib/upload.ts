import { serviceURL } from './config';
import { getAuthToken, invalidateApiCache } from './api';

// Content-addressed (block) upload block size. Must match the server's frozen
// CAS block size (WebUploadBlockSize = 8 MiB): every non-final block MUST be
// exactly this many bytes or the commit's manifest validation rejects it.
export const BLOCK_SIZE = 8 * 1024 * 1024; // 8 MiB

// Files at/above this size use the block-upload protocol (session → check →
// upload blocks → commit). Smaller files keep the single-request path. Kept
// intentionally low (1 MiB) so large uploads dedup/resume via the block flow
// while typical small files stay on the cheap single POST.
export const BLOCK_UPLOAD_THRESHOLD = 1 * 1024 * 1024; // 1 MiB

export interface UploadFile {
  id: string;
  file: File;
  repoId: string;
  parentDir: string;
  relativePath: string;
  progress: number;
  status: 'queued' | 'uploading' | 'completed' | 'failed' | 'cancelled';
  error?: string;
  xhr?: XMLHttpRequest;
}

export type UploadEventType = 'progress' | 'completed' | 'failed' | 'cancelled' | 'queue-changed';

export interface UploadEvent {
  type: UploadEventType;
  fileId: string;
  progress?: number;
  error?: string;
}

type UploadListener = (event: UploadEvent) => void;

let idCounter = 0;

function generateId(): string {
  return `upload-${Date.now()}-${++idCounter}`;
}

/** Lowercase hex SHA-256 of the given bytes (the block hash the server verifies).
 * Exported so the folder-sync engine hashes with the exact same algorithm. */
export async function sha256Hex(data: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(digest))
    .map(b => b.toString(16).padStart(2, '0'))
    .join('');
}

class UploadManager {
  private queue: UploadFile[] = [];
  private maxConcurrent = 3;
  private maxRetries = 2;
  private listeners: UploadListener[] = [];
  private retryCounts = new Map<string, number>();

  getQueue(): UploadFile[] {
    return [...this.queue];
  }

  getActiveCount(): number {
    return this.queue.filter(f => f.status === 'uploading').length;
  }

  subscribe(listener: UploadListener): () => void {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter(l => l !== listener);
    };
  }

  private emit(event: UploadEvent) {
    for (const listener of this.listeners) {
      listener(event);
    }
  }

  addFiles(files: File[], repoId: string, parentDir: string): UploadFile[] {
    const uploadFiles: UploadFile[] = files.map(file => ({
      id: generateId(),
      file,
      repoId,
      parentDir,
      relativePath: (file as any).webkitRelativePath || file.name,
      progress: 0,
      status: 'queued' as const,
    }));

    this.queue.push(...uploadFiles);
    this.emit({ type: 'queue-changed', fileId: '' });
    this.processQueue();
    return uploadFiles;
  }

  cancelFile(fileId: string) {
    const file = this.queue.find(f => f.id === fileId);
    if (!file) return;

    if (file.status === 'uploading' && file.xhr) {
      file.xhr.abort();
    }
    file.status = 'cancelled';
    file.progress = 0;
    this.emit({ type: 'cancelled', fileId });
    this.emit({ type: 'queue-changed', fileId: '' });
    this.processQueue();
  }

  cancelAll() {
    for (const file of this.queue) {
      if (file.status === 'uploading' || file.status === 'queued') {
        if (file.xhr) file.xhr.abort();
        file.status = 'cancelled';
        file.progress = 0;
        this.emit({ type: 'cancelled', fileId: file.id });
      }
    }
    this.emit({ type: 'queue-changed', fileId: '' });
  }

  clearCompleted() {
    this.queue = this.queue.filter(f =>
      f.status !== 'completed' && f.status !== 'cancelled' && f.status !== 'failed'
    );
    this.emit({ type: 'queue-changed', fileId: '' });
  }

  private processQueue() {
    const active = this.getActiveCount();
    const queued = this.queue.filter(f => f.status === 'queued');

    const slotsAvailable = this.maxConcurrent - active;
    const toStart = queued.slice(0, slotsAvailable);

    for (const file of toStart) {
      this.uploadFile(file);
    }
  }

  private async uploadFile(uploadFile: UploadFile) {
    uploadFile.status = 'uploading';
    this.emit({ type: 'queue-changed', fileId: uploadFile.id });

    try {
      if (uploadFile.file.size >= BLOCK_UPLOAD_THRESHOLD) {
        // Large file: content-addressed (block) upload with dedup + resume.
        await this.performBlockUpload(uploadFile);
      } else {
        // Small file: single-request upload via a fresh upload link.
        const uploadLink = await this.getUploadLink(uploadFile.repoId, uploadFile.parentDir);
        await this.performUpload(uploadFile, uploadLink);
      }

      // Refresh the directory listing (the service worker caches GET
      // /api2/repos/<id>/dir/ and would otherwise serve a stale listing that
      // omits the just-uploaded file). Matches create-folder/rename/delete.
      await invalidateApiCache(`/api2/repos/${uploadFile.repoId}/dir`);

      uploadFile.status = 'completed';
      uploadFile.progress = 100;
      this.emit({ type: 'completed', fileId: uploadFile.id });
    } catch (err) {
      if ((uploadFile.status as string) === 'cancelled') return;

      const retries = this.retryCounts.get(uploadFile.id) || 0;
      if (retries < this.maxRetries) {
        this.retryCounts.set(uploadFile.id, retries + 1);
        uploadFile.status = 'queued';
        uploadFile.progress = 0;
        this.emit({ type: 'queue-changed', fileId: uploadFile.id });
      } else {
        uploadFile.status = 'failed';
        uploadFile.error = err instanceof Error ? err.message : 'Upload failed';
        this.emit({ type: 'failed', fileId: uploadFile.id, error: uploadFile.error });
      }
    } finally {
      this.emit({ type: 'queue-changed', fileId: '' });
      this.processQueue();
    }
  }

  private async getUploadLink(repoId: string, parentDir: string): Promise<string> {
    const token = getAuthToken();
    const params = new URLSearchParams({ p: parentDir });
    const res = await fetch(`${serviceURL()}/api2/repos/${repoId}/upload-link/?${params}`, {
      headers: {
        'Authorization': `Token ${token}`,
        'Accept': 'application/json',
      },
    });
    if (!res.ok) throw new Error('Failed to get upload link');
    const url = await res.json();
    return url as string;
  }

  // ---------------------------------------------------------------------------
  // Block (content-addressed) upload — mirrors the web flow:
  //   1. POST /api/v2/repos/:id/block-upload-session/  → session_id, block_size
  //   2. split file into fixed-size blocks, SHA-256 each
  //   3. POST /api/v2/blocks/check                     → which blocks are missing
  //   4. POST /api/v2/blocks/upload (per missing block, raw bytes)
  //   5. POST /api/v2/repos/:id/file-from-blocks/      → commit the manifest
  // ---------------------------------------------------------------------------
  private async performBlockUpload(uploadFile: UploadFile): Promise<void> {
    const { repoId, parentDir, file } = uploadFile;
    const filename = uploadFile.relativePath.split('/').pop() || file.name;

    // Step 1: mint a session (declare the size so the server can fail fast).
    const session = await this.createBlockUploadSession(repoId, parentDir, file.size);
    const blockSize = session.block_size > 0 ? session.block_size : BLOCK_SIZE;

    // Step 2: split into blocks and hash each (ordered manifest).
    // Files that reach this path are always non-empty (>= BLOCK_UPLOAD_THRESHOLD);
    // the server rejects 0-block/empty manifests by design.
    const blocks: { sha256: string; size: number; data: ArrayBuffer }[] = [];
    for (let offset = 0; offset < file.size; offset += blockSize) {
      const slice = file.slice(offset, Math.min(offset + blockSize, file.size));
      const data = await slice.arrayBuffer();
      const sha256 = await sha256Hex(data);
      blocks.push({ sha256, size: data.byteLength, data });
    }

    // Step 3: ask which blocks the server still needs (dedup/resume).
    const uniqueHashes = Array.from(new Set(blocks.map(b => b.sha256)));
    const missing = new Set(await this.checkBlocks(uniqueHashes, session.session_id));

    // Step 4: upload each missing block once, updating progress as we go.
    const uploaded = new Set<string>();
    let bytesDone = 0;
    const totalBytes = file.size || 1;
    for (const block of blocks) {
      if (missing.has(block.sha256) && !uploaded.has(block.sha256)) {
        await this.uploadBlock(session.session_id, block.sha256, block.data);
        uploaded.add(block.sha256);
      }
      bytesDone += block.size;
      uploadFile.progress = Math.min(99, Math.round((bytesDone / totalBytes) * 100));
      this.emit({ type: 'progress', fileId: uploadFile.id, progress: uploadFile.progress });
    }

    // Step 5: commit the file from the ordered block manifest.
    await this.createFileFromBlocks(repoId, {
      session: session.session_id,
      parent_dir: parentDir,
      filename,
      replace: false,
      size: file.size,
      blocks: blocks.map(b => ({ sha256: b.sha256, size: b.size })),
    });
  }

  private async createBlockUploadSession(
    repoId: string,
    parentDir: string,
    size: number,
  ): Promise<{ session_id: string; block_size: number }> {
    const token = getAuthToken();
    const res = await fetch(`${serviceURL()}/api/v2/repos/${repoId}/block-upload-session/`, {
      method: 'POST',
      headers: {
        'Authorization': `Token ${token}`,
        'Content-Type': 'application/json',
        'Accept': 'application/json',
      },
      body: JSON.stringify({ parent_dir: parentDir, size }),
    });
    if (!res.ok) throw new Error(`Failed to create block-upload session (${res.status})`);
    const data = await res.json();
    return { session_id: data.session_id, block_size: data.block_size };
  }

  private async checkBlocks(hashes: string[], session: string): Promise<string[]> {
    const token = getAuthToken();
    const res = await fetch(`${serviceURL()}/api/v2/blocks/check`, {
      method: 'POST',
      headers: {
        'Authorization': `Token ${token}`,
        'Content-Type': 'application/json',
        'Accept': 'application/json',
        'X-Block-Upload-Session': session,
      },
      body: JSON.stringify({ hashes }),
    });
    if (!res.ok) throw new Error(`Failed to check blocks (${res.status})`);
    const data = await res.json();
    return (data.missing ?? []) as string[];
  }

  private async uploadBlock(session: string, hash: string, data: ArrayBuffer): Promise<void> {
    const token = getAuthToken();
    const res = await fetch(`${serviceURL()}/api/v2/blocks/upload`, {
      method: 'POST',
      headers: {
        'Authorization': `Token ${token}`,
        'Content-Type': 'application/octet-stream',
        'X-Block-Hash': hash,
        'X-Block-Upload-Session': session,
      },
      body: data,
    });
    if (!res.ok) throw new Error(`Failed to upload block (${res.status})`);
  }

  private async createFileFromBlocks(
    repoId: string,
    manifest: {
      session: string;
      parent_dir: string;
      filename: string;
      replace: boolean;
      size: number;
      blocks: { sha256: string; size: number }[];
    },
  ): Promise<void> {
    const token = getAuthToken();
    const res = await fetch(`${serviceURL()}/api/v2/repos/${repoId}/file-from-blocks/`, {
      method: 'POST',
      headers: {
        'Authorization': `Token ${token}`,
        'Content-Type': 'application/json',
        'Accept': 'application/json',
      },
      body: JSON.stringify(manifest),
    });
    if (!res.ok) throw new Error(`Failed to commit file from blocks (${res.status})`);
  }

  private performUpload(uploadFile: UploadFile, uploadLink: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      uploadFile.xhr = xhr;

      const formData = new FormData();
      formData.append('file', uploadFile.file, uploadFile.relativePath);
      formData.append('parent_dir', uploadFile.parentDir);
      formData.append('relative_path', '');

      xhr.upload.addEventListener('progress', (e) => {
        if (e.lengthComputable) {
          uploadFile.progress = Math.round((e.loaded / e.total) * 100);
          this.emit({ type: 'progress', fileId: uploadFile.id, progress: uploadFile.progress });
        }
      });

      xhr.addEventListener('load', () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve();
        } else {
          reject(new Error(`Upload failed with status ${xhr.status}`));
        }
      });

      xhr.addEventListener('error', () => reject(new Error('Network error during upload')));
      xhr.addEventListener('abort', () => reject(new Error('Upload aborted')));

      const token = getAuthToken();
      xhr.open('POST', uploadLink);
      if (token) {
        xhr.setRequestHeader('Authorization', `Token ${token}`);
      }
      xhr.send(formData);
    });
  }
}

export const uploadManager = new UploadManager();
