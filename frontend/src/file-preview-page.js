import React, { useEffect, useMemo, useState } from 'react';
import ReactDOM from 'react-dom';

import 'bootstrap/dist/css/bootstrap.min.css';

import { getAuthToken, isAuthenticated, redirectToLogin } from './utils/auth-state';
import PDFViewer from './components/pdf-viewer';

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'svg', 'ico', 'tiff', 'tif']);
const VIDEO_EXTENSIONS = new Set(['mp4', 'webm', 'ogg', 'mov']);
const AUDIO_EXTENSIONS = new Set(['mp3', 'wav', 'flac', 'aac']);
const TEXT_EXTENSIONS = new Set([
    'txt', 'md', 'markdown', 'json', 'yaml', 'yml', 'xml', 'csv',
    'html', 'htm', 'css', 'js', 'ts', 'jsx', 'tsx',
    'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'hpp',
    'sh', 'bash', 'zsh', 'fish',
    'toml', 'ini', 'cfg', 'conf', 'env',
    'sql', 'graphql', 'proto',
    'dockerfile', 'makefile',
    'rb', 'php', 'swift', 'kt', 'scala', 'r', 'lua', 'pl',
    'log', 'diff', 'patch',
]);

function gettext(message) {
    if (typeof window !== 'undefined' && typeof window.gettext === 'function') {
        return window.gettext(message);
    }
    return message;
}

function encodePath(path) {
    if (!path) {
        return '';
    }
    return path.split('/').map((segment) => encodeURIComponent(segment)).join('/');
}

function getFileName(filePath) {
    if (!filePath) {
        return '';
    }

    const parts = filePath.split('/').filter(Boolean);
    return parts.length ? parts[parts.length - 1] : '';
}

function getFileExtension(fileName) {
    const lastDot = fileName.lastIndexOf('.');
    if (lastDot === -1) {
        return '';
    }
    return fileName.slice(lastDot + 1).toLowerCase();
}

function getQueryState() {
    const params = new URLSearchParams(window.location.search);
    const repoID = params.get('repo_id') || '';
    const filePath = params.get('p') || '';
    const objectID = params.get('obj_id') || '';
    return { repoID, filePath, objectID };
}

function buildRawURL({ repoID, filePath, objectID, token }) {
    if (objectID) {
        const params = new URLSearchParams({ obj_id: objectID, p: filePath });
        if (token) {
            params.set('token', token);
        }
        return `/repo/${repoID}/history/raw?${params.toString()}`;
    }

    let url = `/repo/${repoID}/raw${encodePath(filePath)}`;
    if (token) {
        url += `?token=${encodeURIComponent(token)}`;
    }
    return url;
}

function buildDownloadURL({ repoID, filePath, objectID, token }) {
    if (objectID) {
        const params = new URLSearchParams({ obj_id: objectID, p: filePath });
        if (token) {
            params.set('token', token);
        }
        return `/repo/${repoID}/history/download?${params.toString()}`;
    }

    let url = `/lib/${repoID}/file${encodePath(filePath)}?dl=1`;
    if (token) {
        url += `&token=${encodeURIComponent(token)}`;
    }
    return url;
}

function renderCenteredMessage(title, message) {
    return (
        <div className="auth-card">
            <h1>{title}</h1>
            <p>{message}</p>
        </div>
    );
}

