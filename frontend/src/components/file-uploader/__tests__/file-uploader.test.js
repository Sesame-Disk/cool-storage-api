import FileUploader from '../file-uploader';
import { seafileAPI } from '../../../utils/seafile-api';

jest.mock('../../../utils/seafile-api', () => ({
  seafileAPI: {
    getFileServerUploadLink: jest.fn(),
    getUpdateLink: jest.fn(),
  },
}));

jest.mock('../../../utils/utils', () => ({
  Utils: {
    registerGlobalVariable: jest.fn(),
    getErrorMsg: jest.fn(() => 'error'),
  },
}));

jest.mock('../../toast', () => ({
  danger: jest.fn(),
  success: jest.fn(),
}));

const flushPromises = () => new Promise(resolve => setTimeout(resolve, 0));

beforeEach(() => {
  jest.clearAllMocks();
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

  test('initializes per-file opts before scoping a replace upload target', async () => {
    seafileAPI.getUpdateLink.mockResolvedValue({ data: '/update/token-a' });

    const uploader = createUploader();
    const resumableFile = createResumableFile('existing.txt', { opts: undefined });

    uploader.resumable.files = [resumableFile];

    uploader.replaceRepetitionFile();
    await flushPromises();

    expect(resumableFile.opts).toEqual({ target: '/update/token-a' });
    expect(resumableFile.formData.replace).toBe(1);
    expect(resumableFile.formData.target_file).toBe('/existing.txt');
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
