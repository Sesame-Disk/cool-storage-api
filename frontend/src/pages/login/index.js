import React, { useState, useEffect } from 'react';
import { seafileAPI } from '../../utils/seafile-api';
import { setReturnURL } from '../../utils/auth-state';
import { siteTitle, loginBGPath } from '../../utils/constants';
import './login.css';

function LoginPage() {
  const [error, setError] = useState('');
  const [ssoLoading, setSsoLoading] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);

  // Check if OIDC is enabled
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
        // No browser SSO available.
        setOidcEnabled(false);
      });
  }, []);

  const handleSSOLogin = async () => {
    setError('');
    setSsoLoading(true);

    try {
      // Store return URL for after SSO. setReturnURL only accepts site-relative
      // paths, which blocks open redirects while preserving valid deep links.
      const returnURL = new URLSearchParams(window.location.search).get('next') || '/';
      setReturnURL(returnURL);

      // Get OIDC login URL
      const redirectURI = window.location.origin + '/sso/';
      const resp = await seafileAPI.getOIDCLoginURL(redirectURI, returnURL);

      if (resp.data && resp.data.authorization_url) {
        // Redirect to OIDC provider
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
              <h2>Continue with SesameDisk account</h2>
            </div>
          </div>

          {hasError && (
            <div className="login-error" role="alert">
              {error}
            </div>
          )}

          {oidcEnabled ? (
            <div className="login-sso">
              <button
                type="button"
                className="login-submit"
                onClick={handleSSOLogin}
                disabled={ssoLoading}
              >
                {ssoLoading ? 'Redirecting...' : 'Continue with SSO'}
              </button>
              <p className="login-note">
                You will be redirected to the Accounts identity provider and then returned here.
              </p>
            </div>
          ) : (
            <div className="login-note login-note--warning">
              SSO is not available in this environment, and password login is intentionally disabled for now.
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

export default LoginPage;
