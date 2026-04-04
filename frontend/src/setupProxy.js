/**
 * Webpack dev server proxy config (Create React App convention).
 * Proxies API and file-transfer requests to the Go backend so that
 * `npm start` works without CORS issues or cross-origin config.
 *
 * Applies ONLY in development (this file is not bundled into the production build).
 *
 * Target: Go backend URL, defaults to http://localhost:8080.
 * Override: set SESAMEFS_BACKEND_URL=http://localhost:8080 in frontend/.env.local
 */
const { createProxyMiddleware } = require('http-proxy-middleware');

const backendURL = process.env.SESAMEFS_BACKEND_URL || 'http://localhost:8080';

function shouldProxyRepoPath(pathname) {
  if (typeof pathname !== 'string') {
    return false;
  }

  pathname = pathname.split('?')[0].split('#')[0];

  return /^\/repo\/[^/]+\/(?:raw(?:\/|$)|history\/(?:download|view|raw)(?:\/|$))/.test(pathname);
}

module.exports = function (app) {
  const proxy = createProxyMiddleware({
    target: backendURL,
    changeOrigin: true,
    ws: false,
  });

  // API endpoints → Go backend
  app.use('/api', proxy);
  app.use('/api2', proxy);
  app.use('/seafhttp', proxy);
  app.use('/seafile', proxy);
  app.use('/ping', proxy);
  app.use('/health', proxy);
  app.use('/ready', proxy);
  app.use('/oauth', proxy);
  app.use('/client-login', proxy);
  app.use('/client-sso', proxy);
  app.use('/d', proxy);          // share link views
  app.use('/u', proxy);          // upload link views
  app.use('/lib', proxy);        // file view routes
  app.use((req, res, next) => {
    if (shouldProxyRepoPath(req.path || req.url || '')) {
      return proxy(req, res, next);
    }
    return next();
  });
  app.use('/onlyoffice', proxy);
  app.use('/office-convert', proxy);
  app.use('/smart-link', proxy);
  app.use('/billing', proxy);
  app.use('/share', proxy);
  app.use('/saml2', proxy);
  app.use('/org/custom', proxy);
  app.use('/accounts', proxy);   // OIDC redirect targets
};

module.exports.shouldProxyRepoPath = shouldProxyRepoPath;
