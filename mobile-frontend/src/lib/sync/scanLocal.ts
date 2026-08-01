import type { LocalEntry } from './diff';

// Result of walking the picked local folder. `entries` feeds the diff (dirs +
// files, size/mtime only — no hashing yet); `fileHandles` lets the engine
// re-open a file to hash it (only for diff candidates) or to upload it.
export interface LocalScan {
  entries: Map<string, LocalEntry>;
  fileHandles: Map<string, FileSystemFileHandle>;
}

// FileSystemDirectoryHandle exposes async iteration via `.values()`, but the
// lib DOM types don't always include it — narrow locally.
type DirHandleWithValues = FileSystemDirectoryHandle & {
  values(): AsyncIterableIterator<FileSystemHandle>;
};

/**
 * Recursively walk `root`, producing one LocalEntry per file and folder keyed
 * by POSIX-style relPath (root-relative, no leading slash). Reads each file's
 * size + lastModified via getFile() (cheap — no content read) and stashes the
 * handle so the engine can hash/upload it later.
 */
export async function scanLocal(root: FileSystemDirectoryHandle): Promise<LocalScan> {
  const entries = new Map<string, LocalEntry>();
  const fileHandles = new Map<string, FileSystemFileHandle>();

  async function walk(dir: FileSystemDirectoryHandle, prefix: string): Promise<void> {
    for await (const handle of (dir as DirHandleWithValues).values()) {
      const relPath = prefix ? `${prefix}/${handle.name}` : handle.name;
      if (handle.kind === 'directory') {
        entries.set(relPath, { relPath, isDir: true, size: 0, mtime: 0 });
        await walk(handle as FileSystemDirectoryHandle, relPath);
      } else {
        const fileHandle = handle as FileSystemFileHandle;
        const file = await fileHandle.getFile();
        entries.set(relPath, {
          relPath,
          isDir: false,
          size: file.size,
          mtime: file.lastModified,
        });
        fileHandles.set(relPath, fileHandle);
      }
    }
  }

  await walk(root, '');
  return { entries, fileHandles };
}

// --- Worker-backed hashing -------------------------------------------------

let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, { resolve: (h: string) => void; reject: (e: Error) => void }>();

function getWorker(): Worker {
  if (!worker) {
    worker = new Worker(new URL('./hash.worker.ts', import.meta.url), { type: 'module' });
    worker.onmessage = (e: MessageEvent) => {
      const { id, type, contentHash, message } = e.data || {};
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      if (type === 'done') p.resolve(contentHash);
      else p.reject(new Error(message || 'hash failed'));
    };
  }
  return worker;
}

/** SHA-256 content hash of a file, computed off the main thread. */
export function hashFile(file: File): Promise<string> {
  const id = ++seq;
  return new Promise<string>((resolve, reject) => {
    pending.set(id, { resolve, reject });
    getWorker().postMessage({ id, file });
  });
}
