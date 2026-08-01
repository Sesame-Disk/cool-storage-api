import { describe, it, expect, afterEach } from 'vitest';
import { supportsFolderSync } from '../capabilities';

const originalDesc = Object.getOwnPropertyDescriptor(window, 'showDirectoryPicker');

function setSecureContext(value: boolean) {
  Object.defineProperty(window, 'isSecureContext', { value, configurable: true });
}

function setPicker(present: boolean) {
  if (present) {
    (window as unknown as { showDirectoryPicker: unknown }).showDirectoryPicker = () => Promise.resolve();
  } else {
    delete (window as unknown as { showDirectoryPicker?: unknown }).showDirectoryPicker;
  }
}

afterEach(() => {
  if (originalDesc) Object.defineProperty(window, 'showDirectoryPicker', originalDesc);
  else delete (window as unknown as { showDirectoryPicker?: unknown }).showDirectoryPicker;
});

describe('supportsFolderSync', () => {
  it('is true in a secure context with showDirectoryPicker', () => {
    setSecureContext(true);
    setPicker(true);
    expect(supportsFolderSync()).toBe(true);
  });

  it('is false when the File System Access API is absent (iOS Safari / Firefox)', () => {
    setSecureContext(true);
    setPicker(false);
    expect(supportsFolderSync()).toBe(false);
  });

  it('is false outside a secure context', () => {
    setSecureContext(false);
    setPicker(true);
    expect(supportsFolderSync()).toBe(false);
  });
});
