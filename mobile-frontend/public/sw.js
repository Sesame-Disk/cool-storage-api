const CACHE_NAME = 'sesamefs-mobile-v3';
const API_CACHE_NAME = 'sesamefs-api-v1';
const MAX_API_CACHE_ENTRIES = 100;

const APP_SHELL = [
  '/',
  '/libraries/',
  '/login/',
  '/manifest.json',
  '/offline.html'
];

// Install: precache app shell
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => {
      return cache.addAll(APP_SHELL);
    })
  );
  // NOTE: intentionally NOT calling skipWaiting() here. A new worker stays in
  // "waiting" so the app can surface a "new version — reload" prompt and hand
  // control to the user (see SKIP_WAITING message handler below).
});

// Allow the page to activate a waiting worker on demand (update-reload flow).
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

// Activate: clean old caches
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys
          .filter((key) => key !== CACHE_NAME && key !== API_CACHE_NAME)
          .map((key) => caches.delete(key))
      );
    })
  );
  self.clients.claim();
});

// Background sync for uploads
self.addEventListener('sync', (event) => {
  if (event.tag === 'pending-uploads') {
    event.waitUntil(
      self.clients.matchAll().then((clients) => {
        clients.forEach((client) => {
          client.postMessage({ type: 'PROCESS_UPLOAD_QUEUE' });
        });
      })
    );
  }
});

// Fetch: routing strategies
self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);

  // API calls: stale-while-revalidate
  if (url.pathname.startsWith('/api2/') || url.pathname.startsWith('/api/v2.1/')) {
    if (request.method === 'GET') {
      event.respondWith(staleWhileRevalidate(request));
    } else {
      // Non-GET API calls: network only, register sync on failure
      event.respondWith(
        fetch(request).catch((err) => {
          if (request.method === 'POST' && url.pathname.includes('/upload')) {
            self.registration.sync.register('pending-uploads').catch(() => {});
          }
          return new Response(JSON.stringify({ error: 'Offline' }), {
            status: 503,
            statusText: 'Offline',
            headers: { 'Content-Type': 'application/json' },
          });
        })
      );
    }
    return;
  }

  // Navigation requests: network-first, fallback to cached app shell
  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request).catch(() => {
        return caches.match('/offline.html');
      })
    );
    return;
  }

  // Static assets (images, CSS, JS, fonts): cache-first, fallback to network
  if (isStaticAsset(url.pathname)) {
    event.respondWith(cacheFirst(request));
    return;
  }

  // Default: network-first
  event.respondWith(networkFirst(request));
});

function isStaticAsset(pathname) {
  return /\.(js|css|png|jpg|jpeg|gif|svg|ico|woff|woff2|ttf|eot)$/.test(pathname);
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) return cached;
  try {
    const response = await fetch(request);
    if (response.ok) {
      const cache = await caches.open(CACHE_NAME);
      cache.put(request, response.clone());
    }
    return response;
  } catch {
    return new Response('', { status: 408, statusText: 'Offline' });
  }
}

async function networkFirst(request) {
  try {
    const response = await fetch(request);
    if (response.ok) {
      const cache = await caches.open(CACHE_NAME);
      cache.put(request, response.clone());
    }
    return response;
  } catch {
    const cached = await caches.match(request);
    return cached || new Response('', { status: 408, statusText: 'Offline' });
  }
}

async function staleWhileRevalidate(request) {
  const cache = await caches.open(API_CACHE_NAME);
  const cached = await cache.match(request);

  const fetchPromise = fetch(request).then(async (response) => {
    if (response.ok) {
      await cache.put(request, response.clone());
      await trimCache(cache, MAX_API_CACHE_ENTRIES);
    }
    return response;
  }).catch(() => null);

  // Return cached response immediately if available, otherwise wait for network
  if (cached) {
    // Revalidate in background
    fetchPromise;
    return cached;
  }

  const networkResponse = await fetchPromise;
  if (networkResponse) return networkResponse;

  return new Response(JSON.stringify({ error: 'Offline' }), {
    status: 503,
    statusText: 'Offline',
    headers: { 'Content-Type': 'application/json' },
  });
}

async function trimCache(cache, maxEntries) {
  const keys = await cache.keys();
  if (keys.length > maxEntries) {
    const toDelete = keys.slice(0, keys.length - maxEntries);
    await Promise.all(toDelete.map((key) => cache.delete(key)));
  }
}
