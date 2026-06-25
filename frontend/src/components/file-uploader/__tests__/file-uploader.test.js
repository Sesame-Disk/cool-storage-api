import FileUploader from '../file-uploader';
import { seafileAPI } from '../../../utils/seafile-api';
import { shouldUseBlockUpload } from '../block-upload-orchestrator';

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

// Keep the real orchestrator (uploadFileViaBlocks etc.) but make the block-vs-legacy
// eligibility check controllable so duplicate-decision routing is testable.
jest.mock('../block-upload-orchestrator', () => {
  const actual = jest.requireActual('../block-upload-orchestrator');
  return { ...actual, shouldUseBlockUpload: jest.fn(() => false) };
});

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
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/token-a' }); // shared target

    const uploader = createUploader();
    const resumableFile = createResumableFile('existing.txt', { opts: undefined });

    // New flow: the held duplicate is the one currently prompted; replace resolves
    // it, re-arms its per-file target, and pushes it back into the queue.
    uploader.resumable.files = [];
    uploader.state.currentResumableFile = resumableFile;

    uploader.replaceRepetitionFile();
    await flushPromises();

    expect(resumableFile.opts).toEqual({ target: '/update/token-a' });
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
    // Held: it must NOT have started uploading on its own.
    expect(uploader.resumable.upload).not.toHaveBeenCalled();
  });

  test('prompts for a duplicate inside a multi-file batch and offers apply-to-all', () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['a.txt']));
    const a = createResumableFile('a.txt', { file: { name: 'a.txt', size: 10 } });
    const b = createResumableFile('b.txt', { file: { name: 'b.txt', size: 10 } });
    uploader.resumable.files = [a, b];

    // The duplicate is the second file added in a 2-file batch.
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
    const call = uploader.runBlockUpload.mock.calls[0];
    expect(call[0].isBlockUpload).toBe(true);
    expect(call[1]).toBe(f.file);
    expect(call[2]).toEqual({ replace: true });
  });

  test('"Don\'t replace" uploads a legacy duplicate as-is (replace flag 0, shared target, backend auto-renames)', async () => {
    seafileAPI.getFileServerUploadLink.mockResolvedValue({ data: '/upload/tok' });
    const uploader = createUploader(dupProps(['k.txt']));
    const f = createResumableFile('k.txt', { file: { name: 'k.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.uploadFile(false); // Don't replace
    await flushPromises();

    expect(f.formData.replace).toBe(0);
    // Keep uploads through the SHARED target (no per-file target needed).
    expect(uploader.resumable.opts.target).toBe('/upload/tok?ret-json=1');
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

  test('a resolved legacy duplicate does not start the queue until the shared target is set', async () => {
    // Regression: starting resumable.upload() before the shared target is set made
    // non-duplicate siblings POST to the empty default target (page URL → 405).
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

  test('a link-fetch failure re-queues the held duplicate instead of dropping it', async () => {
    seafileAPI.getUpdateLink.mockRejectedValue(new Error('network'));
    const uploader = createUploader(dupProps(['x.txt']));
    const f = createResumableFile('x.txt', { file: { name: 'x.txt', size: 10 }, formData: {} });
    uploader.state.currentResumableFile = f;

    uploader.replaceRepetitionFile(false); // → startLegacyDuplicateUpload → getUpdateLink rejects
    await flushPromises();

    // Not silently lost: it is re-queued and surfaced again for a decision/retry.
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(f);
  });

  test('a "keep" duplicate does NOT start the queue when the shared target fetch fails', async () => {
    // Regression: the catch used to call resumable.upload() anyway, so a "keep" file
    // (no per-file target) would POST to the empty/stale shared target → 405. It must
    // hold the queue and re-offer the duplicate instead.
    seafileAPI.getFileServerUploadLink.mockRejectedValue(new Error('network'));
    const uploader = createUploader(dupProps(['k.txt']));
    const f = createResumableFile('k.txt', { file: { name: 'k.txt', size: 10 } });
    uploader.state.currentResumableFile = f;

    uploader.uploadFile(false); // keep → startLegacyDuplicateUpload → ensureUploadTargetReady rejects
    await flushPromises();

    expect(uploader.resumable.upload).not.toHaveBeenCalled();
    expect(uploader.resumable.removeFile).toHaveBeenCalledWith(f);
    // Re-queued and surfaced again rather than silently dropped against a bad target.
    expect(uploader.state.isUploadRemindDialogShow).toBe(true);
    expect(uploader.state.currentResumableFile).toBe(f);
  });
});

describe('FileUploader shared upload target lifecycle', () => {
  test('a new batch adopts a fresh token instead of reusing the stale target from the last session', async () => {
    seafileAPI.getFileServerUploadLink
      .mockResolvedValueOnce({ data: '/upload/token-a' })
      .mockResolvedValueOnce({ data: '/upload/token-b' });

    const uploader = createUploader();
    const first = createResumableFile('a.iso');
    uploader.resumable.files = [first];
    uploader.onFileAdded(first, [first]);
    await flushPromises();
    expect(uploader.resumable.opts.target).toBe('/upload/token-a?ret-json=1');

    // Session completes → cached link AND the stale shared target are cleared.
    uploader.onComplete();
    expect(uploader.resumable.opts.target).toBe('');
    expect(uploader.isUploadLinkLoaded).toBe(false);

    // Next batch must mint and ADOPT a new token, not keep posting to token-a.
    const second = createResumableFile('b.iso');
    uploader.resumable.files = [second];
    uploader.onFileAdded(second, [second]);
    await flushPromises();

    expect(seafileAPI.getFileServerUploadLink).toHaveBeenCalledTimes(2);
    expect(uploader.resumable.opts.target).toBe('/upload/token-b?ret-json=1');
  });

  test('resetSharedUploadTarget keeps the running target when uploads are still in flight', () => {
    const uploader = createUploader();
    uploader.resumable.opts.target = '/upload/token-a?ret-json=1';
    uploader.isUploadLinkLoaded = true;

    // clearTarget defaults to false: only the cached promise/flag are reset.
    uploader.resetSharedUploadTarget();

    expect(uploader.isUploadLinkLoaded).toBe(false);
    expect(uploader._uploadTargetPromise).toBe(null);
    expect(uploader.resumable.opts.target).toBe('/upload/token-a?ret-json=1');
  });
});
