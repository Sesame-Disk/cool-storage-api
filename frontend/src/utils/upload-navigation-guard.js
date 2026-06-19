import { gettext } from './constants';

// Warns the user before they navigate away while an upload is still in flight,
// covering both in-app link clicks (SPA navigation) and the browser's native
// unload. Shared by every FileUploader variant so this guard — which is subtle
// (capture-phase interception, modifier/anchor filtering, one-shot suppression)
// and previously drifted apart between copies — lives in exactly one place.
//
// `hasActiveUploadWork` is injected: () => boolean, true while a real upload is
// in progress (not saved, not errored).
export default class UploadNavigationGuard {
  constructor(hasActiveUploadWork) {
    this.hasActiveUploadWork = hasActiveUploadWork;
    this.allowNavigationWithoutPrompt = false;
    this.resetTimer = null;
    this.onDocumentClick = this.onDocumentClick.bind(this);
  }

  attach() {
    document.addEventListener('click', this.onDocumentClick, true);
  }

  detach() {
    document.removeEventListener('click', this.onDocumentClick, true);
    window.clearTimeout(this.resetTimer);
    this.allowNavigationWithoutPrompt = false;
    this.resetTimer = null;
  }

  shouldPrompt() {
    return this.hasActiveUploadWork() && !this.allowNavigationWithoutPrompt;
  }

  // Returns true if navigation may proceed (nothing uploading, or the user
  // confirmed). On confirm, suppress the immediate follow-up beforeunload prompt
  // for the navigation the user just accepted, then re-arm so a later navigation
  // still warns.
  confirmIfUploading() {
    if (!this.shouldPrompt()) {
      return true;
    }

    const confirmed = window.confirm(gettext('A file is being uploaded. Are you sure you want to leave this page?'));
    if (!confirmed) {
      return false;
    }

    this.allowNavigationWithoutPrompt = true;
    window.clearTimeout(this.resetTimer);
    this.resetTimer = window.setTimeout(() => {
      this.allowNavigationWithoutPrompt = false;
      this.resetTimer = null;
    }, 1000);
    return true;
  }

  onbeforeunload() {
    if (this.shouldPrompt()) {
      return '';
    }
  }

  onDocumentClick(event) {
    if (!this.shouldPrompt() || event.defaultPrevented) {
      return;
    }

    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }

    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }

    const anchor = target.closest('a[href]');
    if (!anchor) {
      return;
    }

    const href = anchor.getAttribute('href');
    if (!href || href === '#' || /^\s*javascript:/i.test(href)) {
      return;
    }

    if (anchor.hasAttribute('download') || anchor.target === '_blank') {
      return;
    }

    const destination = new URL(anchor.href, window.location.href);
    const current = new URL(window.location.href);
    if (destination.origin === current.origin
      && destination.pathname === current.pathname
      && destination.search === current.search) {
      return;
    }

    if (!this.confirmIfUploading()) {
      event.preventDefault();
      event.stopPropagation();
    }
  }
}
