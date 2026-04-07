import React, { useEffect, useState } from 'react';
import { seafileAPI, setAuthToken } from '../../utils/seafile-api';
import { getReturnURL } from '../../utils/auth-state';
import { siteTitle } from '../../utils/constants';
import '../login/login.css';

/**
 * SSO Callback Page
 *
 * This page handles the OIDC callback after a user authenticates with the OIDC provider.
 *
 * Flow:
 * 1. User clicks "Login with SSO" on login page
 * 2. Backend returns OIDC authorization URL
 * 3. User is redirected to OIDC provider (e.g., t-accounts.sesamedisk.com)
 * 4. User authenticates and is redirected back to /sso?code=xxx&state=yyy
 * 5. This page extracts code/state, exchanges via backend API
 * 6. Backend returns session token
 * 7. Token is stored in localStorage, user is redirected to dashboard
 */
function SSOPage() {
  const [status, setStatus] = useState('processing');
  const [error, setError] = useState('');

  useEffect(() => {
    handleCallback();
  }, []);

  const handleCallback = async () => {
    try {
      // Extract code and state from URL
      const urlParams = new URLSearchParams(window.location.search);
      const code = urlParams.get('code');
      const state = urlParams.get('state');
      const errorParam = urlParams.get('error');
      const errorDescription = urlParams.get('error_description');

      // Check for error from OIDC provider
      if (errorParam) {
        setStatus('error');
        setError(errorDescription || errorParam || 'Authentication was denied');
        return;
      }

      // Validate required parameters
      if (!code || !state) {
        setStatus('error');
        setError('Missing authorization code or state. Please try logging in again.');
        return;
      }

      // Determine redirect URI (same as what was used to start the flow)
      const redirectURI = window.location.origin + '/sso/';

      // Exchange code for tokens via backend
      setStatus('exchanging');
      const response = await seafileAPI.exchangeOIDCCode(code, state, redirectURI);

      if (response.data && response.data.token) {
        // Store the session token in localStorage. Importantly, setAuthToken
        // does NOT write the sesamefs_auth cookie — the backend already set
        // it via Set-Cookie in the OIDC exchange response with the canonical
        // `email@token` format. Writing it again from JS would corrupt it.
        setAuthToken(response.data.token);

        // Store additional user info if needed
        if (response.data.email) {
          localStorage.setItem('sesamefs_user_email', response.data.email);
        }
        if (response.data.name) {
          localStorage.setItem('sesamefs_user_name', response.data.name);
        }

        setStatus('success');

        // getReturnURL reads from sessionStorage and only returns site-relative
        // paths; missing or unsafe values fall back to '/'.
        const returnURL = getReturnURL();

        // Small delay gives the browser time to persist the Set-Cookie header
        // from the exchange response before we navigate away.
        setTimeout(() => {
          window.location.href = returnURL;
        }, 1000);
      } else {
        throw new Error('Invalid response from server');
      }
    } catch (err) {
      console.error('SSO callback error:', err);
      setStatus('error');
      setError(err.response?.data?.error || err.message || 'Authentication failed. Please try again.');
    }
  };

  const handleRetry = () => {
    // Drop any half-consumed return URL and go back to login.
    try { sessionStorage.removeItem('sso_return_url'); } catch (e) { /* ignore */ }
    window.location.href = '/login/';
  };

  return (
    <div className="login-page">
      <div className="login-container">
        <div className="login-header">
          <h1>{siteTitle || 'SesameFS'}</h1>
        </div>

        <div className="sso-status" style={{ textAlign: 'center', padding: '2rem' }}>
          {status === 'processing' && (
            <>
              <div className="spinner-border text-primary" role="status">
                <span className="sr-only">Processing...</span>
              </div>
              <p style={{ marginTop: '1rem' }}>Processing authentication...</p>
            </>
          )}

          {status === 'exchanging' && (
            <>
              <div className="spinner-border text-primary" role="status">
                <span className="sr-only">Exchanging...</span>
              </div>
              <p style={{ marginTop: '1rem' }}>Verifying credentials...</p>
            </>
          )}

          {status === 'success' && (
            <>
              <div style={{ color: 'green', fontSize: '3rem' }}>&#10003;</div>
              <p style={{ marginTop: '1rem' }}>Login successful! Redirecting...</p>
            </>
          )}

          {status === 'error' && (
            <>
              <div className="login-error" style={{ marginBottom: '1rem' }}>
                {error}
              </div>
              <button
                className="btn btn-primary"
                onClick={handleRetry}
              >
                Back to Login
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export default SSOPage;
