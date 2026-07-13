import { supportsFolderSync } from './capabilities';
import { syncAllAuto } from './syncEngine';

// The trigger model (agreed): a browser PWA can't do reliable background sync,
// so we sync every auto-sync library when the app regains focus / becomes
// visible / comes back online — plus manual "Sync now" from the UI. Debounced
// so a burst of focus+visibility+online events fires a single pass.

let started = false;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

function trigger(): void {
  if (typeof navigator !== 'undefined' && navigator.onLine === false) return;
  if (debounceTimer) clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => {
    debounceTimer = null;
    void syncAllAuto();
  }, 800);
}

function onVisibility(): void {
  if (document.visibilityState === 'visible') trigger();
}

/** Wire focus/visibility/online listeners once. Safe no-op where unsupported. */
export function startSyncController(): void {
  if (started || !supportsFolderSync()) return;
  started = true;

  window.addEventListener('focus', trigger);
  window.addEventListener('online', trigger);
  document.addEventListener('visibilitychange', onVisibility);

  // Initial catch-up pass shortly after mount.
  trigger();
}

export function stopSyncController(): void {
  if (!started) return;
  started = false;
  window.removeEventListener('focus', trigger);
  window.removeEventListener('online', trigger);
  document.removeEventListener('visibilitychange', onVisibility);
  if (debounceTimer) {
    clearTimeout(debounceTimer);
    debounceTimer = null;
  }
}
