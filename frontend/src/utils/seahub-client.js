import axios from 'axios';

export function normalizeSiteRoot(siteRoot) {
    if (!siteRoot) {
        return siteRoot;
    }

    return siteRoot.charAt(siteRoot.length - 1) === '/'
        ? siteRoot.substring(0, siteRoot.length - 1)
        : siteRoot;
}

export function initAxiosForSeahubUsage(client, { siteRoot, xcsrfHeaders, withCredentials } = {}) {
    client.server = normalizeSiteRoot(siteRoot);
    client.req = axios.create({
        headers: {
            'X-CSRFToken': xcsrfHeaders,
        },
        ...(typeof withCredentials === 'boolean' ? { withCredentials } : {}),
    });
    return client;
}