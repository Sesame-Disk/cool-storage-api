import { getSesameAiWidgetUser, syncSesameAiWidget } from '../sesame-ai-widget';

describe('sesame-ai-widget bootstrap helper', () => {
    beforeEach(() => {
        window.app = window.app || {};
        window.app.pageOptions = {
            name: '',
            username: '',
            contactEmail: '',
            userRole: '',
        };
        window.billingConfig = undefined;
        window.__SESAMEFS_BOOTSTRAP__ = undefined;
    });

    afterEach(() => {
        const script = document.getElementById('sesame-ai-widget');
        if (script) {
            script.remove();
        }

        delete window.billingConfig;
        delete window.__SESAMEFS_BOOTSTRAP__;
    });

    it('prefers runtime page options and falls back to bootstrap data', () => {
        const runtimeUser = getSesameAiWidgetUser({
            pageOptions: {
                name: 'Runtime User',
                contactEmail: 'runtime@example.com',
                userRole: 'admin',
            },
            bootstrapPageOptions: {
                name: 'Bootstrap User',
                username: 'bootstrap@example.com',
                userRole: 'member',
            },
        });

        expect(runtimeUser).toEqual({
            email: 'runtime@example.com',
            name: 'Runtime User',
            role: 'admin',
        });

        const bootstrapUser = getSesameAiWidgetUser({
            pageOptions: {},
            bootstrapPageOptions: {
                name: 'Bootstrap User',
                username: 'bootstrap@example.com',
                userRole: 'member',
            },
        });

        expect(bootstrapUser).toEqual({
            email: 'bootstrap@example.com',
            name: 'Bootstrap User',
            role: 'member',
        });
    });

    it('injects the widget once and clears user data for anonymous surfaces', () => {
        window.app.pageOptions = {
            name: 'Runtime User',
            contactEmail: 'runtime@example.com',
            userRole: 'admin',
        };

        syncSesameAiWidget({ isAuthenticated: true, pageOptions: window.app.pageOptions });
        syncSesameAiWidget({ isAuthenticated: false, pageOptions: window.app.pageOptions });

        expect(document.querySelectorAll('#sesame-ai-widget')).toHaveLength(1);
        expect(window.billingConfig.dataUser).toEqual({});
    });
});