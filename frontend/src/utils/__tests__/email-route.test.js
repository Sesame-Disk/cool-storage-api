import { normalizeEmailRouteParam } from '../email-route';

describe('normalizeEmailRouteParam', () => {
    test('returns a raw email unchanged', () => {
        expect(normalizeEmailRouteParam('person@example.com')).toBe('person@example.com');
    });

    test('decodes a percent-encoded route email', () => {
        expect(normalizeEmailRouteParam('person%40example.com')).toBe('person@example.com');
    });

    test('leaves malformed percent sequences untouched', () => {
        expect(normalizeEmailRouteParam('person%zzexample.com')).toBe('person%zzexample.com');
    });
});