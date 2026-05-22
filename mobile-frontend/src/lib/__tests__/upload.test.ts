import { describe, it, expect, vi, beforeEach } from 'vitest';

// Must mock before importing upload module
vi.mock('../config', () => ({
  serviceURL: () => 'http://localhost:8080',
}));

vi.mock('../api', () => ({
  getAuthToken: () => 'test-token',
}));

// Import after mocks
import { uploadManager, type UploadEvent } from '../upload';

function createMockFile(name: string, size = 1024): File {
  const content = new ArrayBuffer(size);
  return new File([content], name, { type: 'application/octet-stream' });
}

async function flushUploadWork() {
  await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
}

describe('UploadManager', () => {
  beforeEach(() => {
    // Clear the queue
    uploadManager.cancelAll();
    uploadManager.clearCompleted();
  });

  it('starts with an empty queue', () => {
    expect(uploadManager.getQueue()).toEqual([]);
  });

  it('adds files to the queue', () => {
    // Mock fetch so upload link request doesn't fail
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    }));

    const file = createMockFile('test.txt');
    const result = uploadManager.addFiles([file], 'repo-1', '/');

    expect(result).toHaveLength(1);
    expect(result[0].file.name).toBe('test.txt');
    expect(result[0].repoId).toBe('repo-1');
    expect(result[0].parentDir).toBe('/');

    vi.restoreAllMocks();
  });

  it('generates unique IDs for each upload', () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    }));

    const file1 = createMockFile('a.txt');
    const file2 = createMockFile('b.txt');
    const result = uploadManager.addFiles([file1, file2], 'repo-1', '/');

    expect(result[0].id).not.toBe(result[1].id);

    vi.restoreAllMocks();
  });

  it('emits queue-changed when files are added', () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    }));

    const events: UploadEvent[] = [];
    const unsub = uploadManager.subscribe((e) => events.push(e));

    const file = createMockFile('test.txt');
    uploadManager.addFiles([file], 'repo-1', '/');

    expect(events.some(e => e.type === 'queue-changed')).toBe(true);

    unsub();
    vi.restoreAllMocks();
  });

  it('cancels a specific file', () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {
      // never resolves - simulates pending upload
    })));

    const file = createMockFile('test.txt');
    const [upload] = uploadManager.addFiles([file], 'repo-1', '/');

    uploadManager.cancelFile(upload.id);

    const queue = uploadManager.getQueue();
    const cancelled = queue.find(f => f.id === upload.id);
    expect(cancelled?.status).toBe('cancelled');

    vi.restoreAllMocks();
  });

  it('cancels all uploads', () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})));

    const files = [createMockFile('a.txt'), createMockFile('b.txt')];
    uploadManager.addFiles(files, 'repo-1', '/');

    uploadManager.cancelAll();

    const queue = uploadManager.getQueue();
    expect(queue.every(f => f.status === 'cancelled')).toBe(true);

    vi.restoreAllMocks();
  });

  it('clears completed/cancelled/failed items from queue', () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})));

    const file = createMockFile('test.txt');
    uploadManager.addFiles([file], 'repo-1', '/');
    uploadManager.cancelAll();

    uploadManager.clearCompleted();

    expect(uploadManager.getQueue()).toEqual([]);

    vi.restoreAllMocks();
  });

  it('unsubscribe stops receiving events', () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    }));

    const events: UploadEvent[] = [];
    const unsub = uploadManager.subscribe((e) => events.push(e));
    unsub();

    const file = createMockFile('test.txt');
    uploadManager.addFiles([file], 'repo-1', '/');

    expect(events).toEqual([]);

    vi.restoreAllMocks();
  });

  it('uses webkitRelativePath when available', () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    }));

    const file = createMockFile('test.txt');
    Object.defineProperty(file, 'webkitRelativePath', { value: 'folder/test.txt' });
    const [upload] = uploadManager.addFiles([file], 'repo-1', '/');

    expect(upload.relativePath).toBe('folder/test.txt');

    vi.restoreAllMocks();
  });

  it('retries a 409 upload conflict and completes on the next attempt', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve('http://localhost:8080/upload'),
    });
    vi.stubGlobal('fetch', fetchMock);

    const responses = [409, 200];

    class MockXMLHttpRequest {
      upload = { addEventListener: vi.fn() };
      status = 0;
      private listeners: Record<string, Array<() => void>> = {};

      open = vi.fn();
      setRequestHeader = vi.fn();

      addEventListener = (type: string, listener: () => void) => {
        this.listeners[type] = this.listeners[type] || [];
        this.listeners[type].push(listener);
      };

      send = vi.fn(() => {
        this.status = responses.shift() ?? 200;
        queueMicrotask(() => {
          for (const listener of this.listeners.load || []) {
            listener();
          }
        });
      });

      abort = vi.fn(() => {
        for (const listener of this.listeners.abort || []) {
          listener();
        }
      });
    }

    vi.stubGlobal('XMLHttpRequest', MockXMLHttpRequest as unknown as typeof XMLHttpRequest);

    const events: UploadEvent[] = [];
    const unsub = uploadManager.subscribe((event) => events.push(event));

    const [upload] = uploadManager.addFiles([createMockFile('conflict.txt')], 'repo-1', '/');
    await flushUploadWork();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(uploadManager.getQueue().find(file => file.id === upload.id)?.status).toBe('completed');
    expect(events.some(event => event.type === 'completed' && event.fileId === upload.id)).toBe(true);
    expect(events.some(event => event.type === 'failed' && event.fileId === upload.id)).toBe(false);

    unsub();
    vi.restoreAllMocks();
  });
});
