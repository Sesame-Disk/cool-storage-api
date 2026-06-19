import UploadProgressDialog from '../upload-progress-dialog';

const makeFile = (id, overrides = {}) => ({
  uniqueIdentifier: id,
  fileName: id,
  isSaved: false,
  error: null,
  isUploading: () => false,
  ...overrides,
});

// A file genuinely transferring bytes: isUploading() true and not finalizing.
const transferring = (id) => makeFile(id, { isUploading: () => true, remainingTime: 5 });
// A file whose bytes are sent but the server is still finalizing ("Saving...").
const saving = (id) => makeFile(id, { isUploading: () => true, isFinalizing: true });
const waiting = (id) => makeFile(id, { isUploading: () => false });

// The active-file selection and scroll geometry now live in src/utils and are
// covered by src/utils/__tests__/upload-helpers.test.js. These tests cover only
// this dialog's wiring: that componentDidUpdate scrolls exactly when the active
// upload advances (and not on every progress-tick list rebuild).
describe('UploadProgressDialog auto-scroll gating', () => {
  const buildDialog = () => {
    const dialog = new UploadProgressDialog({ uploadFileList: [] });
    dialog.scrollActiveUploadIntoView = jest.fn();
    return dialog;
  };

  const runUpdate = (dialog, { nextList, nextState, prevList, prevState }) => {
    dialog.props = { uploadFileList: nextList };
    dialog.state = nextState;
    dialog.componentDidUpdate({ uploadFileList: prevList }, prevState);
  };

  test('does not auto-scroll when the active file is unchanged across a list rebuild', () => {
    const dialog = buildDialog();
    dialog.lastActiveUploadId = 'f1';
    const prevList = [transferring('f1'), waiting('f2')];
    // onFileProgress rebuilds the array on every chunk tick with fresh refs but
    // the same active file id.
    const nextList = [transferring('f1'), waiting('f2')];

    runUpdate(dialog, {
      nextList,
      nextState: { isMinimized: false },
      prevList,
      prevState: { isMinimized: false },
    });

    expect(dialog.scrollActiveUploadIntoView).not.toHaveBeenCalled();
  });

  test('auto-scrolls when the active file advances to the next upload', () => {
    const dialog = buildDialog();
    const prevList = [transferring('f1'), waiting('f2')];
    // f1 moved into "Saving..." and f2 started transferring bytes.
    const nextList = [saving('f1'), transferring('f2')];

    runUpdate(dialog, {
      nextList,
      nextState: { isMinimized: false },
      prevList,
      prevState: { isMinimized: false },
    });

    expect(dialog.scrollActiveUploadIntoView).toHaveBeenCalledTimes(1);
  });

  test('auto-scrolls when FileUploader reuses the same mutated file objects', () => {
    const dialog = buildDialog();
    dialog.lastActiveUploadId = 'f1';
    const f1 = transferring('f1');
    const f2 = waiting('f2');
    const prevList = [f1, f2];

    f1.isFinalizing = true;
    f2.isUploading = () => true;
    f2.remainingTime = 5;
    const nextList = [f1, f2];

    runUpdate(dialog, {
      nextList,
      nextState: { isMinimized: false },
      prevList,
      prevState: { isMinimized: false },
    });

    expect(dialog.scrollActiveUploadIntoView).toHaveBeenCalledTimes(1);
  });

  test('auto-scrolls when restored from a minimized state', () => {
    const dialog = buildDialog();
    const list = [transferring('f1')];

    runUpdate(dialog, {
      nextList: list,
      nextState: { isMinimized: false },
      prevList: list,
      prevState: { isMinimized: true },
    });

    expect(dialog.scrollActiveUploadIntoView).toHaveBeenCalledTimes(1);
  });
});
