// Single source of truth for browser auth-related client state.
//
// Rule: the backend owns the session cookie. The web frontend must not persist
// or read the session token from localStorage.

const AUTH_COOKIE = 'sesamefs_auth';
const RETURN_URL_KEY = 'sso_return_url';

// Clear all client-side auth state. Used on logout and on 401.
//
// Cookie clearing is best-effort: the browser may have the cookie with Secure or
// a narrower Path and we might not be able to delete it from JS. That is fine —
// the backend will reject any stale value when it next reaches the server.
export function clearAuth() {
  try {
    localStorage.removeItem('sesamefs_user_email');
    localStorage.removeItem('sesamefs_user_name');
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith('custom_permissions_')) {
        localStorage.removeItem(key);
      }
    }
  } catch (e) {
    // localStorage may be unavailable in private mode — ignore.
  }
  // Best-effort cookie clear (backend also clears on /accounts/logout/).
  document.cookie = AUTH_COOKIE + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
}

// Store the post-login redirect target. Uses sessionStorage so that it auto-clears
// when the tab is closed (prevents stale redirects across unrelated sessions).
export function setReturnURL(next) {
  const validated = validateReturnURL(next);
  try {
    sessionStorage.setItem(RETURN_URL_KEY, validated);
  } catch (e) {
    // sessionStorage unavailable — ignore. Default will be used on retrieval.
  }
}

export function clearReturnURL() {
  try {
    sessionStorage.removeItem(RETURN_URL_KEY);
  } catch (e) {
    // ignore
  }
}

// Read and consume the stored return URL. Returns `/` if missing or invalid.
export function getReturnURL() {
  let stored = null;
  try {
    stored = sessionStorage.getItem(RETURN_URL_KEY);
    sessionStorage.removeItem(RETURN_URL_KEY);
  } catch (e) {
    // ignore
  }
  return validateReturnURL(stored);
}

function validateReturnURL(candidate) {
  if (!candidate || typeof candidate !== 'string') return '/';
  // Must be a site-relative path. Reject schemes and protocol-relative URLs.
  if (!candidate.startsWith('/') || candidate.startsWith('//')) return '/';
  return candidate;
}

function getLoginBaseURL() {
  const configured = window.app?.config?.loginUrl;
  if (typeof configured === 'string' && configured) {
    return configured;
  }

  return '/login/';
}

export function getLoginURL(reason = 'required', nextOverride = null) {
  const current = nextOverride === null
    ? window.location.pathname + window.location.search + window.location.hash
    : validateReturnURL(nextOverride);
  const params = [];
  if (reason === 'expired') params.push('expired=1');
  if (current && current !== '/') params.push('next=' + encodeURIComponent(current));

  const loginBaseURL = getLoginBaseURL();
  if (!params.length) {
    return loginBaseURL;
  }

  const separator = loginBaseURL.includes('?') ? '&' : '?';
  return loginBaseURL + separator + params.join('&');
}

// Redirect to the login page carrying the current location as `next`.
// `reason` is one of 'expired' (session died) or 'required' (never logged in).
export function redirectToLogin(reason = 'required', nextOverride = null) {
  window.location.href = getLoginURL(reason, nextOverride);
}
