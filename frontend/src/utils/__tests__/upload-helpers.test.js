import { findActiveUploadFile, getActiveUploadId } from '../upload-active-file';
import { scrollRowIntoView } from '../upload-scroll';
import UploadNavigationGuard from '../upload-navigation-guard';

jest.mock('../constants', () => ({ gettext: (s) => s }));

const makeFile = (id, overrides = {}) => ({
  uniqueIdentifier: id,
  fileName: id,
  isSaved: false,
  error: null,
  isUploading: () => false,
  ...overrides,
});

const transferring = (id) => makeFile(id, { isUploading: () => true, remainingTime: 5 });
const saving = (id) => makeFile(id, { isUploading: () => true, isFinalizing: true });
const waiting = (id) => makeFile(id, { isUploading: () => false });
const saved = (id) => makeFile(id, { isSaved: true });

describe('findActiveUploadFile / getActiveUploadId', () => {
  test('prefers the file transferring bytes over one stuck in Saving...', () => {
    const f2 = transferring('f2');
    expect(findActiveUploadFile([saving('f1'), f2, waiting('f3')])).toBe(f2);
  });

  test('falls back to the first unsaved file when none are transferring bytes', () => {
    const f1 = saving('f1');
    expect(findActiveUploadFile([f1, waiting('f2')])).toBe(f1);
  });

  test('ignores failed files', () => {
    const failed = makeFile('f1', { error: 'boom', isUploading: () => true });
    const f2 = transferring('f2');
    expect(findActiveUploadFile([failed, f2])).toBe(f2);
  });

  test('returns null when every file is saved or the list is empty', () => {
    expect(findActiveUploadFile([saved('f1'), saved('f2')])).toBeNull();
    expect(findActiveUploadFile([])).toBeNull();
    expect(findActiveUploadFile(null)).toBeNull();
  });

  test('getActiveUploadId returns the active file id or null', () => {
    expect(getActiveUploadId([saving('f1'), transferring('f2')])).toBe('f2');
    expect(getActiveUploadId([])).toBeNull();
  });
});

describe('scrollRowIntoView', () => {
  // The scroll container box sits 100px down the page and is 200px tall.
  const makeContainer = (scrollTop) => ({
    scrollTop,
    clientHeight: 200,
    getBoundingClientRect: () => ({ top: 100, height: 200 }),
  });
  const makeRow = (viewportTop, height = 50) => ({
    getBoundingClientRect: () => ({ top: viewportTop, height }),
  });

  test('scrolls down to reveal a row below the viewport', () => {
    const c = makeContainer(0);
    scrollRowIntoView(c, makeRow(700)); // rowBottom 650 - 200 + 8
    expect(c.scrollTop).toBe(458);
  });

  test('scrolls up to reveal a row above the viewport', () => {
    const c = makeContainer(500);
    scrollRowIntoView(c, makeRow(60)); // rowTop (60-100)+500=460 -> 460-8
    expect(c.scrollTop).toBe(452);
  });

  test('leaves scrollTop untouched when the row is already visible', () => {
    const c = makeContainer(0);
    scrollRowIntoView(c, makeRow(150));
    expect(c.scrollTop).toBe(0);
  });

  test('no-ops on a missing container or row', () => {
    expect(() => scrollRowIntoView(null, makeRow(150))).not.toThrow();
    expect(() => scrollRowIntoView(makeContainer(0), null)).not.toThrow();
  });
});

describe('UploadNavigationGuard', () => {
  let active;
  let guard;

  beforeEach(() => {
    jest.useFakeTimers();
    active = true;
    guard = new UploadNavigationGuard(() => active);
    window.confirm = jest.fn(() => true);
  });

  afterEach(() => {
    jest.runOnlyPendingTimers();
    jest.useRealTimers();
  });

  const makeEvent = (href) => {
    const anchor = document.createElement('a');
    if (href != null) {
      anchor.setAttribute('href', href);
    }
    const target = document.createElement('span');
    anchor.appendChild(target);
    return {
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
  };

  test('onbeforeunload prompts only while an upload is active', () => {
    expect(guard.onbeforeunload()).toBe('');
    active = false;
    expect(guard.onbeforeunload()).toBeUndefined();
  });

  test('blocks a link click while uploading and the user cancels', () => {
    window.confirm.mockReturnValue(false);
    const event = makeEvent('/libraries/next/');

    guard.onDocumentClick(event);

    expect(window.confirm).toHaveBeenCalled();
    expect(event.preventDefault).toHaveBeenCalled();
    expect(event.stopPropagation).toHaveBeenCalled();
  });

  test('ignores javascript:void(0) pseudo-links', () => {
    const event = makeEvent('javascript:void(0)'); // eslint-disable-line no-script-url

    guard.onDocumentClick(event);

    expect(window.confirm).not.toHaveBeenCalled();
    expect(event.preventDefault).not.toHaveBeenCalled();
  });

  test('ignores modifier-key and non-primary-button clicks', () => {
    const event = makeEvent('/libraries/next/');
    event.metaKey = true;

    guard.onDocumentClick(event);

    expect(window.confirm).not.toHaveBeenCalled();
    expect(event.preventDefault).not.toHaveBeenCalled();
  });

  test('suppresses the follow-up beforeunload for one confirmed navigation, then re-arms', () => {
    expect(guard.confirmIfUploading()).toBe(true);
    expect(guard.onbeforeunload()).toBeUndefined();

    jest.advanceTimersByTime(1000);

    expect(guard.onbeforeunload()).toBe('');
  });

  test('does not prompt when no upload is active', () => {
    active = false;
    expect(guard.confirmIfUploading()).toBe(true);
    expect(window.confirm).not.toHaveBeenCalled();
  });

  test('detach clears the suppression timer and listener', () => {
    guard.attach();
    expect(guard.confirmIfUploading()).toBe(true);
    guard.detach();
    expect(guard.allowNavigationWithoutPrompt).toBe(false);
  });
});
