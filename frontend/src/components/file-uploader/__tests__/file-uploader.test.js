import FileUploader from '../file-uploader';
import { seafileAPI } from '../../../utils/seafile-api';
import { shouldUseBlockUpload } from '../block-upload-orchestrator';
import UploadRemindDialog from '../../dialog/upload-remind-dialog';
import toaster from '../../toast';

// Recursively find the first rendered element of a given component type.
const findRenderedByType = (node, type) => {
  if (!node || typeof node !== 'object') {
    return null;
  }
  if (node.type === type) {
    return node;
  }
  const children = node.props && node.props.children;
  const arr = Array.isArray(children) ? children : [children];
  for (let i = 0; i < arr.length; i += 1) {
    const found = findRenderedByType(arr[i], type);
    if (found) {
      return found;
    }
  }
  return null;
};

jest.mock('../../../utils/seafile-api', () => ({
  seafileAPI: {
    getFileServerUploadLink: jest.fn(),
    getUpdateLink: jest.fn(),
  },
}));

// Keep the real orchestrator but make the block-vs-legacy eligibility check
// controllable so duplicate-decision routing is testable.
jest.mock('../block-upload-orchestrator', () => {
  const actual = jest.requireActual('../block-upload-orchestrator');
  return { ...actual, shouldUseBlockUpload: jest.fn(() => false) };
});

jest.mock('../../../utils/utils', () => ({
  Utils: {
    registerGlobalVariable: jest.fn(),
    getErrorMsg: jest.fn(() => 'error'),
  },
}));

jest.mock('../../toast', () => ({
  danger: jest.fn(),
  success: jest.fn(),
  warning: jest.fn(),
}));

const flushPromises = () => new Promise(resolve => setTimeout(resolve, 0));

beforeEach(() => {
  jest.clearAllMocks();
  shouldUseBlockUpload.mockReturnValue(false);
  window.confirm = jest.fn(() => true);
});

const defaultProps = {
  repoID: 'repo-1',
  direntList: [],
  dragAndDrop: false,
  path: '/',
  onFileUploadSuccess: jest.fn(),
};

const createResumableFile = (name, overrides = {}) => ({
  fileName: name,
  relativePath: name,
  uniqueIdentifier: `${name}-id`,
  formData: {
    parent_dir: '/',
  },
  ...overrides,
});

const createUploader = (props = {}) => {
  const uploader = new FileUploader({ ...defaultProps, ...props });

  uploader.setState = (update, callback) => {
    const nextState = typeof update === 'function' ? update(uploader.state, uploader.props) : update;
    uploader.state = { ...uploader.state, ...nextState };
    if (callback) {
      callback();
    }
  };

  uploader.resumable = {
    files: [],
    opts: {},
    upload: jest.fn(),
    isUploading: jest.fn(() => false),
    removeFile: jest.fn(function (file) {
      this.files = this.files.filter(f => f !== file);
    }),
  };
  // Spy on setUploadFileList while keeping its real behaviour (block-entry merge).
  const realSetUploadFileList = uploader.setUploadFileList;
  uploader.setUploadFileList = jest.fn((...args) => realSetUploadFileList(...args));
  uploader.resumableUpload = jest.fn();
  uploader.retryUploadFile = jest.fn();
  uploader.restoreConcurrencyIfIdle = jest.fn();

  return uploader;
};

describe('FileUploader upload link reuse regression', () => {
  test('fetches the session upload link only once when files are added later', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/token-a' });

    const uploader = createUploader();
    const firstFile = createResumableFile('big.iso');
    const secondFile = createResumableFile('notes.txt');

    uploader.resumable.files = [firstFile];
    uploader.onFileAdded(firstFile, [firstFile]);
    await flushPromises();

    expect(seafileAPI.getFileServerUploadLink).toHaveBeenCalledTimes(1);
    expect(uploader.resumable.opts.target).toBe('/upload/token-a?ret-json=1');
    expect(uploader.resumableUpload).toHaveBeenCalledWith(firstFile);

    uploader.resumable.files = [firstFile, secondFile];
    uploader.onFileAdded(secondFile, [firstFile, secondFile]);
    await flushPromises();

    expect(seafileAPI.getFileServerUploadLink).toHaveBeenCalledTimes(1);
    expect(uploader.resumable.opts.target).toBe('/upload/token-a?ret-json=1');
  });

  test('keeps the session upload link after a 409-triggered global error while uploads are still active', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/token-a' });

    const uploader = createUploader();
    const bigFile = createResumableFile('big.iso');
    const laterFile = createResumableFile('later.txt');
    const conflictMessage = JSON.stringify({ error: 'library was modified concurrently; retry the upload' });

    uploader.resumable.files = [bigFile];
    uploader.onFileAdded(bigFile, [bigFile]);
    await flushPromises();

    uploader.state.uploadFileList = [bigFile];
    uploader.resumable.isUploading.mockReturnValue(true);

    uploader.onFileError(bigFile, conflictMessage);
    uploader.onError('Error', bigFile);

    expect(uploader.retryUploadFile).toHaveBeenCalledWith(bigFile);
    expect(uploader.isUploadLinkLoaded).toBe(true);

    uploader.resumable.files = [bigFile, laterFile];
    uploader.onFileAdded(laterFile, [bigFile, laterFile]);
    await flushPromises();

    expect(seafileAPI.getFileServerUploadLink).toHaveBeenCalledTimes(1);
    expect(uploader.resumable.opts.target).toBe('/upload/token-a?ret-json=1');
  });

  test('clears the session upload link after the queue is actually idle', () => {
    const uploader = createUploader();

    uploader.isUploadLinkLoaded = true;
    uploader.resumable.isUploading.mockReturnValue(false);

    uploader.onError('Error');

    expect(uploader.isUploadLinkLoaded).toBe(false);
  });

  test('replace sets the INSTANCE target to the update-link (resumablejs ignores per-file target)', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/token-a' });

    const uploader = createUploader();
    const resumableFile = createResumableFile('existing.txt', { opts: undefined });

    // Lone replace, queue idle → 'update' mode runs immediately with the update-link as
    // the shared instance target (per-file opts.target is ignored by resumablejs 1.1.16).
    uploader.resumable.files = [];
    uploader.state.currentResumableFile = resumableFile;

    uploader.replaceRepetitionFile();
    await flushPromises();

    expect(uploader.resumable.opts.target).toBe('/update/token-a');
    expect(resumableFile.formData.replace).toBe(1);
    expect(resumableFile.formData.target_file).toBe('/existing.txt');
    expect(uploader.resumable.files).toContain(resumableFile);
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('clears retry files when the upload dialog is closed', () => {
    const uploader = createUploader();
    const failedFile = createResumableFile('failed.txt', { error: 'boom' });

    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [failedFile];
    uploader.state.forbidUploadFileList = [createResumableFile('blocked.txt')];
    uploader.state.retryFileList = [failedFile];
    uploader.state.totalProgress = 100;
    uploader.state.uploadBitrate = 5000;
    uploader.isUploadLinkLoaded = true;

    uploader.onCloseUploadDialog();

    expect(uploader.state.isUploadProgressDialogShow).toBe(false);
    expect(uploader.state.uploadFileList).toEqual([]);
    expect(uploader.state.forbidUploadFileList).toEqual([]);
    expect(uploader.state.retryFileList).toEqual([]);
    expect(uploader.state.totalProgress).toBe(0);
    expect(uploader.state.uploadBitrate).toBe(0);
    expect(uploader.isUploadLinkLoaded).toBe(false);
  });
});


