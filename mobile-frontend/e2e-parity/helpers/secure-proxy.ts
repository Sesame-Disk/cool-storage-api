import net from 'node:net';

/**
 * Loopback TCP forwarder that gives the parity suite a SECURE-CONTEXT origin
 * when it runs inside a container.
 *
 * Why this exists: the PWA uses secure-context-only browser APIs — Service
 * Workers and `crypto.subtle` (upload SHA-256 hashing in `src/lib/upload.ts`).
 * A secure context is HTTPS **or** http://localhost / 127.0.0.1. When the suite
 * runs in the `mobile-test` container and reaches the app via a container-DNS
 * host such as http://mobile-frontend, that is an INSECURE context, so uploads
 * and the service worker silently fail — a test-environment artifact that looks
 * exactly like a PWA bug. A real user on http://localhost:<port> is fine.
 *
 * Set `PARITY_PROXY_TARGET=<host:port>` (e.g. `mobile-frontend:80`) and this
 * starts a forwarder on `127.0.0.1:<PARITY_PROXY_PORT>` (default 18073) so specs
 * can hit `http://localhost:<port>` — a secure context — while traffic still
 * lands on the container-network service. Point `PARITY_BASE_URL` at that
 * localhost URL.
 *
 * (Chromium's `--unsafely-treat-insecure-origin-as-secure` is NOT used: it needs
 * a paired `--user-data-dir`, which Playwright forbids in launch args. A real
 * loopback origin is the reliable approach and needs no browser flags.)
 *
 * Returns a teardown function; a no-op when PARITY_PROXY_TARGET is unset (the
 * default localhost run needs no proxy).
 */
export async function maybeStartSecureProxy(): Promise<() => Promise<void>> {
  const target = process.env.PARITY_PROXY_TARGET;
  if (!target) return async () => {};

  const [host, portStr] = target.split(':');
  const targetPort = Number(portStr || '80');
  const listenPort = Number(process.env.PARITY_PROXY_PORT || '18073');

  const server = net.createServer((client) => {
    const upstream = net.connect(targetPort, host);
    // Tear the paired socket down on either side's error so a failed upstream
    // (e.g. the app not up yet) doesn't leak half-open connections.
    client.on('error', () => upstream.destroy());
    upstream.on('error', () => client.destroy());
    client.pipe(upstream);
    upstream.pipe(client);
  });

  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(listenPort, '127.0.0.1', () => resolve());
  });
  // eslint-disable-next-line no-console
  console.log(
    `[parity] secure-context proxy listening 127.0.0.1:${listenPort} -> ${host}:${targetPort}`,
  );

  return async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  };
}
