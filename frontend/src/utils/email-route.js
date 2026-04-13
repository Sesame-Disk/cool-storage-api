export function normalizeEmailRouteParam(email) {
    if (typeof email !== 'string' || email.length === 0) {
        return email;
    }

    if (/%(?![0-9A-Fa-f]{2})/.test(email)) {
        return email;
    }

    try {
        return decodeURIComponent(email);
    } catch (error) {
        return email;
    }
}