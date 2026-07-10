import React, { useState, useEffect } from 'react';
import { seafileAPI } from '../../utils/seafile-api';
import { setReturnURL, getReturnURL } from '../../utils/auth-state';
import { siteTitle, loginBGPath } from '../../utils/constants';
import './login.css';

function LoginPage() {
  const [error, setError] = useState('');
  const [ssoLoading, setSsoLoading] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);

  // Local (username/password) auth state.
  const [localEnabled, setLocalEnabled] = useState(false);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [localLoading, setLocalLoading] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    // Show session expired message if redirected due to 401
    const params = new URLSearchParams(window.location.search);
    if (params.get('expired') === '1') {
      setError('Your session has expired. Please log in again.');
    } else if (params.get('error')) {
      setError('The login flow could not be completed. Please try again.');
    }

    seafileAPI.getOIDCConfig()
      .then(resp => {
        if (resp.data && resp.data.enabled) {
          setOidcEnabled(true);
        }
      })
      .catch(() => {
        setOidcEnabled(false);
      });

    // Discover which auth methods are enabled. The auth service (or the reverse
    // proxy in front of it) serves /api/v2.1/auth/methods on the same origin.
    fetch('/api/v2.1/auth/methods', { credentials: 'include' })
      .then(r => (r.ok ? r.json() : null))
      .then(data => {
        if (data) {
          setLocalEnabled(Boolean(data.local));
          if (data.oidc) setOidcEnabled(true);
        }
      })
      .catch(() => {
        setLocalEnabled(false);
      });
  }, []);

  const handleSSOLogin = async () => {
    setError('');
    setSsoLoading(true);

    try {
      const returnURL = new URLSearchParams(window.location.search).get('next') || '/';
      setReturnURL(returnURL);

      const redirectURI = window.location.origin + '/sso/';
      const resp = await seafileAPI.getOIDCLoginURL(redirectURI, returnURL);

      if (resp.data && resp.data.authorization_url) {
        window.location.href = resp.data.authorization_url;
      } else {
        throw new Error('Failed to get SSO login URL');
      }
    } catch (err) {
      console.error('SSO login error:', err);
      setError(err.response?.data?.error || err.message || 'SSO login failed. Please try again.');
      setSsoLoading(false);
    }
  };

  const handleLocalLogin = async (e) => {
    e.preventDefault();
    setError('');
    setNotice('');
    setLocalLoading(true);

    try {
      // Same-origin POST; the backend sets the sesamefs_auth session cookie on
      // success (the frontend never stores the token itself).
      const resp = await fetch('/api/v2.1/auth/local/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ email: email.trim(), password }),
      });

      if (!resp.ok) {
        let message = 'Login failed. Please check your credentials.';
        if (resp.status === 429) {
          message = 'Too many failed attempts. Please wait and try again.';
        } else {
          const data = await resp.json().catch(() => null);
          if (data && data.error) message = data.error;
        }
        setError(message);
        setLocalLoading(false);
        return;
      }

      const data = await resp.json();
      try {
        if (data.email) localStorage.setItem('sesamefs_user_email', data.email);
        if (data.name) localStorage.setItem('sesamefs_user_name', data.name);
      } catch (storageErr) {
        // localStorage unavailable (private mode) — non-fatal.
      }

      if (data.must_change_password) {
        // Let them in but flag that a password change is expected.
        setNotice('You are using a temporary password. Please change it in Settings after signing in.');
      }

      const next = new URLSearchParams(window.location.search).get('next');
      if (next) setReturnURL(next);
      window.location.href = getReturnURL();
    } catch (err) {
      console.error('Local login error:', err);
      setError('Login failed. Please try again.');
      setLocalLoading(false);
    }
  };

  const bgStyle = loginBGPath ? { backgroundImage: `url(${loginBGPath})` } : {};
  const hasError = Boolean(error);

  return (
    <div className="login-page" style={bgStyle}>
      <div className="login-shell">
        <section className="login-hero">
          <div className="login-hero__eyebrow">SesameFS Access</div>
          <h1 className="login-hero__title">{siteTitle || 'SesameFS'}</h1>
          <p className="login-hero__copy">
            Sign in to reach your libraries, shares, and admin tools from one place.
          </p>
          <div className="login-hero__chips" aria-hidden="true">
            <span>sync</span>
            <span>share</span>
            <span>admin</span>
          </div>
        </section>

        <section className="login-panel" aria-label="Login form">
          <div className="login-panel__header">
            <div>
              <p className="login-panel__kicker">Workspace Login</p>
              <h2>Sign in to {siteTitle || 'SesameFS'}</h2>
            </div>
          </div>

          {hasError && (
            <div className="login-error" role="alert">
              {error}
            </div>
          )}
          {notice && (
            <div className="login-note login-note--warning" role="status">
              {notice}
            </div>
          )}

          {localEnabled && (
            <form className="login-local" onSubmit={handleLocalLogin}>
              <label className="login-field">
                <span className="login-field__label">Email</span>
                <input
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </label>
              <label className="login-field">
                <span className="login-field__label">Password</span>
                <input
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </label>
              <button type="submit" className="login-submit" disabled={localLoading}>
                {localLoading ? 'Signing in…' : 'Sign in'}
              </button>
            </form>
          )}

          {localEnabled && oidcEnabled && (
            <div className="login-divider" aria-hidden="true"><span>or</span></div>
          )}

          {oidcEnabled && (
            <div className="login-sso">
              <button
                type="button"
                className="login-submit login-submit--secondary"
                onClick={handleSSOLogin}
                disabled={ssoLoading}
              >
                {ssoLoading ? 'Redirecting...' : 'Continue with SSO'}
              </button>
              <p className="login-note">
                You will be redirected to the Accounts identity provider and then returned here.
              </p>
            </div>
          )}

          {!localEnabled && !oidcEnabled && (
            <div className="login-note login-note--warning">
              No login methods are enabled in this environment. Enable local auth
              (AUTH_LOCAL_ENABLED) or OIDC to sign in.
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

export default LoginPage;