describe('FileUploader navigation guard', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.runOnlyPendingTimers();
    jest.useRealTimers();
  });

  test('blocks link navigation while an upload is still active and the user cancels', () => {
    const uploader = createUploader();
    const activeFile = createResumableFile('big.iso', { isSaved: false, error: null });
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [activeFile];
    window.confirm.mockReturnValue(false);

    const anchor = document.createElement('a');
    anchor.href = '/libraries/next/';
    const target = document.createElement('span');
    anchor.appendChild(target);

    const event = {
      target,
      defaultPrevented: false,
      button: 0,
      metaKey: false,
      ctrlKey: false,
      shiftKey: false,
      altKey: false,
      preventDefault: jest.fn(),
      stopPropagation: jest.fn(),
    };

    uploader.onDocumentNavigationAttempt(event);

    expect(window.confirm).toHaveBeenCalled();
    expect(event.preventDefault).toHaveBeenCalled();
    expect(event.stopPropagation).toHaveBeenCalled();
  });

  test('allows one confirmed navigation attempt without a second beforeunload prompt', () => {
    const uploader = createUploader();
    const activeFile = createResumableFile('big.iso', { isSaved: false, error: null });
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [activeFile];

    expect(uploader.confirmNavigationIfUploading()).toBe(true);
    expect(uploader.onbeforeunload()).toBeUndefined();

    jest.advanceTimersByTime(1000);

    expect(uploader.onbeforeunload()).toBe('');
  });

  test('does not prompt when only failed uploads remain', () => {
    const uploader = createUploader();
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [createResumableFile('failed.iso', { isSaved: false, error: 'boom' })];

    expect(uploader.onbeforeunload()).toBeUndefined();
    expect(uploader.confirmNavigationIfUploading()).toBe(true);
    expect(window.confirm).not.toHaveBeenCalled();
  });

  test('ignores javascript:void(0) pseudo-links and does not prompt', () => {
    const uploader = createUploader();
    const activeFile = createResumableFile('big.iso', { isSaved: false, error: null });
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [activeFile];

    const anchor = document.createElement('a');
    anchor.setAttribute('href', 'javascript:void(0)'); // eslint-disable-line no-script-url
    const target = document.createElement('span');
    anchor.appendChild(target);

    const event = {
      target,
      defaultPrevented: false,
      button: 0,
      metaKey: false,
      ctrlKey: false,
      shiftKey: false,
      altKey: false,
      preventDefault: jest.fn(),
      stopPropagation: jest.fn(),
    };

    uploader.onDocumentNavigationAttempt(event);

    expect(window.confirm).not.toHaveBeenCalled();
    expect(event.preventDefault).not.toHaveBeenCalled();
  });
});

