import React from 'react';
import ReactDOM from 'react-dom';

import 'bootstrap/dist/css/bootstrap.min.css';

function gettext(message) {
    if (typeof window !== 'undefined' && typeof window.gettext === 'function') {
        return window.gettext(message);
    }
    return message;
}

function getErrorState() {
    const params = new URLSearchParams(window.location.search);
    const status = params.get('status') || '500';
    const title = params.get('title') || gettext('Something went wrong');
    const message = params.get('message') || gettext('We could not complete this request.');
    return { status, title, message };
}

function FileErrorPage() {
    const { status, title, message } = getErrorState();
    const showRetry = status.startsWith('5');

    React.useEffect(() => {
        document.title = `${title} - SesameFS`;
    }, [title]);

    return (
        <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1.5rem', background: 'linear-gradient(180deg, #16203a, #11182a)' }}>
            <div style={{ width: '100%', maxWidth: '34rem', borderRadius: '20px', padding: '2.25rem 2rem', background: 'rgba(18, 24, 40, 0.94)', border: '1px solid rgba(255, 255, 255, 0.1)', boxShadow: '0 18px 50px rgba(3, 6, 14, 0.3)', color: '#eef2f8', textAlign: 'center' }}>
                <p style={{ fontSize: '5rem', fontWeight: 700, lineHeight: 1, margin: '0 0 1rem', color: '#ff855f' }}>{status}</p>
                <h1 style={{ fontSize: '1.8rem', lineHeight: 1.1, margin: '0 0 0.8rem', fontWeight: 650 }}>{title}</h1>
                <p style={{ margin: '0 0 1.6rem', color: 'rgba(238, 242, 248, 0.74)', lineHeight: 1.6 }}>{message}</p>
                <div style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'center', gap: '0.75rem' }}>
                    {showRetry && (
                        <button type="button" className="btn btn-sm" onClick={() => window.location.reload()} style={{ minHeight: '2.8rem', borderRadius: '999px', padding: '0.75rem 1.15rem', color: '#fff', background: '#ff855f', fontWeight: 600 }}>
                            {gettext('Retry')}
                        </button>
                    )}
                    <button type="button" className="btn btn-sm" onClick={() => (window.history.length > 1 ? window.history.back() : window.location.assign('/'))} style={{ minHeight: '2.8rem', borderRadius: '999px', padding: '0.75rem 1.15rem', color: '#eef2f8', background: 'rgba(255, 255, 255, 0.06)', border: '1px solid rgba(255, 255, 255, 0.14)', fontWeight: 600 }}>
                        {gettext('Go back')}
                    </button>
                    <a href="/" className="btn btn-sm" style={{ minHeight: '2.8rem', borderRadius: '999px', padding: '0.75rem 1.15rem', color: '#eef2f8', background: 'rgba(255, 255, 255, 0.06)', border: '1px solid rgba(255, 255, 255, 0.14)', fontWeight: 600 }}>
                        {gettext('Back to home')}
                    </a>
                </div>
            </div>
        </div>
    );
}

ReactDOM.render(<FileErrorPage />, document.getElementById('wrapper'));