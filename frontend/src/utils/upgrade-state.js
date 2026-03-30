const HARD_PROFILE_LIMITS = Object.freeze({
    maxShareLinks: 3,
    maxUploadLinks: 1,
    shareLinkExpireDaysMax: 3,
    uploadLinkExpireDaysMax: 3,
});

function getPageOptions() {
    return window.app?.pageOptions || {};
}

export function getUpgradeState() {
    const pageOptions = getPageOptions();
    const upgradeFeatures = Array.isArray(pageOptions.upgradeFeatures) ? pageOptions.upgradeFeatures : [];
    const storageInfo = pageOptions.storageInfo || {};
    const trafficInfo = pageOptions.trafficInfo || {};

    const canUpgrade = pageOptions.canUpgrade === true;
    const isOrgOwner = pageOptions.isOrgOwner === true;
    const maxUsers = Number(pageOptions.maxUsers) || 0;
    const currentUsers = Number(pageOptions.currentUsers) || 0;
    const storageOverQuota = storageInfo.over_quota === true;
    const trafficOverQuota = trafficInfo.over_quota === true;
    const hasLockedFeatures = upgradeFeatures.length > 0;

    return {
        canUpgrade,
        isOrgOwner,
        maxUsers,
        currentUsers,
        upgradeFeatures,
        hasLockedFeatures,
        isSingleMemberPlan: maxUsers === 1,
        isFeatureLockedOwner: canUpgrade && isOrgOwner && hasLockedFeatures,
        isPaidOwnerUpgradeCandidate: canUpgrade && isOrgOwner && !hasLockedFeatures,
        storageInfo,
        trafficInfo,
        storageOverQuota,
        trafficOverQuota,
        overQuota: storageOverQuota || trafficOverQuota,
        trafficResetDate: trafficInfo.reset_date || null,
        shareLinkExpireDaysMax: Number(pageOptions.shareLinkExpireDaysMax) || 0,
        uploadLinkExpireDaysMax: Number(pageOptions.uploadLinkExpireDaysMax) || 0,
    };
}

export function hasUpgradeFeatures() {
    return getUpgradeState().hasLockedFeatures;
}

export function featureRequiresUpgrade(featureKey) {
    if (!featureKey) {
        return hasUpgradeFeatures();
    }

    return getUpgradeState().upgradeFeatures.includes(featureKey);
}

export function getHardPlanLimits() {
    return HARD_PROFILE_LIMITS;
}