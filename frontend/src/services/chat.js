/* eslint-disable */
import React, { useEffect, useRef, useState } from 'react';

/** Small People & Presence list (loads when mounted) */
function ZulipChat({ endpoint = '/api/chat/people', title = 'People & Presence', onClose, notifications }) {
    const [people, setPeople] = useState(null); // null = loading state before first fetch
    const [loading, setLoading] = useState(true);
    const [err, setErr] = useState('');
    const currentUsername = window.app?.pageOptions?.username || '';
    const notificationCount = notifications?.unread_count || 0;

    useEffect(() => {
        let cancelled = false;
        (async () => {
            try {
                setLoading(true);
                setErr('');
                const res = await fetch(endpoint, { credentials: 'same-origin' });
                if (!res.ok) throw new Error(`Error ${res.status}`);
                const data = await res.json();
                if (!cancelled) setPeople(Array.isArray(data) ? data : []);
            } catch (e) {
                if (!cancelled) {
                    setErr(e.message || 'Could not load the list.');
                    setPeople([]);
                }
            } finally {
                if (!cancelled) setLoading(false);
            }
        })();
        return () => { cancelled = true; };
    }, [endpoint]);

    const statusLabel = {
        online: 'Online',
        idle: 'Idle',
        offline: 'Offline',
        unknown: 'Invisible',
    };

    return (
        <div className="zulip-chat">
            <div className="zulip-chat__header">
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <span>{title}</span>
                    {notificationCount > 0 && (
                        <div
                            style={{ position: 'relative', display: 'inline-block' }}
                            title={`${notificationCount} unread notification${notificationCount > 1 ? 's' : ''}`}
                        >
                            <svg
                                style={{ width: '18px', height: '18px', color: '#4b5563' }}
                                fill="currentColor"
                                viewBox="0 0 20 20"
                            >
                                <path d="M10 2a6 6 0 00-6 6v3.586l-.707.707A1 1 0 004 14h12a1 1 0 00.707-1.707L16 11.586V8a6 6 0 00-6-6zM10 18a3 3 0 01-3-3h6a3 3 0 01-3 3z" />
                            </svg>
                            <span
                                style={{
                                    position: 'absolute',
                                    top: '-4px',
                                    right: '-4px',
                                    backgroundColor: '#ef4444',
                                    color: 'white',
                                    fontSize: '9px',
                                    borderRadius: '9999px',
                                    minWidth: '14px',
                                    height: '14px',
                                    display: 'flex',
                                    alignItems: 'center',
                                    justifyContent: 'center',
                                    fontWeight: 'bold',
                                    padding: '0 3px'
                                }}
                            >
                                {notificationCount > 9 ? '9+' : notificationCount}
                            </span>
                        </div>
                    )}
                </div>
                <div className="zulip-chat__header-actions">
                    <button
                        className="zulip-chat__btn"
                        onClick={() => {
                            // manual refresh
                            setPeople(null);
                            setLoading(true);
                            // trigger effect by nudging endpoint state? simple re-run:
                            // call the same logic again:
                            fetch(endpoint, { credentials: 'same-origin' })
                                .then((r) => {
                                    if (!r.ok) throw new Error(`Error ${r.status}`);
                                    return r.json();
                                })
                                .then((d) => setPeople(Array.isArray(d) ? d : []))
                                .catch((e) => setErr(e.message || 'Could not load the list.'))
                                .finally(() => setLoading(false));
                        }}
                        disabled={loading}
                    >
                        {loading ? 'Refreshing…' : 'Refresh'}
                    </button>
                    {onClose && (
                        <button
                            className="chat-close"
                            aria-label="Close"
                            onClick={onClose}
                        >
                            ×
                        </button>
                    )}
                </div>
            </div>

            <div className="zulip-chat__body">
                {loading && people === null && (
                    <div style={{ padding: 16, opacity: 0.7 }}>Loading contacts…</div>
                )}
                {!!err && <div style={{ padding: 16, color: '#b91c1c' }}>{err}</div>}
                {people && people.length === 0 && !loading && !err && (
                    <div style={{ padding: 16, opacity: 0.7 }}>No users to display.</div>
                )}

                {people && people.length > 0 && people.map((u) => {
                    const isCurrentUser = u.email.toLowerCase() === currentUsername.toLowerCase();

                    return (
                        <div className="zulip-chat__row" key={u.user_id ?? u.email}>
                            <img
                                className="zulip-chat__avatar"
                                src={u.avatar_url}
                                alt=""
                                loading="lazy"
                                onError={(e) => {
                                    e.currentTarget.src =
                                        'data:image/svg+xml;utf8,<svg xmlns=\'http://www.w3.org/2000/svg\' width=\'32\' height=\'32\'><rect width=\'100%\' height=\'100%\' fill=\'%23e5e7eb\'/></svg>';
                                }}
                            />
                            <div className="zulip-chat__meta">
                                <div className="zulip-chat__name">
                                    {u.full_name || u.email}
                                    {isCurrentUser && (
                                        <span className="zulip-chat__you-badge">You</span>
                                    )}
                                </div>
                                <div className="zulip-chat__email" title={u.email}>{u.email}</div>
                            </div>
                            <div className="zulip-chat__status">
                                <span className={`zulip-chat__dot ${u.status || 'unknown'}`} />
                                {statusLabel[u.status] || statusLabel.unknown}
                            </div>
                            <div>
                                <button
                                    className="zulip-chat__btn zulip-chat__btn--primary"
                                    onClick={() => {
                                        let targetUrl = u.dm_url;

                                        if (isCurrentUser) {
                                            // Si es el usuario actual, abrir el chat sin parámetros de DM
                                            // Eliminar query string si existe
                                            const questionMarkIndex = targetUrl.indexOf('?');
                                            if (questionMarkIndex !== -1) {
                                                targetUrl = targetUrl.substring(0, questionMarkIndex);
                                            }
                                        }

                                        window.open(targetUrl, '_blank', 'noopener,noreferrer');
                                    }}
                                >
                                    {isCurrentUser ? 'Open Chat' : 'Message'}
                                </button>
                            </div>
                        </div>
                    );
                })}
            </div>
        </div>
    );
}

