export const BYTES_IN_GB = 1000 * 1000 * 1000;

export function quotaBytesToGigabyteInput(quota) {
    if (quota === undefined || quota === null || quota <= 0) {
        return '';
    }

    return String(Math.round(quota / BYTES_IN_GB));
}

export function parseGigabytesInput(value, emptyValue = 0) {
    const trimmed = `${value || ''}`.trim();
    if (trimmed === '') {
        return emptyValue;
    }

    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed) || parsed < 0) {
        return null;
    }

    return Math.round(parsed * BYTES_IN_GB);
}