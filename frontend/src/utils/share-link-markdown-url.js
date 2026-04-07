import { siteRoot, serviceURL } from './constants';
import { Utils } from './utils';

function decodePath(value) {
    try {
        return decodeURIComponent(value);
    } catch (error) {
        return value;
    }
}

function normalizeSharePath(filePath) {
    const normalized = Utils.pathNormalize(filePath || '/');
    return normalized ? `/${normalized}` : '/';
}

function resolveShareRelativePath(currentFilePath, targetPath) {
    if (!targetPath) {
        return '';
    }

    if (targetPath.startsWith('/')) {
        return normalizeSharePath(targetPath);
    }

    const currentDir = currentFilePath && currentFilePath.includes('/')
        ? currentFilePath.slice(0, currentFilePath.lastIndexOf('/') + 1)
        : '/';
    return normalizeSharePath(`${currentDir}${targetPath}`);
}

function buildSharedFileURL(sharedToken, filePath, hash = '') {
    return `${siteRoot}d/${encodeURIComponent(sharedToken)}/files/?p=${encodeURIComponent(normalizeSharePath(filePath))}${hash}`;
}

function buildSharedDirURL(sharedToken, dirPath, hash = '') {
    return `${siteRoot}d/${encodeURIComponent(sharedToken)}/?p=${encodeURIComponent(normalizeSharePath(dirPath))}${hash}`;
}

function buildSharedImageProxyURL(sharedToken, imagePath) {
    return `${serviceURL}/view-image-via-share-link/?token=${encodeURIComponent(sharedToken)}&path=${encodeURIComponent(normalizeSharePath(imagePath))}`;
}

function splitHash(value) {
    const hashIndex = value.indexOf('#');
    if (hashIndex === -1) {
        return { path: value, hash: '' };
    }

    return {
        path: value.slice(0, hashIndex),
        hash: value.slice(hashIndex),
    };
}

function isExternalTarget(target) {
    return /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(target);
}

function extractSmartLinkToken(target) {
    const normalizedTarget = target || '';
    const prefixes = [serviceURL, getAppOrigin(), ''].filter((prefix, index, list) => prefix !== undefined && list.indexOf(prefix) === index);

    for (const prefix of prefixes) {
        const candidatePrefix = `${prefix}/smart-link/`;
        if (normalizedTarget.startsWith(candidatePrefix)) {
            const token = normalizedTarget.slice(candidatePrefix.length).split(/[/?#]/)[0];
            return token ? decodePath(token) : '';
        }
    }

    return '';
}

function getAppOrigin() {
    if (typeof window === 'undefined' || !window.location?.origin) {
        return '';
    }

    return window.location.origin;
}

function getInternalRepoPath(target, repoID, kind) {
    const normalizedTarget = target || '';
    const prefixes = [serviceURL, getAppOrigin(), ''].filter((prefix, index, list) => prefix !== undefined && list.indexOf(prefix) === index);
    const basePath = kind === 'file' ? `/lib/${repoID}/file` : `/library/${repoID}`;

    for (const prefix of prefixes) {
        const candidatePrefix = `${prefix}${basePath}`;
        if (normalizedTarget.startsWith(candidatePrefix)) {
            return decodePath(normalizedTarget.slice(candidatePrefix.length));
        }
    }

    return '';
}

function normalizeInternalDirPath(dirPath) {
    const normalized = decodePath(dirPath || '');
    if (!normalized || normalized === '/') {
        return '/';
    }

    const segments = normalized.split('/').filter(Boolean);
    if (segments.length <= 1) {
        return '/';
    }

    return normalizeSharePath(`/${segments.slice(1).join('/')}`);
}

function rewriteSharedLinkTarget(target, context) {
    const { repoID, sharedToken, currentFilePath, smartLinkMap } = context;
    const { path, hash } = splitHash(target || '');

    if (!path || path.startsWith('#')) {
        return null;
    }

    const internalDirPath = getInternalRepoPath(path, repoID, 'dir');
    if (internalDirPath) {
        return buildSharedDirURL(sharedToken, normalizeInternalDirPath(internalDirPath), hash);
    }

    const internalFilePath = getInternalRepoPath(path, repoID, 'file');
    if (internalFilePath) {
        return buildSharedFileURL(sharedToken, internalFilePath, hash);
    }

    const smartLinkToken = extractSmartLinkToken(path);
    if (smartLinkToken) {
        const smartLinkTarget = smartLinkMap?.[smartLinkToken];
        if (!smartLinkTarget?.path) {
            return null;
        }

        if (smartLinkTarget.isDir) {
            return buildSharedDirURL(sharedToken, smartLinkTarget.path, hash);
        }

        return buildSharedFileURL(sharedToken, smartLinkTarget.path, hash);
    }

    if (isExternalTarget(path)) {
        return null;
    }

    const resolvedPath = resolveShareRelativePath(currentFilePath, path);
    if (!resolvedPath) {
        return null;
    }

    if (path.endsWith('/')) {
        return buildSharedDirURL(sharedToken, resolvedPath, hash);
    }

    return buildSharedFileURL(sharedToken, resolvedPath, hash);
}

export function rewriteSharedMarkdownNode(node, context) {
    if (!node) {
        return node;
    }

    if (node.type === 'image' && node.data?.src) {
        const imageTarget = node.data.src;
        let imagePath = '';

        imagePath = getInternalRepoPath(splitHash(imageTarget).path, context.repoID, 'file');
        if (imagePath) {
            imagePath = normalizeSharePath(imagePath);
        } else if (!imageTarget.startsWith('#') && !isExternalTarget(imageTarget)) {
            imagePath = resolveShareRelativePath(context.currentFilePath, splitHash(imageTarget).path);
        }

        if (imagePath) {
            node.data.src = buildSharedImageProxyURL(context.sharedToken, imagePath);
        }
        return node;
    }

    if (node.type === 'link' && node.url) {
        const rewritten = rewriteSharedLinkTarget(node.url, context);
        if (rewritten) {
            node.url = rewritten;
        }
    }

    return node;
}

export const shareLinkMarkdownUrlHelpers = {
    buildSharedDirURL,
    buildSharedFileURL,
    buildSharedImageProxyURL,
    normalizeSharePath,
    resolveShareRelativePath,
};