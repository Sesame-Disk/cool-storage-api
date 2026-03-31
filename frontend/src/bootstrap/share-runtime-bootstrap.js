function escapeHtml(value) {
  return String(value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function ensureAppGlobals() {
  window.app = window.app || {};
  window.app.config = {
    serviceURL: '',
    siteRoot: '/',
    staticUrl: '/static/',
    ...(window.app.config || {}),
  };
  window.shared = window.shared || {};
  window.shared.pageOptions = window.shared.pageOptions || {};
  window.uploadLink = window.uploadLink || {};
}

export function getSiteRoot() {
  const siteRoot = window.app?.config?.siteRoot || '/';
  return siteRoot.endsWith('/') ? siteRoot : `${siteRoot}/`;
}

export function getShareToken() {
  const match = window.location.pathname.match(/^\/d\/([^/]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

export function getUploadToken() {
  const match = window.location.pathname.match(/^\/u\/d\/([^/]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

export function buildShareBootstrapUrl() {
  const token = getShareToken();
  if (!token) {
    return '';
  }

  const suffix = /^\/d\/[^/]+\/files\/?$/.test(window.location.pathname)
    ? `api/v2.1/share-links/${encodeURIComponent(token)}/files/bootstrap/`
    : `api/v2.1/share-links/${encodeURIComponent(token)}/bootstrap/`;

  return `${getSiteRoot()}${suffix}${window.location.search || ''}`;
}

export function buildUploadBootstrapUrl() {
  const token = getUploadToken();
  if (!token) {
    return '';
  }

  return `${getSiteRoot()}api/v2.1/upload-links/${encodeURIComponent(token)}/bootstrap/${window.location.search || ''}`;
}

export async function fetchBootstrap(url) {
  if (!url) {
    return {
      bootstrapError: true,
      message: 'Invalid public link URL.',
    };
  }

  try {
    const response = await fetch(url, { credentials: 'same-origin' });
    let data = null;

    try {
      data = await response.json();
    } catch (error) {
      data = null;
    }

    if (!response.ok) {
      return {
        bootstrapError: true,
        status: response.status,
        message: data?.error || 'Unable to load page bootstrap.',
      };
    }

    return data || {};
  } catch (error) {
    return {
      bootstrapError: true,
      message: 'Network error while loading the public link page.',
    };
  }
}

export function renderPublicBootstrapError(message, title = 'Unable to load page') {
  const mount = document.getElementById('wrapper') || document.body;
  mount.innerHTML = `
    <div style="min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f5f7fa;padding:24px;box-sizing:border-box;">
      <div style="max-width:32rem;width:100%;background:#fff;border-radius:12px;padding:32px;box-shadow:0 12px 32px rgba(15,23,42,0.12);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;">
        <h2 style="margin:0 0 12px;font-size:1.25rem;color:#0f172a;">${escapeHtml(title)}</h2>
        <p style="margin:0;color:#475569;line-height:1.5;">${escapeHtml(message)}</p>
      </div>
    </div>`;
}

export async function loadShareBootstrap() {
  ensureAppGlobals();

  if (Object.keys(window.shared.pageOptions || {}).length > 0) {
    return {
      render_mode: 'bundle',
      page_options: window.shared.pageOptions,
    };
  }

  const data = await fetchBootstrap(buildShareBootstrapUrl());
  if (data?.bootstrapError) {
    return data;
  }

  window.shared.pageOptions = { ...(data?.page_options || {}) };
  window.shared.bootstrapMeta = data;
  return data;
}

export async function loadUploadLinkBootstrap() {
  ensureAppGlobals();

  if (Object.keys(window.uploadLink || {}).length > 0) {
    return {
      render_mode: 'bundle',
      page_options: window.uploadLink,
    };
  }

  const data = await fetchBootstrap(buildUploadBootstrapUrl());
  if (data?.bootstrapError) {
    return data;
  }

  if (data?.render_mode && data.render_mode !== 'bundle') {
    return {
      bootstrapError: true,
      message: `Unsupported upload link render mode: ${data.render_mode}`,
    };
  }

  Object.assign(window.uploadLink, data?.page_options || {});
  window.uploadLinkBootstrapMeta = data;
  return data;
}