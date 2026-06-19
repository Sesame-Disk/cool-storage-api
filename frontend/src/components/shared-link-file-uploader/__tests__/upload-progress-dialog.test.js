import UploadProgressDialog, { findActiveUploadFile } from '../upload-progress-dialog';

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
// isUploading() stays true while the last chunk awaits the server response, so
// isFinalizing is what marks it as saving.
const saving = (id) => makeFile(id, { isUploading: () => true, isFinalizing: true });
const waiting = (id) => makeFile(id, { isUploading: () => false });
const saved = (id) => makeFile(id, { isSaved: true });

describe('findActiveUploadFile', () => {
  test('prefers the file transferring bytes over one stuck in Saving...', () => {
    const f1 = saving('f1');
    const f2 = transferring('f2');
    const f3 = waiting('f3');
    expect(findActiveUploadFile([f1, f2, f3])).toBe(f2);
  });

  test('falls back to the first unsaved file when none are transferring bytes', () => {
    const f1 = saving('f1');
    const f2 = waiting('f2');
    expect(findActiveUploadFile([f1, f2])).toBe(f1);
  });

  test('returns null when every file is saved', () => {
    expect(findActiveUploadFile([saved('f1'), saved('f2')])).toBeNull();
  });

  test('ignores failed files', () => {
    const failed = makeFile('f1', { error: 'boom', isUploading: () => true });
    const f2 = transferring('f2');
    expect(findActiveUploadFile([failed, f2])).toBe(f2);
  });
});

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

describe('UploadProgressDialog scrollActiveUploadIntoView geometry', () => {
  const buildDialog = ({ container, row, isMinimized = false }) => {
    const dialog = new UploadProgressDialog({ uploadFileList: [] });
    dialog.state = { isMinimized };
    dialog.contentRef = { current: container };
    dialog.activeUploadRowRef = { current: row };
    return dialog;
  };

  // The scroll container box sits 100px down the page and is 200px tall.
  const makeContainer = (scrollTop) => ({
    scrollTop,
    clientHeight: 200,
    getBoundingClientRect: () => ({ top: 100, height: 200 }),
  });

  const makeRow = (viewportTop, height = 50) => ({
    getBoundingClientRect: () => ({ top: viewportTop, height }),
  });

  test('scrolls down to reveal an active row below the viewport', () => {
    // Regression: the old offsetTop math collapsed to scrollTop = 0 here, so a
    // row near the bottom of a long list (e.g. file 18/18) never came into view.
    const container = makeContainer(0);
    // Row 600px below the container's content top => well past the 200px viewport.
    const dialog = buildDialog({ container, row: makeRow(700) });

    dialog.scrollActiveUploadIntoView();

    // rowBottom (650) - clientHeight (200) + 8 = 458
    expect(container.scrollTop).toBe(458);
  });

  test('scrolls up to reveal an active row above the viewport', () => {
    const container = makeContainer(500);
    // Row 40px above the container's content top while scrolled down 500px.
    const dialog = buildDialog({ container, row: makeRow(60) });

    dialog.scrollActiveUploadIntoView();

    // rowTop = (60 - 100) + 500 = 460 => scrollTop = max(0, 460 - 8) = 452
    expect(container.scrollTop).toBe(452);
  });

  test('leaves scrollTop untouched when the active row is already visible', () => {
    const container = makeContainer(0);
    const dialog = buildDialog({ container, row: makeRow(150) });

    dialog.scrollActiveUploadIntoView();

    expect(container.scrollTop).toBe(0);
  });

  test('does nothing while minimized', () => {
    const container = makeContainer(0);
    const dialog = buildDialog({ container, row: makeRow(700), isMinimized: true });

    dialog.scrollActiveUploadIntoView();

    expect(container.scrollTop).toBe(0);
  });
});
