export const ACCOUNTS_ORG_USER_VIEWS = Object.freeze({
    MEMBERS: 'members',
    ADMINS: 'admins',
    USER: 'user',
});

export const ACCOUNTS_ORG_USER_ACTIONS = Object.freeze({
    ADD_USER: 'add-user',
    INVITE_USERS: 'invite-users',
    ADD_ADMIN: 'add-admin',
    TRANSFER_OWNERSHIP: 'transfer-ownership',
    SEARCH_USERS: 'search-users',
    MANAGE_USER: 'manage-user',
    EDIT_NAME: 'edit-name',
    EDIT_CONTACT_EMAIL: 'edit-contact-email',
    SET_STATUS: 'set-status',
    DELETE_USER: 'delete-user',
    RESTORE_USER: 'restore-user',
    RESET_PASSWORD: 'reset-password',
    REVOKE_ADMIN: 'revoke-admin',
});

export function buildAccountsOrgUserManagementURL(baseURL, options = {}) {
    if (!baseURL) {
        return '';
    }

    try {
        const url = new URL(baseURL, window.location.origin);
        const {
            view,
            action,
            userEmail,
            query,
            status,
        } = options;

        url.searchParams.set('source', 'sesamefs-org-admin');

        if (view) {
            url.searchParams.set('view', view);
        }

        if (action) {
            url.searchParams.set('action', action);
        }

        if (userEmail) {
            url.searchParams.set('user_email', userEmail);
        }

        if (query) {
            url.searchParams.set('query', query);
        }

        if (status) {
            url.searchParams.set('status', status);
        }

        return url.toString();
    } catch (error) {
        return baseURL;
    }
}