const normalizeRegionKey = (region) => {
    if (typeof region !== 'string') {
        return '';
    }
    return region.trim().toLowerCase();
};

export const getStorageRegionLabel = (region, regionLabels = {}) => {
    const regionKey = normalizeRegionKey(region);
    if (!regionKey) {
        return '';
    }

    const configuredLabel = regionLabels && typeof regionLabels === 'object' ? regionLabels[regionKey] : '';
    if (typeof configuredLabel === 'string' && configuredLabel.trim()) {
        return configuredLabel.trim();
    }

    return region;
};

export const buildStorageRegionOptions = (availableRegions = [], regionLabels = {}) => {
    if (!Array.isArray(availableRegions)) {
        return [];
    }

    return availableRegions
        .map((region) => {
            const value = normalizeRegionKey(region);
            if (!value) {
                return null;
            }
            return {
                value,
                label: getStorageRegionLabel(value, regionLabels),
            };
        })
        .filter(Boolean);
};

export const formatStoragePolicyLabel = (policy, regionLabels = {}, translate = (value) => value) => {
    const effectivePolicy = policy || {};
    const dataResidency = effectivePolicy.data_residency === 'strict' ? 'strict' : 'flexible';
    const defaultRegion = normalizeRegionKey(effectivePolicy.default_region);
    const residencyLabel = dataResidency === 'strict' ? translate('Strict') : translate('Flexible');

    if (!defaultRegion) {
        return residencyLabel;
    }

    return `${residencyLabel} (${getStorageRegionLabel(defaultRegion, regionLabels)})`;
};