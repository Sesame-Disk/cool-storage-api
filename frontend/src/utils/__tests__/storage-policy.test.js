import {
    buildStorageRegionOptions,
    formatStoragePolicyLabel,
    getStorageRegionLabel,
} from '../storage-policy';

describe('storage policy helper', () => {
    test('uses configured region labels when formatting a policy summary', () => {
        const result = formatStoragePolicyLabel({
            data_residency: 'strict',
            default_region: 'na',
        }, {
            na: 'North America',
        }, (value) => value);

        expect(result).toBe('Strict (North America)');
    });

    test('builds select options with user-facing labels', () => {
        expect(buildStorageRegionOptions(['na', 'eu'], {
            na: 'North America',
            eu: 'Europe',
        })).toEqual([
            { value: 'na', label: 'North America' },
            { value: 'eu', label: 'Europe' },
        ]);
    });

    test('falls back to the raw region when no label is configured', () => {
        expect(getStorageRegionLabel('apac', {})).toBe('apac');
    });
});