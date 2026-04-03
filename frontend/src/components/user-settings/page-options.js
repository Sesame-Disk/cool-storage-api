import { siteRoot } from '../../utils/constants';

export function getSettingsPageOptions() {
    return window.app?.pageOptions || {};
}

export function getSettingsRoute(routeName, fallbackRoute, replacements = {}) {
    const backendRoutes = getSettingsPageOptions().backendRoutes || {};
    let route = backendRoutes[routeName] || fallbackRoute;

    Object.entries(replacements).forEach(([key, value]) => {
        route = route.replace(`{${key}}`, encodeURIComponent(value || ''));
    });

    return `${siteRoot}${route}`;
}