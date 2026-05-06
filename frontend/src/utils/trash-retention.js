import { gettext } from './constants';

function normalizeRetentionDays(value) {
    const days = Number(value);
    if (!Number.isFinite(days) || days <= 0) {
        return 0;
    }

    return Math.floor(days);
}

function getPageOptions(scope = 'app') {
    if (scope === 'sys') {
        return window.sysadmin?.pageOptions || {};
    }

    return window.app?.pageOptions || {};
}

export function formatRetentionDaysValue(days) {
    return `${normalizeRetentionDays(days)}d`;
}

export function appendRetentionNotice(baseMessage, notice) {
    if (!notice) {
        return baseMessage;
    }

    return `${baseMessage}<br /><span class="text-secondary">${notice}</span>`;
}

export function getUserGraceDays(scope = 'app') {
    return normalizeRetentionDays(getPageOptions(scope).userGraceDays);
}

export function getOrgGraceDays(scope = 'app') {
    return normalizeRetentionDays(getPageOptions(scope).orgGraceDays);
}

export function getTrashReposExpireDays(scope = 'app') {
    return normalizeRetentionDays(getPageOptions(scope).trashReposExpireDays);
}

export function getDeletedUsersRetentionMessage(scope = 'app') {
    const userGraceDays = getUserGraceDays(scope);
    if (userGraceDays > 0) {
        return gettext('Deleted users remain restorable for the next {placeholder} days before permanent deletion.').replace('{placeholder}', userGraceDays);
    }

    return gettext('Deleted users are cleaned up according to the current system retention policy.');
}

export function getDeletedOrganizationsRetentionMessage(scope = 'app') {
    const orgGraceDays = getOrgGraceDays(scope);
    if (orgGraceDays > 0) {
        return gettext('Deleted organizations remain restorable for the next {placeholder} days before permanent deletion.').replace('{placeholder}', orgGraceDays);
    }

    return gettext('Deleted organizations are cleaned up according to the current system retention policy.');
}

export function getDeletedLibrariesRetentionMessage(scope = 'app') {
    const trashReposExpireDays = getTrashReposExpireDays(scope);
    if (trashReposExpireDays > 0) {
        return gettext('Deleted libraries are kept in trash for {placeholder} days before automatic cleanup.').replace('{placeholder}', trashReposExpireDays);
    }

    return gettext('Deleted libraries are cleaned automatically according to the current system retention policy.');
}

export function getDeletedLibrariesEmptyMessage(scope = 'app') {
    const trashReposExpireDays = getTrashReposExpireDays(scope);
    if (trashReposExpireDays > 0) {
        return gettext('You have not deleted any libraries in the last {placeholder} days. A deleted library will be cleaned automatically after this period.').replace('{placeholder}', trashReposExpireDays);
    }

    return gettext('You have not deleted any libraries recently. Deleted libraries are cleaned automatically according to the current system retention policy.');
}

export function getDeletedLibrariesCleanupTip(scope = 'app') {
    const trashReposExpireDays = getTrashReposExpireDays(scope);
    if (trashReposExpireDays > 0) {
        return gettext('Tip: libraries deleted {placeholder} days ago will be cleaned automatically.').replace('{placeholder}', trashReposExpireDays);
    }

    return gettext('Tip: deleted libraries are cleaned automatically according to the current system retention policy.');
}