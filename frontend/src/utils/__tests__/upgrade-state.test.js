import { featureRequiresUpgrade, getHardPlanLimits, getUpgradeState, hasUpgradeFeatures } from '../upgrade-state';

describe('upgrade-state', () => {
    const originalApp = window.app;

    afterEach(() => {
        window.app = originalApp;
    });

    test('reads live upgrade state from window.app.pageOptions', () => {
        window.app = {
            pageOptions: {
                canUpgrade: true,
                isOrgOwner: true,
                upgradeFeatures: ['add_group', 'invite_guest'],
                storageInfo: { over_quota: false },
                trafficInfo: { over_quota: true, reset_date: '2026-04-14' },
                shareLinkExpireDaysMax: 3,
                uploadLinkExpireDaysMax: 3,
            },
        };

        expect(getUpgradeState()).toEqual(expect.objectContaining({
            canUpgrade: true,
            isOrgOwner: true,
            hasLockedFeatures: true,
            isFeatureLockedOwner: true,
            trafficOverQuota: true,
            overQuota: true,
            trafficResetDate: '2026-04-14',
            shareLinkExpireDaysMax: 3,
            uploadLinkExpireDaysMax: 3,
        }));
    });

    test('feature helpers use live page options instead of frozen module state', () => {
        window.app = { pageOptions: { upgradeFeatures: ['generate_share_link'] } };
        expect(hasUpgradeFeatures()).toBe(true);
        expect(featureRequiresUpgrade('generate_share_link')).toBe(true);
        expect(featureRequiresUpgrade('add_group')).toBe(false);

        window.app = { pageOptions: { upgradeFeatures: [] } };
        expect(hasUpgradeFeatures()).toBe(false);
        expect(featureRequiresUpgrade('generate_share_link')).toBe(false);
    });

    test('hard plan limits stay aligned with the current backend hard profile', () => {
        expect(getHardPlanLimits()).toEqual({
            maxShareLinks: 3,
            maxUploadLinks: 1,
            shareLinkExpireDaysMax: 3,
            uploadLinkExpireDaysMax: 3,
        });
    });
});