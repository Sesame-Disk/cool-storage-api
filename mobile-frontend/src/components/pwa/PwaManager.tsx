import { useEffect, useRef, useState } from 'react';

// PWA lifecycle UI: (1) Android/Chromium install prompt via beforeinstallprompt,
// (2) iOS Safari "Add to Home Screen" guidance (no install event exists there),
// (3) a "new version — reload" prompt driven by the waiting service worker.
// Mounted once from AppLayout. Self-contained so it works on every page.

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

const DISMISS_KEY = 'pwa-install-dismissed';

function isStandalone(): boolean {
  return (
    window.matchMedia?.('(display-mode: standalone)').matches ||
    // iOS Safari
    (window.navigator as unknown as { standalone?: boolean }).standalone === true
  );
}

function isIosSafari(): boolean {
  const ua = window.navigator.userAgent;
  const iOS = /iPad|iPhone|iPod/.test(ua);
  const webkit = /WebKit/.test(ua);
  const notOtherBrowser = !/CriOS|FxiOS|EdgiOS|OPiOS/.test(ua);
  return iOS && webkit && notOtherBrowser;
}

export default function PwaManager() {
  const [installEvent, setInstallEvent] = useState<BeforeInstallPromptEvent | null>(null);
  const [showInstall, setShowInstall] = useState(false);
  const [showIosGuide, setShowIosGuide] = useState(false);
  const [waitingWorker, setWaitingWorker] = useState<ServiceWorker | null>(null);
  // Only reload on controllerchange when WE applied an update — not when the
  // service worker first claims the page (clients.claim on activate), which
  // would spuriously reload on the very first visit and kill in-flight actions.
  const updatingRef = useRef(false);

  // --- Install prompt (Android/Chromium) ---
  useEffect(() => {
    const dismissed = localStorage.getItem(DISMISS_KEY) === '1';
    // Don't prompt to install on the auth pages — it's premature UX and the
    // bottom-anchored banner can overlap the login button on short screens.
    const onAuthPage = /^\/(login|sso)/.test(window.location.pathname);
    const onBIP = (e: Event) => {
      e.preventDefault();
      setInstallEvent(e as BeforeInstallPromptEvent);
      if (!dismissed && !isStandalone() && !onAuthPage) {
        // A real install prompt wins over the iOS fallback guidance.
        setShowIosGuide(false);
        setShowInstall(true);
      }
    };
    const onInstalled = () => {
      setShowInstall(false);
      setInstallEvent(null);
    };
    window.addEventListener('beforeinstallprompt', onBIP);
    window.addEventListener('appinstalled', onInstalled);

    // iOS has no beforeinstallprompt — offer guidance instead.
    if (!dismissed && isIosSafari() && !isStandalone() && !onAuthPage) setShowIosGuide(true);

    return () => {
      window.removeEventListener('beforeinstallprompt', onBIP);
      window.removeEventListener('appinstalled', onInstalled);
    };
  }, []);

  // --- Service-worker update detection ---
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return;
    let reg: ServiceWorkerRegistration | undefined;

    navigator.serviceWorker.getRegistration().then((r) => {
      if (!r) return;
      reg = r;
      if (r.waiting && navigator.serviceWorker.controller) setWaitingWorker(r.waiting);
      r.addEventListener('updatefound', () => {
        const installing = r.installing;
        if (!installing) return;
        installing.addEventListener('statechange', () => {
          if (installing.state === 'installed' && navigator.serviceWorker.controller) {
            setWaitingWorker(installing);
          }
        });
      });
    });

    let reloaded = false;
    const onControllerChange = () => {
      if (reloaded || !updatingRef.current) return;
      reloaded = true;
      window.location.reload();
    };
    navigator.serviceWorker.addEventListener('controllerchange', onControllerChange);
    return () => {
      navigator.serviceWorker.removeEventListener('controllerchange', onControllerChange);
      void reg;
    };
  }, []);

  const dismiss = () => {
    localStorage.setItem(DISMISS_KEY, '1');
    setShowInstall(false);
    setShowIosGuide(false);
  };

  const acceptInstall = async () => {
    if (!installEvent) return;
    await installEvent.prompt();
    await installEvent.userChoice.catch(() => undefined);
    setShowInstall(false);
    setInstallEvent(null);
  };

  const reloadForUpdate = () => {
    updatingRef.current = true;
    waitingWorker?.postMessage({ type: 'SKIP_WAITING' });
  };

  const bar =
    'fixed inset-x-0 bottom-16 z-50 mx-auto max-w-md px-4 pb-[env(safe-area-inset-bottom)]';
  const card =
    'flex items-center gap-3 rounded-2xl bg-white dark:bg-neutral-800 shadow-lg ring-1 ring-black/10 dark:ring-white/10 p-3';

  return (
    <>
      {showInstall && (
        <div className={bar} data-testid="pwa-install-banner" role="dialog" aria-label="Install app">
          <div className={card}>
            <img src="/icons/icon-96x96.png" alt="" width={40} height={40} className="rounded-lg" />
            <div className="flex-1 text-sm">
              <p className="font-semibold">Install Sesame Disk</p>
              <p className="text-neutral-500 dark:text-neutral-400">Add it to your home screen for a full-screen app.</p>
            </div>
            <button
              onClick={acceptInstall}
              data-testid="pwa-install-accept"
              className="rounded-full bg-[#eb8205] px-4 py-2 text-sm font-semibold text-white"
            >
              Install
            </button>
            <button onClick={dismiss} aria-label="Dismiss" className="p-2 text-neutral-400">✕</button>
          </div>
        </div>
      )}

      {showIosGuide && (
        <div className={bar} data-testid="pwa-ios-guidance" role="dialog" aria-label="Add to home screen">
          <div className={card}>
            <img src="/icons/icon-96x96.png" alt="" width={40} height={40} className="rounded-lg" />
            <div className="flex-1 text-sm">
              <p className="font-semibold">Add to Home Screen</p>
              <p className="text-neutral-500 dark:text-neutral-400">
                Tap the Share button, then “Add to Home Screen” to install.
              </p>
            </div>
            <button onClick={dismiss} aria-label="Dismiss" className="p-2 text-neutral-400">✕</button>
          </div>
        </div>
      )}

      {waitingWorker && (
        <div className={bar} data-testid="pwa-update-banner" role="dialog" aria-label="Update available">
          <div className={card}>
            <div className="flex-1 text-sm">
              <p className="font-semibold">New version available</p>
              <p className="text-neutral-500 dark:text-neutral-400">Reload to get the latest.</p>
            </div>
            <button
              onClick={reloadForUpdate}
              data-testid="pwa-update-reload"
              className="rounded-full bg-[#eb8205] px-4 py-2 text-sm font-semibold text-white"
            >
              Reload
            </button>
          </div>
        </div>
      )}
    </>
  );
}