/** Launcher link + drawer with outside-click & Esc close */
export default function ChatLauncher({
    endpoint = '/api/chat/people',
    linkLabel = 'Chat',
    linkClassName = '',
    showNewBadge = true,
    inSidebar = false,
}) {
    // Only render if chat is enabled
    const chatEnabled = window.app?.pageOptions?.chatEnabled;
    if (!chatEnabled) {
        return null;
    }

    const [open, setOpen] = useState(false);
    const [notifications, setNotifications] = useState(null);
    const drawerRef = useRef(null);

    // Fetch notifications function
    const fetchNotifications = async () => {
        try {
            const res = await fetch('/api/chat/notifications', { credentials: 'same-origin' });
            if (res.ok) {
                const data = await res.json();
                setNotifications(data);
            }
        } catch (e) {
            console.error('Error fetching notifications:', e);
        }
    };

    // close on Esc
    useEffect(() => {
        if (!open) return;
        const onKey = (e) => e.key === 'Escape' && setOpen(false);
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [open]);

    // close on outside click
    useEffect(() => {
        if (!open) return;
        const onDown = (e) => {
            if (drawerRef.current && !drawerRef.current.contains(e.target)) {
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', onDown);
        return () => document.removeEventListener('mousedown', onDown);
    }, [open]);

    // Fetch notifications with polling every 60 seconds
    useEffect(() => {
        fetchNotifications();
        const interval = setInterval(fetchNotifications, 60000); // Poll every 60 seconds
        return () => clearInterval(interval);
    }, []);

    const notificationCount = notifications?.unread_count || 0;

    return (
        <>
            {/* Enlace que pones junto a Billing */}
            <a
                href="#"
                className={`${linkClassName}${inSidebar ? ' chat-link-wrapper' : ''}`}
                onClick={(e) => {
                    e.preventDefault();
                    setOpen(true);
                }}
            >
                <div style={{ display: 'flex', alignItems: 'center' }}>
                    <span>{linkLabel}</span>
                    {showNewBadge && inSidebar && <span className="chat-new-badge">NEW</span>}
                    {notificationCount > 0 && (
                        <div
                            style={{ position: 'relative' }}
                            title={`${notificationCount} unread notification${notificationCount > 1 ? 's' : ''}`}
                        >
                            <svg
                                style={{ width: '20px', height: '20px', color: '#4b5563' }}
                                fill="currentColor"
                                viewBox="0 0 20 20"
                            >
                                <path d="M10 2a6 6 0 00-6 6v3.586l-.707.707A1 1 0 004 14h12a1 1 0 00.707-1.707L16 11.586V8a6 6 0 00-6-6zM10 18a3 3 0 01-3-3h6a3 3 0 01-3 3z" />
                            </svg>
                            <span
                                style={{
                                    position: 'absolute',
                                    top: '-4px',
                                    right: '-4px',
                                    backgroundColor: '#ef4444',
                                    color: 'white',
                                    fontSize: '10px',
                                    borderRadius: '9999px',
                                    minWidth: '16px',
                                    height: '16px',
                                    display: 'flex',
                                    alignItems: 'center',
                                    justifyContent: 'center',
                                    fontWeight: 'bold',
                                    padding: '0 4px'
                                }}
                            >
                                {notificationCount > 9 ? '9+' : notificationCount}
                            </span>
                        </div>
                    )}
                </div>
            </a>

            {/* Overlay + Drawer */}
            {open && (
                <>
                    <div className="chat-overlay" aria-hidden="true" />
                    <aside
                        className="chat-drawer"
                        ref={drawerRef}
                        role="dialog"
                        aria-modal="true"
                        aria-label="People & Presence"
                    >
                        {/* Lazy: el listado se monta solo cuando open === true */}
                        <ZulipChat endpoint={endpoint} onClose={() => setOpen(false)} notifications={notifications} />
                    </aside>
                </>
            )}
        </>
    );
}
