import { siteRoot } from './constants';

function getBootstrapInlinePreviewExtensions() {
    const values = window?.app?.pageOptions?.inlinePreviewExtensions;
    if (!Array.isArray(values)) {
        return [];
    }

    return values
        .map((value) => (typeof value === 'string' ? value.trim().toLowerCase() : ''))
        .filter(Boolean);
}

function encodePath(path) {
    if (!path) {
        return '';
    }

    return path.split('/').map(segment => encodeURIComponent(segment)).join('/');
}

function getExtension(filePath) {
    if (!filePath) {
        return '';
    }

    const fileName = filePath.split('/').pop() || '';
    const idx = fileName.lastIndexOf('.');
    if (idx === -1) {
        return '';
    }

    return fileName.substring(idx + 1).toLowerCase();
}

export function isInlinePreviewableFile(filePath) {
    const extension = getExtension(filePath);
    if (!extension) {
        return false;
    }

    return getBootstrapInlinePreviewExtensions().includes(extension);
}

export function buildFrontendFilePreviewURL({ repoID, filePath, objID }) {
    let url = `${siteRoot}file-preview/?repo_id=${encodeURIComponent(repoID)}&p=${encodeURIComponent(filePath)}`;
    if (objID) {
        url += `&obj_id=${encodeURIComponent(objID)}`;
    }
    return url;
}

export function buildFileViewURL({ repoID, filePath }) {
    if (isInlinePreviewableFile(filePath)) {
        return buildFrontendFilePreviewURL({ repoID, filePath });
    }

    return `${siteRoot}lib/${repoID}/file${encodePath(filePath)}`;
}

export function buildHistoricFileViewURL({ repoID, filePath, objID }) {
    if (isInlinePreviewableFile(filePath)) {
        return buildFrontendFilePreviewURL({ repoID, filePath, objID });
    }

    return `${siteRoot}repo/${repoID}/history/view?obj_id=${encodeURIComponent(objID)}&p=${encodeURIComponent(filePath)}`;
}