describe('FileUploader block upload integration', () => {
  test('treats active block uploads as uploading work even when resumable is idle', () => {
    const uploader = createUploader();
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [{
      uniqueIdentifier: 'block-1',
      isSaved: false,
      error: null,
      progress: () => 0.25,
      isUploading: () => true,
    }];

    expect(uploader.isUploading()).toBe(true);
    expect(uploader.hasActiveUploadWork()).toBe(true);
  });

  test('retries failed block uploads through the block flow instead of resumable bootstrap', () => {
    const uploader = createUploader();
    const blockEntry = {
      uniqueIdentifier: 'block-1',
      isBlockUpload: true,
      file: { name: 'big.bin', size: 10 },
      isSaved: false,
      error: 'boom',
      progress: () => 0.4,
      isUploading: () => false,
    };
    uploader.state.retryFileList = [blockEntry];
    uploader.state.uploadFileList = [blockEntry];
    uploader.runBlockUpload = jest.fn();

    uploader.onUploadRetry(blockEntry);

    expect(uploader.runBlockUpload).toHaveBeenCalledWith(blockEntry, blockEntry.file);
    expect(uploader.retryUploadFile).not.toHaveBeenCalled();
    expect(uploader.state.retryFileList).toEqual([]);
    expect(blockEntry.error).toBeNull();
  });

  test('keeps total progress below 100 when cancelling one block upload but another is still active', () => {
    const uploader = createUploader();
    const cancelled = {
      uniqueIdentifier: 'block-1',
      isSaved: false,
      error: null,
      progress: () => 0.1,
      isUploading: () => true,
      cancel: jest.fn(),
    };
    const active = {
      uniqueIdentifier: 'block-2',
      isSaved: false,
      error: null,
      progress: () => 0.4,
      isUploading: () => true,
      cancel: jest.fn(),
    };
    uploader.state.uploadFileList = [cancelled, active];

    uploader.onUploadCancel(cancelled);

    expect(cancelled.cancel).toHaveBeenCalled();
    expect(uploader.state.uploadFileList).toEqual([active]);
    expect(uploader.state.totalProgress).toBe(40);
  });

  test('cancels active block uploads when the dialog closes', () => {
    const uploader = createUploader();
    const active = {
      uniqueIdentifier: 'block-1',
      isSaved: false,
      error: null,
      progress: () => 0.2,
      isUploading: () => true,
      cancel: jest.fn(),
    };
    uploader.state.isUploadProgressDialogShow = true;
    uploader.state.uploadFileList = [active];

    uploader.onCloseUploadDialog();

    expect(active.cancel).toHaveBeenCalled();
    expect(uploader.state.isUploadProgressDialogShow).toBe(false);
    expect(uploader.state.uploadFileList).toEqual([]);
  });

  test('preserves block entries when a legacy upload is added later', () => {
    const uploader = createUploader();
    const blockEntry = {
      uniqueIdentifier: 'block-1',
      isBlockUpload: true,
      isSaved: false,
      error: null,
      progress: () => 0.5,
      isUploading: () => true,
    };
    const legacyFile = createResumableFile('small.txt', {
      size: 10,
      progress: () => 0,
      isUploading: () => false,
    });
    uploader.state.uploadFileList = [blockEntry];
    uploader.resumable.files = [legacyFile];

    uploader.setUploadFileList();

    expect(uploader.state.uploadFileList).toEqual([blockEntry, legacyFile]);
  });

  test('cancel all still aborts a block upload that is already in saving at 100%', () => {
    const uploader = createUploader();
    const saving = {
      uniqueIdentifier: 'block-1',
      isSaved: false,
      error: null,
      _phase: 'saving',
      progress: () => 1,
      isUploading: () => true,
      cancel: jest.fn(),
    };
    const done = {
      uniqueIdentifier: 'done-1',
      isSaved: true,
      error: null,
      progress: () => 1,
      isUploading: () => false,
      cancel: jest.fn(),
    };
    uploader.state.uploadFileList = [saving, done];

    uploader.onCancelAllUploading();

    expect(saving.cancel).toHaveBeenCalled();
    expect(done.cancel).not.toHaveBeenCalled();
    expect(uploader.state.uploadFileList).toEqual([done]);
  });

  test('does not fake block bitrate from logical progress and sums block + legacy bitrate', () => {
    const uploader = createUploader();
    const blockEntry = {
      uniqueIdentifier: 'block-1',
      isBlockUpload: true,
      isSaved: false,
      error: null,
      size: 1000,
      _phase: 'uploading',
      _uploading: true,
      _progress: 0,
      progress: () => blockEntry._progress,
      isUploading: () => true,
    };
    uploader.state.uploadFileList = [blockEntry];

    // Logical progress must NOT fabricate a speed — bytes on the wire drive it.
    uploader.updateBlockUploadProgress(blockEntry, 0.5);
    expect(uploader.state.uploadBitrate).toBe(0);

    blockEntry._bitrate = 3000;
    const legacyFile = createResumableFile('small.txt', {
      size: 1000,
      progress: () => 0.5,
      isUploading: () => true,
      remainingTime: -1,
    });
    uploader.state.uploadFileList = [blockEntry, legacyFile];
    uploader.resumable.files = [legacyFile];
    uploader.getBitrate = jest.fn(() => {
      uploader.legacyUploadBitrate = 2000;
      return 2000;
    });

    uploader.onFileProgress(legacyFile);

    expect(uploader.state.uploadBitrate).toBe(5000);
  });

  test('feeds the block limiter only on fresh bitrate samples, not every progress tick', () => {
    jest.useFakeTimers();
    try {
      jest.setSystemTime(1000);
      const uploader = createUploader();
      uploader.blockLimiter = { noteBitrate: jest.fn() };
      const entry = {
        uniqueIdentifier: 'block-1',
        isBlockUpload: true,
        isSaved: false,
        error: null,
        _phase: 'uploading',
        _uploading: true,
        _progress: 0.5,
        progress: () => 0.5,
        isUploading: () => true,
      };
      uploader.state.uploadFileList = [entry];

      // First call seeds the sampler (not a real sample): no limiter feed.
      uploader.updateBlockUploadTransferredBytes(entry, 1024);
      expect(uploader.blockLimiter.noteBitrate).not.toHaveBeenCalled();

      // Rapid progress ticks inside the 500ms throttle window reuse the same reading
      // and must NOT count as new healthy samples (this is what made the ramp shoot up).
      uploader.updateBlockUploadTransferredBytes(entry, 1024);
      uploader.updateBlockUploadTransferredBytes(entry, 1024);
      expect(uploader.blockLimiter.noteBitrate).not.toHaveBeenCalled();

      // After the window elapses, exactly one fresh sample is fed.
      jest.setSystemTime(1600);
      uploader.updateBlockUploadTransferredBytes(entry, 1024);
      expect(uploader.blockLimiter.noteBitrate).toHaveBeenCalledTimes(1);
      expect(typeof uploader.blockLimiter.noteBitrate.mock.calls[0][0]).toBe('number');
    } finally {
      jest.useRealTimers();
    }
  });
});