function FilePreviewPage() {
    const [{ repoID, filePath, objectID }] = useState(() => getQueryState());
    const [textContent, setTextContent] = useState(null);
    const [textLoading, setTextLoading] = useState(false);
    const [textError, setTextError] = useState('');

    const token = useMemo(() => getAuthToken(), []);
    const fileName = useMemo(() => getFileName(filePath), [filePath]);
    const extension = useMemo(() => getFileExtension(fileName), [fileName]);
    const rawURL = useMemo(() => buildRawURL({ repoID, filePath, objectID, token }), [repoID, filePath, objectID, token]);
    const downloadURL = useMemo(() => buildDownloadURL({ repoID, filePath, objectID, token }), [repoID, filePath, objectID, token]);
    const isHistoric = !!objectID;

    useEffect(() => {
        document.title = fileName ? `${fileName} - SesameFS` : 'SesameFS';
    }, [fileName]);

    useEffect(() => {
        if (!isAuthenticated()) {
            redirectToLogin('required');
        }
    }, []);

    useEffect(() => {
        if (!TEXT_EXTENSIONS.has(extension)) {
            return undefined;
        }

        let cancelled = false;
        setTextLoading(true);
        setTextError('');
        setTextContent(null);

        fetch(rawURL, { cache: 'no-cache' })
            .then((response) => {
                if (response.status === 401) {
                    redirectToLogin('expired');
                    return Promise.reject(new Error('unauthorized'));
                }
                if (!response.ok) {
                    throw new Error(`HTTP ${response.status}`);
                }
                return response.text();
            })
            .then((text) => {
                if (!cancelled) {
                    setTextContent(text);
                    setTextLoading(false);
                }
            })
            .catch((error) => {
                if (!cancelled && error.message !== 'unauthorized') {
                    setTextError(error.message);
                    setTextLoading(false);
                }
            });

        return () => {
            cancelled = true;
        };
    }, [extension, rawURL]);

    if (!repoID || !filePath || !fileName) {
        return renderCenteredMessage(gettext('Preview unavailable'), gettext('The file preview request is missing required information.'));
    }

    let content = (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', color: '#666' }}>
            <p>{gettext('Preview not available for this file type.')}</p>
        </div>
    );

    if (extension === 'pdf') {
        content = <PDFViewer src={rawURL} title={fileName} />;
    } else if (IMAGE_EXTENSIONS.has(extension)) {
        content = (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', padding: '20px', overflow: 'auto' }}>
                <img src={rawURL} alt={fileName} style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }} />
            </div>
        );
    } else if (VIDEO_EXTENSIONS.has(extension)) {
        content = (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', background: '#000' }}>
                <video controls style={{ maxWidth: '100%', maxHeight: '100%' }} src={rawURL}>
                    {gettext('Your browser does not support video playback.')}
                </video>
            </div>
        );
    } else if (AUDIO_EXTENSIONS.has(extension)) {
        content = (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', background: '#f8f9fa' }}>
                <audio controls style={{ width: '80%', maxWidth: '600px' }} src={rawURL}>
                    {gettext('Your browser does not support audio playback.')}
                </audio>
            </div>
        );
    } else if (TEXT_EXTENSIONS.has(extension)) {
        content = (
            <div style={{ height: '100%', overflow: 'auto', background: '#1e1e1e', padding: 0 }}>
                <pre style={{ margin: 0, padding: '20px', color: '#d4d4d4', fontFamily: "'SF Mono', Monaco, 'Cascadia Code', 'Roboto Mono', Consolas, 'Courier New', monospace", fontSize: '13px', lineHeight: 1.6, tabSize: 4, whiteSpace: 'pre-wrap', wordWrap: 'break-word' }}>
                    <code>
                        {textLoading ? gettext('Loading...') : textError ? `${gettext('Failed to load file')}: ${textError}` : textContent}
                    </code>
                </pre>
            </div>
        );
    }

    return (
        <div className="preview-layout">
            <div className="page-header">
                <div className="page-header-left">
                    <a href="/"><img src="/static/img/logo.png" alt="SesameFS" className="logo" onError={(event) => { event.currentTarget.style.display = 'none'; }} /></a>
                    <div className="file-info">
                        <div className="file-name" title={fileName}>{fileName}</div>
                        {isHistoric && <div className="shared-by">{gettext('Historic version preview')}</div>}
                    </div>
                </div>
                <div className="page-header-right" style={{ display: 'flex', gap: '0.75rem', alignItems: 'center' }}>
                    <button type="button" className="btn btn-outline-secondary btn-sm" onClick={() => window.history.back()}>{gettext('Back')}</button>
                    <a href={downloadURL} className="btn-download">{gettext('Download')}</a>
                </div>
            </div>
            <div className="preview-container">
                {content}
            </div>
        </div>
    );
}

ReactDOM.render(<FilePreviewPage />, document.getElementById('wrapper'));