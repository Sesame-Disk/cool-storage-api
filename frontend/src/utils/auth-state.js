// Single source of truth for authentication state.
//
// Why this module exists:
// - Prior code spread token/cookie handling across seafile-api.js, login/index.js,
//   sso/index.js, logout.js, account.js and bootstrap-entry.js. Each spot had its
//   own idea of what "logged in" meant.
// - The backend sets the `sesamefs_auth` cookie in the canonical `email@token`
//   format during OIDC exchange. The previous frontend code overwrote that cookie
//   with just `<token>`, which corrupted the format and made every cookie-based
//   auth check on the backend fail — causing the double-login bug when entering
//   /sys/ or /org/.
// - Rule: the backend owns the cookie. JS only READS it (and clears it on logout
//   or 401). JS never writes the cookie with a token value.

const TOKEN_KEY = 'sesamefs_auth_token';
const AUTH_COOKIE = 'sesamefs_auth';
const RETURN_URL_KEY = 'sso_return_url';

export function getAuthToken() {
  // 1. Primary storage: localStorage.
  const stored = localStorage.getItem(TOKEN_KEY);
  if (stored) return stored;

  // 2. Fallback: the backend-set cookie `sesamefs_auth=email@token`. Only used
  //    when localStorage was cleared (e.g., by a 401 interceptor) but the session
  //    cookie is still valid. We extract the token portion and re-hydrate
  //    localStorage so subsequent reads are fast.
  try {
    const cookies = document.cookie.split(';');
    for (let i = 0; i < cookies.length; i++) {
      const cookie = cookies[i].trim();
      if (cookie.startsWith(AUTH_COOKIE + '=')) {
        const value = decodeURIComponent(cookie.substring(AUTH_COOKIE.length + 1));
        const lastAt = value.lastIndexOf('@');
        if (lastAt > 0 && lastAt < value.length - 1) {
          const cookieToken = value.substring(lastAt + 1);
          localStorage.setItem(TOKEN_KEY, cookieToken);
          return cookieToken;
        }
      }
    }
  } catch (e) {
    // Cookie parsing failed — ignore.
  }
  return null;
}

export function isAuthenticated() {
  return !!getAuthToken();
}

// Persist the token after a successful login.
//
// IMPORTANT: this does NOT write the `sesamefs_auth` cookie. The backend already
// set it with the correct `email@token` format in the Set-Cookie header of the
// OIDC exchange response. Writing it here with just `<token>` would corrupt the
// format and break cookie-based auth (the bug this module fixes).
export function setAuthTokenAndCookie(token) {
  localStorage.setItem(TOKEN_KEY, token);
}

// Clear all client-side auth state. Used on logout and on 401.
//
// Cookie clearing is best-effort: the browser may have the cookie with Secure or
// a narrower Path and we might not be able to delete it from JS. That is fine —
// the backend will reject any stale value when it next reaches the server.
export function clearAuth() {
  try {
    localStorage.removeItem(TOKEN_KEY);
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

// Redirect to the login page carrying the current location as `next`.
// `reason` is one of 'expired' (session died) or 'required' (never logged in).
export function redirectToLogin(reason = 'required') {
  const current = window.location.pathname + window.location.search + window.location.hash;
  const next = encodeURIComponent(current);
  const params = [];
  if (reason === 'expired') params.push('expired=1');
  if (current && current !== '/') params.push('next=' + next);
  const qs = params.length ? '?' + params.join('&') : '';
  window.location.href = '/login/' + qs;
}