describe('FileUploader block upload file-level queue (serialization)', () => {
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

  const makeQueueEntry = (id) => ({
    uniqueIdentifier: id,
    file: { name: id, size: 10 },
    isBlockUpload: true,
    isSaved: false,
    error: null,
    _progress: 0,
    progress: () => 0,
    isUploading: () => true,
    cancel: jest.fn(),
  });

  // Replace runBlockUpload with a deferred so the test controls when the active file
  // settles and the queue advances.
  const withDeferredRunBlockUpload = (uploader) => {
    const deferreds = [];
    uploader.runBlockUpload = jest.fn(() => {
      let resolve;
      const promise = new Promise((r) => { resolve = r; });
      deferreds.push({ resolve });
      return promise;
    });
    return deferreds;
  };

  test('a freshly created block entry starts in the "queued" phase (renders Waiting, not Hashing)', () => {
    const uploader = createUploader();
    const entry = uploader.createBlockUploadEntry({
      uniqueIdentifier: 'x', fileName: 'x.bin', relativePath: 'x.bin', file: { name: 'x.bin', size: 10 },
    });
    expect(entry._phase).toBe('queued');
  });

  test('runs only one block file at a time; the next starts when the active one settles', async () => {
    const uploader = createUploader();
    const deferreds = withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    const c = makeQueueEntry('c');

    uploader.enqueueBlockUpload(a, a.file);
    uploader.enqueueBlockUpload(b, b.file);
    uploader.enqueueBlockUpload(c, c.file);

    // Only the first file is active; the other two wait.
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1);
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(a, a.file);
    expect(uploader.blockUploadQueue.length).toBe(2);

    deferreds[0].resolve();
    await settle();
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(2);
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(b, b.file);

    deferreds[1].resolve();
    await settle();
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(3);
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(c, c.file);

    deferreds[2].resolve();
    await settle();
    expect(uploader.activeBlockUpload).toBe(null);
    expect(uploader.blockUploadQueue.length).toBe(0);
  });

  test('a block file releases the slot at "saving" so the next starts while it commits', async () => {
    const uploader = createUploader();
    const deferreds = withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    uploader.state.uploadFileList = [a, b];

    uploader.enqueueBlockUpload(a, a.file); // active
    uploader.enqueueBlockUpload(b, b.file); // waiting
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1);
    expect(uploader.activeBlockUpload.entry).toBe(a);
    expect(uploader.blockUploadQueue.length).toBe(1);

    // a enters its commit phase → slot handed off, b starts WITHOUT a having settled.
    uploader.setBlockUploadPhase(a, 'saving');
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(2);
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(b, b.file);
    expect(uploader.activeBlockUpload.entry).toBe(b);
    expect(uploader.blockUploadQueue.length).toBe(0);

    // a's commit finishing later must NOT null b's slot or wedge the queue.
    deferreds[0].resolve();
    await settle();
    expect(uploader.activeBlockUpload.entry).toBe(b);
  });

  test('a needs_upload re-entry into "saving" does not re-release or strand the slot', async () => {
    const uploader = createUploader();
    withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    uploader.state.uploadFileList = [a, b];

    uploader.enqueueBlockUpload(a, a.file);
    uploader.enqueueBlockUpload(b, b.file);
    uploader.setBlockUploadPhase(a, 'saving'); // hands off to b
    expect(uploader.activeBlockUpload.entry).toBe(b);

    // commit reported needs_upload → a goes back to uploading, then saving again.
    uploader.setBlockUploadPhase(a, 'uploading');
    uploader.setBlockUploadPhase(a, 'saving');

    // b keeps the slot; a (already handed off) cannot grab or strand it.
    expect(uploader.activeBlockUpload.entry).toBe(b);
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(2);
  });

  test('cancelling a queued (waiting) block file removes it so it never starts', async () => {
    const uploader = createUploader();
    const deferreds = withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    uploader.state.uploadFileList = [a, b];

    uploader.enqueueBlockUpload(a, a.file); // active
    uploader.enqueueBlockUpload(b, b.file); // waiting
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1);
    expect(uploader.blockUploadQueue.length).toBe(1);

    uploader.onUploadCancel(b); // cancel the waiting file
    expect(uploader.blockUploadQueue.length).toBe(0);
    expect(b.cancel).toHaveBeenCalled();

    deferreds[0].resolve(); // active finishes → drain finds an empty queue
    await settle();
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1); // b never ran
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(a, a.file);
  });

  test('closing the dialog clears queued jobs but keeps the active marker until it settles', async () => {
    const uploader = createUploader();
    const deferreds = withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    uploader.state.uploadFileList = [a, b];

    uploader.enqueueBlockUpload(a, a.file);
    uploader.enqueueBlockUpload(b, b.file);
    expect(uploader.activeBlockUpload).not.toBe(null);
    expect(uploader.blockUploadQueue.length).toBe(1);

    uploader.onCloseUploadDialog();
    // Pending jobs are dropped, but the active (now-cancelled) job still owns the slot
    // until its promise settles, so nothing new can start mid-teardown.
    expect(uploader.blockUploadQueue.length).toBe(0);
    expect(uploader.activeBlockUpload).not.toBe(null);

    deferreds[0].resolve(); // active settles → marker released, queue empty
    await settle();
    expect(uploader.activeBlockUpload).toBe(null);
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1); // b (removed) never ran
  });

  test('cancel-all/close does not start a new block file until the cancelled active one settles', async () => {
    const uploader = createUploader();
    const deferreds = withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');
    uploader.state.uploadFileList = [a];

    uploader.enqueueBlockUpload(a, a.file); // active
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1);

    uploader.onCancelAllUploading(); // clears pending queue; a is aborted but still settling
    // A new upload arrives during the abort window.
    uploader.enqueueBlockUpload(b, b.file);
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1); // b waits, NOT started

    deferreds[0].resolve(); // old active finally settles → release slot
    await settle();
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(2);
    expect(uploader.runBlockUpload).toHaveBeenLastCalledWith(b, b.file);
  });

  test('enqueueBlockUpload is idempotent for the same entry (no double CAS session)', () => {
    const uploader = createUploader();
    withDeferredRunBlockUpload(uploader);
    const a = makeQueueEntry('a');
    const b = makeQueueEntry('b');

    expect(uploader.enqueueBlockUpload(a, a.file)).toBe(true); // becomes active
    expect(uploader.enqueueBlockUpload(a, a.file)).toBe(false); // already active → skipped
    expect(uploader.runBlockUpload).toHaveBeenCalledTimes(1);

    expect(uploader.enqueueBlockUpload(b, b.file)).toBe(true); // queued
    expect(uploader.enqueueBlockUpload(b, b.file)).toBe(false); // already queued → skipped
    expect(uploader.blockUploadQueue.length).toBe(1);
  });
});

