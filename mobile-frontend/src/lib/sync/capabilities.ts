// Folder sync depends on the File System Access API (`showDirectoryPicker`),
// which reads a real local disk folder. It exists on Chromium desktop + Android
// but NOT on iOS Safari or Firefox. Every piece of sync UI gates on this so the
// feature is cleanly hidden/disabled where it can't work — same guard shape as
// the `navigator.share` check in `src/lib/share.ts`.
export function supportsFolderSync(): boolean {
  return (
    typeof window !== 'undefined' &&
    window.isSecureContext &&
    'showDirectoryPicker' in window
  );
}
