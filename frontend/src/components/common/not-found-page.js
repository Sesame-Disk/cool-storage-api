import React from 'react';
import PropTypes from 'prop-types';
import { siteRoot } from '../../utils/constants';

function getText(message) {
    if (typeof window !== 'undefined' && typeof window.gettext === 'function') {
        return window.gettext(message);
    }
    return message;
}

function goBack() {
    if (typeof window === 'undefined') {
        return;
    }
    if (window.history.length > 1) {
        window.history.back();
        return;
    }
    window.location.href = siteRoot || '/';
}

function NotFoundPage({
    title,
    message,
    primaryHref,
    primaryLabel,
    secondaryLabel,
    secondaryOnClick,
}) {
    return (
        <div
            className="d-flex align-items-center justify-content-center w-100"
            style={{
                flex: 1,
                minHeight: 0,
                padding: '2rem',
            }}
        >
            <div className="w-100" style={{ maxWidth: '34rem' }}>
                <div
                    className="text-center"
                    style={{
                        minHeight: '22rem',
                        padding: '2.25rem 2rem',
                        background: '#fff',
                        border: '1px solid rgba(24, 32, 51, 0.08)',
                        borderRadius: '20px',
                        boxShadow: '0 18px 50px rgba(21, 31, 53, 0.1)',
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        justifyContent: 'center',
                    }}
                >
                    <div
                        className="mb-3"
                        style={{
                            fontSize: '4.5rem',
                            fontWeight: 700,
                            lineHeight: 1,
                            color: '#2d6bff',
                        }}
                    >
                        404
                    </div>
                    <h3 className="sf-heading mb-3" style={{ fontSize: '2rem', lineHeight: 1.1 }}>{title}</h3>
                    <p className="text-secondary mb-4" style={{ maxWidth: '28rem', fontSize: '1rem', lineHeight: 1.6 }}>{message}</p>
                    <div className="d-flex flex-wrap justify-content-center" style={{ gap: '0.75rem' }}>
                        <a href={primaryHref} className="btn btn-primary">{primaryLabel}</a>
                        <button type="button" className="btn btn-outline-primary" onClick={secondaryOnClick}>{secondaryLabel}</button>
                    </div>
                </div>
            </div>
        </div>
    );
}

NotFoundPage.propTypes = {
    title: PropTypes.string,
    message: PropTypes.string,
    primaryHref: PropTypes.string,
    primaryLabel: PropTypes.string,
    secondaryLabel: PropTypes.string,
    secondaryOnClick: PropTypes.func,
};

NotFoundPage.defaultProps = {
    title: getText('Page not found'),
    message: getText('The address you requested does not map to a page in this section.'),
    primaryHref: siteRoot || '/',
    primaryLabel: getText('Back to home'),
    secondaryLabel: getText('Go back'),
    secondaryOnClick: goBack,
};

export default NotFoundPage;