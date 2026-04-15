import React, { useEffect, useState } from 'react';
import { seafileAPI } from '../../utils/seafile-api';
import { clearReturnURL, redirectToLogin, setReturnURL } from '../../utils/auth-state';
import { siteTitle } from '../../utils/constants';
import '../login/login.css';

function LoginSSOPage() {
    const [error, setError] = useState('');

    useEffect(() => {
        let cancelled = false;

        const startSSO = async () => {
            const next = new URLSearchParams(window.location.search).get('next') || '/';
            setReturnURL(next);

            try {
                await seafileAPI.invalidateSession();

                const redirectURI = window.location.origin + '/sso/';
                const resp = await seafileAPI.getOIDCLoginURL(redirectURI, next);
                const authorizationURL = resp?.data?.authorization_url;
                if (!authorizationURL) {
                    throw new Error('Failed to get SSO login URL');
                }

                window.location.replace(authorizationURL);
            } catch (err) {
                if (cancelled) {
                    return;
                }
                setError(err.response?.data?.error || err.message || 'SSO login failed. Please try again.');
            }
        };

        startSSO();

        return () => {
            cancelled = true;
        };
    }, []);

    return (
        <div className="login-page">
            <div className="login-container">
                <div className="login-header">
                    <h1>{siteTitle || 'SesameFS'}</h1>
                </div>

                <div className="sso-status" style={{ textAlign: 'center', padding: '2rem' }}>
                    {error ? (
                        <>
                            <div className="login-error" style={{ marginBottom: '1rem' }}>
                                {error}
                            </div>
                            <button className="btn btn-primary" onClick={() => {
                                clearReturnURL();
                                redirectToLogin('required', '/');
                            }}>
                                Back to Login
                            </button>
                        </>
                    ) : (
                        <>
                            <div className="spinner-border text-primary" role="status">
                                <span className="sr-only">Redirecting...</span>
                            </div>
                            <p style={{ marginTop: '1rem' }}>Redirecting to SSO...</p>
                        </>
                    )}
                </div>
            </div>
        </div>
    );
}

export default LoginSSOPage;