describe('FileUploader duplicate-name prompting', () => {
  const dupProps = (names) => ({ direntList: names.map(name => ({ type: 'file', name })) });

  test('prompts (and holds out of the queue) for a duplicate even in a single upload', () => {
    const uploader = createUploader(dupProps(['dup.txt']));
    const f = createResumableFile('dup.txt', { file: { name: 'dup.txt', size: 10 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]);

    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(f);
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(f);
    // Held: it must NOT have started uploading on its own, and must NOT be rendered.
    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.state.uploadFileList).not.toContain(f);
  });

  test('prompts for a duplicate inside a multi-file batch and offers apply-to-all', () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['a.txt']));
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const b = createResumableFile('b.txt', { file: { name: 'b.txt', size: 10 } });
    uploader.resumable.files = [a, b];

    uploader.onFileAdded(b, [a, b]); // not a duplicate → normal flow
    uploader.onFileAdded(a, [a, b]); // duplicate → prompt

    expect(uploader.state.currentResumableFile).toBe(a);
    expect(uploader.state.duplicateBatchActive).toBe(true); // drives the "apply to all" checkbox
  });

  test('a large (block-eligible) duplicate also prompts and replace routes to the block flow', () => {
    shouldUseBlockUpload.mockReturnValue(true);
    const uploader = createUploader(dupProps(['big.bin']));
    uploader.runBlockUpload = jest.fn();
    const f = createResumableFile('big.bin', { file: { name: 'big.bin', size: 999 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]);
    expect(uploader.state.isUploadRemindDialogShow).toBe(true); // not silently auto-renamed

    uploader.replaceRepetitionFile(false); // Replace
    // The block duplicate is routed through the file-level queue; the replace decision
    // rides on entry._replace, which runBlockUpload reads (no third-arg).
    const call = uploader.runBlockUpload.mock.calls[0];
    expect(call[0].isBlockUpload).toBe(true);
    expect(call[0]._replace).toBe(true);
    expect(call[1]).toBe(f.file);
  });

  test('"Don\'t replace" uploads a legacy duplicate as-is (replace flag 0, shared target, backend auto-renames)', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['k.txt']));
    const f = createResumableFile('k.txt', { file: { name: 'k.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.uploadFile(false); // Don't replace
    await flushPromises();

    expect(f.formData.replace).toBe(0);
    expect(uploader.resumable.opts.target).toBe('/upload/tok?ret-json=1'); // shared target
    expect(uploader.resumable.files).toContain(f);
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('apply-to-all resolves every held duplicate with the same action without re-prompting', () => {
    const uploader = createUploader(dupProps(['a.txt', 'b.txt']));
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const b = createResumableFile('b.txt', { file: { name: 'b.txt', size: 10 } });
    uploader.resumable.files = [a, b];

    uploader.onFileAdded(a, [a, b]); // prompt a
    uploader.onFileAdded(b, [a, b]); // queue b

    uploader.applyDuplicateDecision = jest.fn();
    uploader.replaceRepetitionFile(true); // Replace + apply to all

    expect(uploader.duplicateBulkAction).toBe('replace');
    expect(uploader.applyDuplicateDecision).toHaveBeenCalledWith(a, 'replace');
    expect(uploader.applyDuplicateDecision).toHaveBeenCalledWith(b, 'replace');
    expect(uploader.pendingDuplicates).toEqual([]);
    expect(uploader.state.isUploadRemindDialogShow).toBe(false);
  });

  test('a later duplicate in the SAME batch auto-applies the bulk action without re-prompting', () => {
    const uploader = createUploader(dupProps(['a.txt', 'c.txt']));
    uploader.startLegacyDuplicateUpload = jest.fn();
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const c = createResumableFile('c.txt', { file: { name: 'c.txt', size: 10 } });
    const batch = [a, c]; // resumable hands the SAME array to every fileAdded in a batch
    uploader.resumable.files = [a, c];

    uploader.onFileAdded(a, batch); // prompt a
    uploader.uploadFile(true); // "Don't replace" + apply to all → bulk = 'keep'
    uploader.onFileAdded(c, batch); // later duplicate, SAME batch → auto-applied

    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(c);
    expect(uploader.startLegacyDuplicateUpload).toHaveBeenCalledWith(c, false); // 'keep' → no replace
    expect(uploader.state.isUploadRemindDialogShow).toBe(false); // no prompt
  });

  test('a duplicate in a NEW batch re-prompts — the bulk choice does not carry across batches', () => {
    const uploader = createUploader(dupProps(['a.txt', 'c.txt']));
    uploader.startLegacyDuplicateUpload = jest.fn();
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const c = createResumableFile('c.txt', { file: { name: 'c.txt', size: 10 } });

    uploader.onFileAdded(a, [a]); // batch 1
    uploader.uploadFile(true); // keep + apply to all → bulk = 'keep'
    uploader.onFileAdded(c, [c]); // batch 2 (different array) → must re-prompt

    expect(uploader.duplicateBulkAction).toBe(null);
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(c);
  });

  test('Cancel drops the held duplicate: it is neither uploaded nor re-queued', () => {
    const uploader = createUploader(dupProps(['c.txt']));
    uploader.runBlockUpload = jest.fn();
    uploader.startLegacyDuplicateUpload = jest.fn();
    const f = createResumableFile('c.txt', { file: { name: 'c.txt', size: 10 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]); // prompt
    uploader.cancelFileUpload(false); // Cancel

    expect(uploader.state.isUploadRemindDialogShow).toBe(false);
    expect(uploader.resumable.files).not.toContain(f);
    expect(uploader.runBlockUpload).not.toHaveBeenCalled();
    expect(uploader.startLegacyDuplicateUpload).not.toHaveBeenCalled();
  });

  test('legacy replace duplicate in a subfolder builds target_file with a slash', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/token-a' });
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/token-a' }); // shared target
    const uploader = createUploader({ path: '/docs', direntList: [{ type: 'file', name: 'existing.txt' }] });
    // Held files can be pulled from the queue before formData.parent_dir is set, so
    // simulate the empty-formData case the fallback must handle.
    const f = createResumableFile('existing.txt', { file: { name: 'existing.txt', size: 10 }, formData: {} });
    uploader.state.currentResumableFile = f;

    uploader.replaceRepetitionFile(false);
    await flushPromises();

    expect(f.formData.parent_dir).toBe('/docs/');
    expect(f.formData.target_file).toBe('/docs/existing.txt'); // not the malformed "/docsexisting.txt"
  });

  test('a resolved legacy duplicate does not start the queue until the shared target is set (405 regression)', async () => {
    let resolveLink;
    seafileAPI.getFileServerUploadLink.mockReturnValue(new Promise(r => { resolveLink = r; }));
    const uploader = createUploader(dupProps(['k.txt']));
    const f = createResumableFile('k.txt', { file: { name: 'k.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.uploadFile(false); // keep
    await flushPromises();
    expect(uploader.resumable.upload).not.toHaveBeenCalled(); // target not ready yet

    resolveLink({ data: '/upload/tok' });
    await flushPromises();
    expect(uploader.resumable.opts.target).toBe('/upload/tok?ret-json=1');
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('a link-fetch failure re-queues the held duplicate instead of dropping it (no stale Waiting row)', async () => {
    seafileAPI.getUpdateLink.mockRejectedValue(new Error('network'));
    const uploader = createUploader(dupProps(['x.txt']));
    const f = createResumableFile('x.txt', { file: { name: 'x.txt', size: 10 }, formData: {} });
    uploader.state.currentResumableFile = f;

    uploader.replaceRepetitionFile(false); // → startLegacyDuplicateUpload → getUpdateLink rejects
    await flushPromises();

    // Not silently lost; re-offered. And never left a row in the rendered list.
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(f);
    expect(uploader.state.uploadFileList).not.toContain(f);
  });

  test('a "keep" duplicate does NOT start the queue when the shared target fetch fails', async () => {
    seafileAPI.getFileServerUploadLink.mockRejectedValue(new Error('network'));
    const uploader = createUploader(dupProps(['k.txt']));
    const f = createResumableFile('k.txt', { file: { name: 'k.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.uploadFile(false); // keep → startLegacyDuplicateUpload → ensureSharedUploadTarget rejects
    await flushPromises();

    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(f);
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(f);
  });

  test('flag OFF: a non-duplicate legacy file uploads through the shared target (no per-page 405)', async () => {
    shouldUseBlockUpload.mockReturnValue(false);
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(); // empty direntList → no duplicates
    const f = createResumableFile('plain.txt', { file: { name: 'plain.txt', size: 10 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]);
    await flushPromises();

    expect(uploader.state.isUploadRemindDialogShow).toBe(false); // no duplicate prompt
    expect(uploader.resumable.opts.target).toBe('/upload/tok?ret-json=1'); // shared target, not page URL
    expect(uploader.resumableUpload).toHaveBeenCalledWith(f);
  });

  test('a re-queued replace attempt does not leak its update-link into a later keep decision', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['x.txt']));
    // Same held object that a previous Replace attempt already armed (per-file update
    // link + replace flag) before being re-queued.
    const f = createResumableFile('x.txt', {
      file: { name: 'x.txt', size: 10 },
      opts: { target: '/update/stale' },
      formData: { parent_dir: '/', replace: 1, target_file: '/x.txt' },
    });

    // User now picks "Don't replace" on it.
    uploader.startLegacyDuplicateUpload(f, false);
    await flushPromises();

    expect(f.formData.replace).toBe(0);
    expect(f.opts.target).toBeUndefined(); // stale update-link cleared → no accidental replace
    expect(f.formData.target_file).toBeUndefined();
  });

  test('a replace decision uses the update-link instance target, never the shared upload-link', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    seafileAPI.getFileServerUploadLink.mockRejectedValue(new Error('shared link down'));
    const uploader = createUploader(dupProps(['x.txt']));
    const f = createResumableFile('x.txt', { file: { name: 'x.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.replaceRepetitionFile(false);
    await flushPromises();

    // Replace ('update' mode) never touches the upload-link; a shared-link failure is
    // irrelevant to it.
    expect(seafileAPI.getFileServerUploadLink).not.toHaveBeenCalled();
    expect(uploader.resumable.opts.target).toBe('/update/tok');
    expect(f.formData.replace).toBe(1);
    expect(f.formData.target_file).toBe('/x.txt');
    expect(uploader.resumable.files).toContain(f);
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('cancelling the only held duplicate closes the otherwise-empty progress dialog', () => {
    const uploader = createUploader(dupProps(['c.txt']));
    const f = createResumableFile('c.txt', { file: { name: 'c.txt', size: 10 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]); // prompt → opens the progress panel + remind dialog
    expect(uploader.state.isUploadProgressDialogShow).toBe(true);

    uploader.cancelFileUpload(false); // Cancel the only duplicate

    expect(uploader.state.isUploadRemindDialogShow).toBe(false);
    expect(uploader.state.isUploadProgressDialogShow).toBe(false); // no empty panel left behind
  });

  test('re-dropping a file already uploaded in THIS session prompts instead of silently duplicating', () => {
    const uploader = createUploader(); // empty server direntList
    // A completed upload of the same name is still shown in the dialog.
    uploader.state.uploadFileList = [{
      uniqueIdentifier: 'done', fileName: 'all-databases.sql', isSaved: true, error: null, isUploading: () => false,
    }];
    const f = createResumableFile('all-databases.sql', { file: { name: 'all-databases.sql', size: 10 } });
    uploader.resumable.files = [f];

    uploader.onFileAdded(f, [f]);

    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(f);
    expect(uploader.state.isUploadRemindDialogShow).toBe(true); // prompted, not a silent 2nd row
    expect(uploader.state.currentResumableFile).toBe(f);
  });

  test('a re-add of a destination completed THIS session is caught synchronously even if the list is stale', () => {
    // The production bug: a block file finishes (fully deduplicated), then the same
    // destination is added again before the async uploadFileList / server direntList
    // reflects it → it slipped past the released guard and the stale list, producing a
    // second "Waiting…" row next to the "Uploaded" one. completedUploadNameKeys is the
    // synchronous authority that catches it regardless of the async state.
    const uploader = createUploader(); // empty server direntList
    uploader.state.uploadFileList = [];
    const first = createResumableFile('all-databases.sql', { uniqueIdentifier: 'done', file: { name: 'all-databases.sql', size: 10 } });
    uploader.markUploadSaved(first, 'all-databases.sql'); // moves the key to completed
    expect(uploader.completedUploadNameKeys.has('repo-1:/:all-databases.sql')).toBe(true);

    // Simulate the race: the saved entry is NOT in the rendered list yet.
    uploader.state.uploadFileList = [];
    const second = createResumableFile('all-databases.sql', { uniqueIdentifier: 'redrop', file: { name: 'all-databases.sql', size: 10 } });
    uploader.resumable.files = [second];
    uploader.onFileAdded(second, [second]);

    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(second); // held out, prompted
    expect(uploader.state.isUploadRemindDialogShow).toBe(true); // not a silent 2nd row
    expect(uploader.state.currentResumableFile).toBe(second);
  });

  test('closing the dialog clears the completed-destination guard for the next session', () => {
    const uploader = createUploader();
    uploader.state.uploadFileList = [];
    const f = createResumableFile('a.txt', { uniqueIdentifier: 'a', file: { name: 'a.txt', size: 10 } });
    uploader.markUploadSaved(f, 'a.txt');
    expect(uploader.completedUploadNameKeys.size).toBe(1);

    uploader.onCloseUploadDialog();

    expect(uploader.completedUploadNameKeys.size).toBe(0);
  });

  test('the parent passes showApplyToAll (duplicateBatchActive) to the remind dialog', () => {
    const uploader = createUploader();
    uploader.state.isUploadRemindDialogShow = true;
    uploader.state.currentResumableFile = { uniqueIdentifier: 'x', fileName: 'x' };
    uploader.state.duplicateBatchActive = true;

    const dialog = findRenderedByType(uploader.render(), UploadRemindDialog);
    expect(dialog).not.toBeNull();
    expect(dialog.props.showApplyToAll).toBe(true);
  });

  test('a replace is HELD while the upload-link queue is busy, then runs with the update-link when idle', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    const uploader = createUploader(dupProps(['dup.txt']));

    // A normal file uploads via the upload-link → 'upload' mode active.
    const normal = createResumableFile('new.txt', { file: { name: 'new.txt', size: 10 } });
    uploader.resumable.files = [normal];
    uploader.onFileAdded(normal, [normal]);
    await flushPromises();
    expect(uploader.activeLegacyMode).toBe('upload');
    uploader.resumable.isUploading.mockReturnValue(true);
    uploader.resumable.upload.mockClear();

    // Resolve a Replace while the upload-link queue is in flight → it must be HELD, not
    // started (resumablejs routes ALL chunks to one instance target, so starting now
    // would reroute the normal file to the update endpoint).
    const dup = createResumableFile('dup.txt', { file: { name: 'dup.txt', size: 10 } });
    uploader.state.currentResumableFile = dup;
    uploader.replaceRepetitionFile(false);
    await flushPromises();

    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.legacyHold.some(h => h.resumableFile === dup)).toBe(true);
    expect(uploader.resumable.files).not.toContain(dup); // held out of the running queue

    // The upload-link queue finishes → the held replace runs with the update-link target.
    uploader.resumable.isUploading.mockReturnValue(false);
    uploader.onComplete();
    await flushPromises();

    expect(uploader.resumable.opts.target).toBe('/update/tok');
    expect(uploader.resumable.files).toContain(dup);
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('a lone replace (queue idle) starts immediately with the update-link', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    const uploader = createUploader(dupProps(['only.txt']));
    const f = createResumableFile('only.txt', { file: { name: 'only.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.replaceRepetitionFile(false);
    await flushPromises();

    expect(uploader.activeLegacyMode).toBe('update');
    expect(uploader.resumable.opts.target).toBe('/update/tok');
    expect(uploader.resumable.upload).toHaveBeenCalled();
  });

  test('apply-to-all Cancel closes both dialogs and leaves no empty progress panel', () => {
    const uploader = createUploader(dupProps(['a.txt', 'b.txt']));
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const b = createResumableFile('b.txt', { file: { name: 'b.txt', size: 10 } });
    uploader.resumable.files = [a, b];

    uploader.onFileAdded(a, [a, b]); // prompt a
    uploader.onFileAdded(b, [a, b]); // queue b
    expect(uploader.state.isUploadProgressDialogShow).toBe(true);

    uploader.cancelFileUpload(true); // Cancel + apply to all

    expect(uploader.state.isUploadRemindDialogShow).toBe(false);
    expect(uploader.state.isUploadProgressDialogShow).toBe(false); // no empty panel
    expect(uploader.pendingDuplicates).toEqual([]);
  });
});

describe('FileUploader target-mode scheduler + sync dedup guard', () => {
  const dupProps = (names) => ({ direntList: names.map(name => ({ type: 'file', name })) });

  test('a rapid second add of the same destination is dropped (synchronous dedup guard)', () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(); // empty direntList → not a server duplicate
    const f1 = createResumableFile('a.txt', { uniqueIdentifier: 'a-1', file: { name: 'a.txt', size: 10 } });
    const f2 = createResumableFile('a.txt', { uniqueIdentifier: 'a-2', file: { name: 'a.txt', size: 10 } });

    uploader.onFileAdded(f1, [f1]); // first → reserves the key, schedules
    uploader.onFileAdded(f2, [f2]); // rapid second, same destination → dropped

    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(f2);
    expect(toaster.warning).toHaveBeenCalled();
    expect(uploader.legacyHold.length).toBe(0);
    expect(uploader.activeUploadNameKeys.has('repo-1:/:a.txt')).toBe(true);
  });

  test('the dedup key is released on success so a later re-drop can be offered the prompt', () => {
    const uploader = createUploader();
    const f = createResumableFile('a.txt', { uniqueIdentifier: 'a-1' });
    uploader.activeUploadNameKeys.add(uploader.getUploadDestinationKey(f));
    uploader.state.uploadFileList = [{ uniqueIdentifier: 'a-1', fileName: 'a.txt' }];

    uploader.markUploadSaved(f, 'a.txt');

    expect(uploader.activeUploadNameKeys.has(uploader.getUploadDestinationKey(f))).toBe(false);
  });

  test('apply-to-all Replace runs every replace via the update-link with no hold (queue idle)', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    const uploader = createUploader(dupProps(['a.txt', 'b.txt']));
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const b = createResumableFile('b.txt', { file: { name: 'b.txt', size: 10 } });
    uploader.resumable.files = [a, b];

    uploader.onFileAdded(a, [a, b]); // prompt a
    uploader.onFileAdded(b, [a, b]); // queue b
    uploader.replaceRepetitionFile(true); // Replace + apply to all
    await flushPromises();

    expect(uploader.activeLegacyMode).toBe('update');
    expect(uploader.legacyHold.length).toBe(0); // both share 'update' → no conflict
    expect(uploader.resumable.files).toEqual(expect.arrayContaining([a, b]));
    expect(uploader.resumable.opts.target).toBe('/update/tok');
    expect(a.formData.replace).toBe(1);
    expect(b.formData.replace).toBe(1);
  });

  test('a normal file added while a replace is uploading is held (Waiting), then runs upload-link when idle', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['dup.txt']));

    // Replace running → 'update' mode active.
    const dup = createResumableFile('dup.txt', { file: { name: 'dup.txt', size: 10 } });
    uploader.state.currentResumableFile = dup;
    uploader.replaceRepetitionFile(false);
    await flushPromises();
    expect(uploader.activeLegacyMode).toBe('update');
    uploader.resumable.isUploading.mockReturnValue(true);
    uploader.resumable.upload.mockClear();

    // A normal file is dropped during the replace → must be held (different mode).
    const normal = createResumableFile('new.txt', { file: { name: 'new.txt', size: 10 } });
    uploader.resumable.files = [...uploader.resumable.files, normal];
    uploader.onFileAdded(normal, [normal]);
    await flushPromises();

    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.legacyHold.some(h => h.resumableFile === normal && h.mode === 'upload')).toBe(true);
    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(normal);
    expect(uploader.state.uploadFileList).toContain(normal); // rendered as "Waiting…"

    // Replace finishes → the held normal file runs with the upload-link. A lone file
    // resumes from its uploaded-bytes offset (resumableUpload), not a bare upload().
    uploader.resumable.isUploading.mockReturnValue(false);
    uploader.onComplete();
    await flushPromises();
    expect(uploader.activeLegacyMode).toBe('upload');
    expect(uploader.resumable.opts.target).toBe('/upload/tok?ret-json=1');
    expect(uploader.resumable.files).toContain(normal);
    expect(uploader.resumableUpload).toHaveBeenCalledWith(normal);
  });

  test('a legacy render preserves an in-flight block entry queued by a not-yet-committed setState (large file not dropped)', () => {
    const uploader = createUploader();
    // Simulate React batching: setState only QUEUES; commit applies updaters in order,
    // and a plain-object setState keeps the value it was computed with at call time.
    const pending = [];
    uploader.setState = (u) => { pending.push(u); };
    const commit = () => {
      while (pending.length) {
        const u = pending.shift();
        const next = typeof u === 'function' ? u(uploader.state) : u;
        uploader.state = { ...uploader.state, ...next };
      }
    };

    const blockEntry = {
      uniqueIdentifier: 'big', fileName: 'big.bin', isBlockUpload: true,
      isSaved: false, error: null, progress: () => 0, isUploading: () => true,
    };
    // Block entry added via a functional setState (like maybeBlockUpload), not committed yet.
    uploader.setState(prev => ({ uploadFileList: [...prev.uploadFileList, blockEntry] }));

    // A legacy file renders BEFORE the block setState commits.
    const small = createResumableFile('small.txt');
    uploader.resumable.files = [small];
    uploader.renderLegacyList();

    commit();

    const ids = uploader.state.uploadFileList.map(i => i.uniqueIdentifier);
    expect(ids).toContain('big'); // the large/block file is NOT dropped from the list
    expect(ids).toContain('small.txt-id');
  });

  test('a target-fetch failure frees the active mode and drains the next held group (no wedge)', async () => {
    seafileAPI.getFileServerUploadLink.mockRejectedValue(new Error('upload link down'));
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    const uploader = createUploader();

    const normal = createResumableFile('n.txt');
    normal._uploadMode = 'upload';
    const replaceFile = createResumableFile('r.txt', { uniqueIdentifier: 'r' });
    replaceFile._fromDuplicatePrompt = true;
    replaceFile._uploadMode = 'update'; // set by enqueueLegacyUpload before holding
    uploader.legacyHold = [{ resumableFile: replaceFile, mode: 'update' }];
    uploader.resumable.files = [normal];
    uploader.activeLegacyMode = 'upload';

    uploader.startLegacyFiles([normal]); // upload-link fetch rejects
    await flushPromises(); // upload-link rejection → catch drains the held replace group
    await flushPromises(); // update-link fetch resolves → instance target set

    // The failed normal file is removed; the held replace runs with the update-link.
    expect(uploader.resumable.files).not.toContain(normal);
    expect(uploader.resumable.opts.target).toBe('/update/tok');
    expect(uploader.resumable.files).toContain(replaceFile);
    expect(uploader.activeLegacyMode).toBe('update');
  });

  test('onComplete clears the cached update-link so the next replace fetches a fresh one', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/tok' });
    const uploader = createUploader();
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    uploader.state.currentResumableFile = a;
    uploader.replaceRepetitionFile(false);
    await flushPromises();
    expect(seafileAPI.getUpdateLink).toHaveBeenCalledTimes(1);

    uploader.onComplete(); // batch done → cached update-link dropped

    const b = createResumableFile('b.txt', { uniqueIdentifier: 'b', file: { name: 'b.txt', size: 10 } });
    uploader.state.currentResumableFile = b;
    uploader.replaceRepetitionFile(false);
    await flushPromises();
    expect(seafileAPI.getUpdateLink).toHaveBeenCalledTimes(2); // fresh fetch, not the stale cache
  });

  test('onLegacyQueueIdle does not start held work while a reset is in progress', () => {
    const uploader = createUploader();
    uploader._resettingUploads = true;
    uploader.legacyHold = [{ resumableFile: createResumableFile('h.txt'), mode: 'update' }];
    uploader.activeLegacyMode = 'upload';

    uploader.onLegacyQueueIdle();

    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.activeLegacyMode).toBe('upload'); // untouched during the reset
  });
});
