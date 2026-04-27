const SESAME_AI_WIDGET_ID = 'sesame-ai-widget';
const SESAME_AI_WIDGET_SRC = 'https://ai.sesamedisk.com/widgets/widget.js?v=1.1.1';

export function getSesameAiWidgetUser({ pageOptions, bootstrapPageOptions } = {}) {
    const resolvedPageOptions = pageOptions || window.app?.pageOptions || {};
    const resolvedBootstrapPageOptions = bootstrapPageOptions || window.__SESAMEFS_BOOTSTRAP__?.app_page_options || {};
    const email = resolvedPageOptions.contactEmail || resolvedPageOptions.username || resolvedBootstrapPageOptions.contactEmail || resolvedBootstrapPageOptions.username || '';
    const name = resolvedPageOptions.name || resolvedBootstrapPageOptions.name || '';
    const role = resolvedPageOptions.userRole || resolvedBootstrapPageOptions.userRole || resolvedBootstrapPageOptions.role || 'member';

    if (!email && !name) {
        return null;
    }

    return { email, name, role };
}

export function syncSesameAiWidget({ isAuthenticated = false, pageOptions, bootstrapPageOptions } = {}) {
    if (typeof window === 'undefined' || typeof document === 'undefined') {
        return;
    }

    const user = isAuthenticated ? getSesameAiWidgetUser({ pageOptions, bootstrapPageOptions }) : null;

    window.billingConfig = window.billingConfig || {};
    window.billingConfig.dataUser = user || {};

    if (!document.getElementById(SESAME_AI_WIDGET_ID)) {
        const script = document.createElement('script');
        script.id = SESAME_AI_WIDGET_ID;
        script.src = SESAME_AI_WIDGET_SRC;
        script.defer = true;
        document.body.appendChild(script);
    }
}