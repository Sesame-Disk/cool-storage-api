import React, { useEffect, useRef } from 'react';
import ReactDom from 'react-dom';

import 'bootstrap/dist/css/bootstrap.min.css';

import SharedLinkPasswordDialog from './components/shared-link-password-dialog';
import { Utils } from './utils/utils';
import {
    buildShareBootstrapUrl,
    ensureAppGlobals,
    fetchBootstrap,
    renderPublicBootstrapError,
} from './bootstrap/share-runtime-bootstrap';

const SHARE_MODULE_LOADERS = {
    sharedDirView: () => import('./shared-dir-view'),
    sharedFileViewMarkdown: () => import('./shared-file-view-markdown'),
    sharedFileViewText: () => import('./shared-file-view-text'),
    sharedFileViewImage: () => import('./shared-file-view-image'),
    sharedFileViewVideo: () => import('./shared-file-view-video'),
    sharedFileViewPDF: () => import('./shared-file-view-pdf'),
    sharedFileViewSVG: () => import('./shared-file-view-svg'),
    sharedFileViewAudio: () => import('./shared-file-view-audio'),
    sharedFileViewDocument: () => import('./shared-file-view-document'),
    sharedFileViewSpreadsheet: () => import('./shared-file-view-spreadsheet'),
    sharedFileViewSdoc: () => import('./shared-file-view-sdoc'),
    sharedFileViewUnknown: () => import('./shared-file-view-unknown'),
};

function renderLoading() {
    const mount = document.getElementById('wrapper');
    if (!mount) {
        return;
    }

    mount.innerHTML = `
    <div class="loading" style="min-height:100vh;">
      <div class="loading-spinner"></div>
      <span>Loading share link...</span>
    </div>`;
}

function mapErrorTitle(status, message) {
    if (status === 404) {
        return 'Not Found';
    }
    if (status === 410 && /disabled/i.test(message || '')) {
        return 'Link Disabled';
    }
    if (status === 410) {
        return 'Link Expired';
    }
    if (status === 403) {
        return 'Forbidden';
    }
    return 'Unable to load page';
}

function PublicPasswordPrompt({ token }) {
    return <SharedLinkPasswordDialog token={token} />;
}

function OnlyOfficeSharePreview({ pageOptions }) {
    const editorRef = useRef(null);
    const { fileName, fileSize, sharedBy, canDownload, downloadPath, apiJSURL, onlyOfficeConfig } = pageOptions;

    useEffect(() => {
        let cancelled = false;
        let script = document.querySelector(`script[data-onlyoffice-src="${apiJSURL}"]`);

        const initEditor = () => {
            if (cancelled || !editorRef.current) {
                return;
            }

            if (typeof window.DocsAPI === 'undefined') {
                window.setTimeout(initEditor, 100);
                return;
            }

            try {
                editorRef.current.innerHTML = '';
                // eslint-disable-next-line no-new
                new window.DocsAPI.DocEditor('oo-preview-container', onlyOfficeConfig);
            } catch (error) {
                editorRef.current.innerHTML = `<div class="editor-error"><h2>Preview unavailable</h2><p>${String(error.message || error)}</p></div>`;
            }
        };

        if (!script) {
            script = document.createElement('script');
            script.src = apiJSURL;
            script.async = true;
            script.dataset.onlyofficeSrc = apiJSURL;
            script.onload = initEditor;
            document.body.appendChild(script);
        } else {
            initEditor();
        }

        return () => {
            cancelled = true;
        };
    }, [apiJSURL, onlyOfficeConfig]);

    return (
        <div className="preview-layout">
            <div className="page-header">
                <div className="page-header-left">
                    <a href="/"><img src="/static/img/logo.png" alt="SesameFS" className="logo" /></a>
                    <div className="file-info">
                        <div className="file-name" title={fileName}>{fileName}</div>
                        <div className="shared-by">Shared by {sharedBy}</div>
                    </div>
                </div>
                <div className="page-header-right">
                    {canDownload && <a href={downloadPath} className="btn-download">Download ({Utils.bytesToSize(fileSize)})</a>}
                </div>
            </div>
            <div className="preview-container" id="oo-preview-container" ref={editorRef}>
                <div className="loading">
                    <div className="loading-spinner"></div>
                    <span>Loading document preview...</span>
                </div>
            </div>
        </div>
    );
}

async function bootstrapSharePage() {
    ensureAppGlobals();
    renderLoading();

    const data = await fetchBootstrap(buildShareBootstrapUrl());
    if (data?.bootstrapError) {
        renderPublicBootstrapError(data.message, mapErrorTitle(data.status, data.message));
        return;
    }

    if (data?.title) {
        document.title = data.title;
    }

    const pageOptions = { ...(data?.page_options || {}) };
    window.shared = window.shared || {};
    window.shared.pageOptions = pageOptions;
    window.shared.bootstrapMeta = data;

    if (pageOptions.needPassword) {
        ReactDom.render(<PublicPasswordPrompt token={pageOptions.sharedToken || pageOptions.token} />, document.getElementById('wrapper'));
        return;
    }

    if (data.render_mode === 'onlyoffice') {
        ReactDom.render(<OnlyOfficeSharePreview pageOptions={pageOptions} />, document.getElementById('wrapper'));
        return;
    }

    if (data.render_mode !== 'bundle') {
        renderPublicBootstrapError(`Unsupported share link render mode: ${data.render_mode || 'unknown'}`);
        return;
    }

    const loadModule = SHARE_MODULE_LOADERS[data.bundle];
    if (!loadModule) {
        renderPublicBootstrapError(`Unsupported share link bundle: ${data.bundle || 'unknown'}`);
        return;
    }

    await loadModule();
}

bootstrapSharePage().catch((error) => {
    renderPublicBootstrapError(error?.message || 'Failed to initialize the share link page.');
});