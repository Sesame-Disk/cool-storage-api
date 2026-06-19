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
  };
  uploader.setUploadFileList = jest.fn();
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
