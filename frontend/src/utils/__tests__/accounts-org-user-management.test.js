import {
    ACCOUNTS_ORG_USER_ACTIONS,
    ACCOUNTS_ORG_USER_VIEWS,
    buildAccountsOrgUserManagementURL,
} from '../accounts-org-user-management';

describe('accounts org user management URL helper', () => {
    test('returns empty string when no base URL is configured', () => {
        expect(buildAccountsOrgUserManagementURL('', {
            view: ACCOUNTS_ORG_USER_VIEWS.MEMBERS,
            action: ACCOUNTS_ORG_USER_ACTIONS.ADD_USER,
        })).toBe('');
    });

    test('builds an Accounts URL with org-admin context query parameters', () => {
        const result = buildAccountsOrgUserManagementURL('https://accounts.example.com/orgs/org-1/users/', {
            view: ACCOUNTS_ORG_USER_VIEWS.USER,
            action: ACCOUNTS_ORG_USER_ACTIONS.MANAGE_USER,
            userEmail: 'person@example.com',
            query: 'alice',
            status: 'active',
        });

        expect(result).toBe('https://accounts.example.com/orgs/org-1/users/?source=sesamefs-org-admin&view=user&action=manage-user&user_email=person%40example.com&query=alice&status=active');
    });

    test('preserves existing query parameters on the base URL', () => {
        const result = buildAccountsOrgUserManagementURL('/orgs/org-1/users/?foo=bar', {
            view: ACCOUNTS_ORG_USER_VIEWS.ADMINS,
            action: ACCOUNTS_ORG_USER_ACTIONS.ADD_ADMIN,
        });

        expect(result).toBe('http://localhost/orgs/org-1/users/?foo=bar&source=sesamefs-org-admin&view=admins&action=add-admin');
    });
});