import React from 'react';

function getDefaultAppConfig() {
    return {
        siteRoot: '/',
        loginUrl: '/login/',
        serviceURL: '',
        fileServerRoot: `${window.location.origin}/seafhttp/`,
        mediaUrl: '/static/',
        siteTitle: 'Sesame Disk',
        siteName: 'Sesame Disk',
        logoPath: 'img/logo.png',
        faviconPath: '/favicon.png',
        logoWidth: 147,
        logoHeight: 64,
        loginBGPath: '',
        avatarInfo: { defaultUrl: '/static/img/default-avatar.png' },
        avatarURL: '/static/img/default-avatar.png',
        isPro: 'True',
        isDocs: 'False',
        isDBSqlite3: 'False',
        useGoFileserver: 'True',
        seafileVersion: '11.0.0',
        lang: 'en',
        enableRepoAutoDel: 'True',
    };
}

function getDefaultLanguageOption(langCode = 'en') {
    return {
        langCode,
        langName: langCode,
    };
}

function getDefaultBackendRoutes() {
    return {
        languageChange: 'i18n/?lang={langCode}',
        passwordChange: 'accounts/password/change/',
        twoFactorSetup: 'profile/two_factor_authentication/setup/',
        twoFactorDisable: 'profile/two_factor_authentication/disable/',
        twoFactorBackupTokens: 'profile/two_factor_authentication/backup/tokens/',
        deleteAccount: 'profile/delete/',
        wechatWorkConnect: 'work-weixin/oauth-connect/?next={next}',
        wechatWorkDisconnect: 'work-weixin/oauth-disconnect/?next={next}',
        dingtalkConnect: 'dingtalk/connect/?next={next}',
        dingtalkDisconnect: 'dingtalk/disconnect/?next={next}',
        samlConnect: 'saml2/connect/?next={next}',
        samlDisconnect: 'saml2/disconnect/?next={next}',
        orgSamlConnect: 'org/custom/{orgID}/saml2/connect/?next={next}',
        orgSamlDisconnect: 'org/custom/{orgID}/saml2/disconnect/?next={next}',
    };
}

const APP_PAGE_OPTION_DEFAULTS = {
    name: '',
    username: '',
    contactEmail: '',
    langCode: 'en',
    currentLang: getDefaultLanguageOption(),
    langList: [],
    backendRoutes: getDefaultBackendRoutes(),
    canAddRepo: false,
    canShareRepo: false,
    canAddGroup: false,
    canGenerateShareLink: false,
    canGenerateUploadLink: false,
    canSendShareLinkEmail: false,
    canSendShareLinkMail: false,
    canViewOrg: false,
    fileAuditEnabled: false,
    folderPermEnabled: false,
    enableResetEncryptedRepoPassword: 'False',
    isEmailConfigured: 'False',
    trashReposExpireDays: 30,
    seafileCollabServer: '',
    groupImportMembersExtraMsg: '',
    enableUploadFolder: 'True',
    enableResumableFileUpload: 'True',
    resumableUploadFileBlockSize: 8 * 1024 * 1024,
    maxUploadFileSize: null,
    maxNumberOfFilesForFileupload: 1000,
    storages: [],
    libraryTemplates: [],
    enableRepoSnapshotLabel: false,
    enableEncryptedLibrary: true,
    enableRepoHistorySetting: true,
    repoPasswordMinLength: 8,
    canAddPublicRepo: false,
    canPublishRepo: false,
    canInvitePeople: false,
    canLockUnlockFile: false,
    canUpdatePassword: false,
    passwordOperationText: 'Change',
    enableGetAuthToken: false,
    enableWebdavSecret: false,
    enableAddressBook: false,
    showLogoutIcon: true,
    shareLinkForceUsePassword: false,
    shareLinkPasswordMinLength: 8,
    shareLinkPasswordStrengthLevel: 1,
    shareLinkExpireDaysMin: 0,
    shareLinkExpireDaysMax: 0,
    shareLinkExpireDaysDefault: 0,
    uploadLinkExpireDaysMin: 0,
    uploadLinkExpireDaysMax: 0,
    uploadLinkExpireDaysDefault: 0,
    maxFileName: 255,
    thumbnailSizeForOriginal: 1024,
    customNavItems: [],
    sideNavFooterCustomHtml: '',
    aboutDialogCustomHtml: '',
    enableShowContactEmailWhenSearchUser: false,
    enableShowLoginIDWhenSearchUser: false,
    isSystemStaff: false,
    enableVideoThumbnail: false,
    enableOnlyoffice: true,
    onlyofficeConverterExtensions: ['doc', 'xls', 'ppt', 'odt', 'ods', 'odp'],
    enableSeafileAI: false,
    enableSSOToThirdpartWebsite: false,
    enableTC: false,
    enableSeaTableIntegration: false,
    canSetExProps: false,
    enableSubscription: false,
    additionalShareDialogNote: {},
    additionalAppBottomLinks: [],
    additionalAboutDialogLinks: [],
    enableShareToDepartment: false,
    enableSysAdminViewRepo: false,
    enableOCM: false,
    ocmRemoteServers: [],
    enableOCMViaWebdav: false,
    enableSeadoc: false,
    curNoteMsg: '',
    curNoteID: '',
    guideEnabled: false,
    fileUpdatesEmailInterval: 0,
    collaborateEmailInterval: 0,
    enableWechatWork: false,
    enableDingtalk: false,
    isOrgContext: false,
    enableADFS: false,
    enableDeleteAccount: false,
    csrfToken: '',
    orgID: '',
    samlConnected: false,
    orgSamlConnected: false,
    socialNextPage: '/',
    defaultDevice: '',
    backupTokens: [],
};

const ORG_APP_PAGE_OPTION_OVERRIDES = {
    canViewOrg: true,
};

const SYS_APP_PAGE_OPTION_OVERRIDES = {
    canViewOrg: true,
    isSystemStaff: true,
    enableSysAdminViewRepo: true,
};

const ORG_PAGE_OPTION_DEFAULTS = {
    orgID: '',
    orgName: '',
    invitationLink: '',
    orgMemberQuotaEnabled: 'False',
    orgMembers: 0,
    orgMembersQuota: 0,
    hasUserAvailability: true,
    orgEnableAdminCustomLogo: 'False',
    orgEnableAdminCustomName: 'False',
    orgEnableAdminInviteUser: 'False',
    enableMultiADFS: 'False',
    enableSubscription: false,
};

const SYSADMIN_PAGE_OPTION_DEFAULTS = {
    constance_enabled: false,
    multi_tenancy: true,
    multi_institution: false,
    sysadmin_extra_enabled: false,
    enable_guest_invitation: false,
    enable_terms_and_conditions: false,
    is_default_admin: false,
    enable_file_scan: false,
    enable_work_weixin: false,
    enable_dingtalk: false,
    enableSysAdminViewRepo: true,
    haveLDAP: false,
    enable_share_link_report_abuse: false,
    twoFactorAuthEnabled: false,
    trashReposExpireDays: 30,
    availableRoles: ['default', 'user', 'admin', 'guest', 'readonly'],
    availableAdminRoles: ['superadmin'],
    institutions: [],
    admin_permissions: {
        can_view_system_info: false,
        can_view_statistic: false,
        can_config_system: false,
        can_manage_library: false,
        can_manage_user: false,
        can_manage_group: false,
        can_view_user_log: false,
        can_view_admin_log: false,
        other_permission: false,
    },
};

function getAppPageOptionDefaults(scope) {
    if (scope === 'org') {
        return { ...APP_PAGE_OPTION_DEFAULTS, ...ORG_APP_PAGE_OPTION_OVERRIDES };
    }

    if (scope === 'sys') {
        return { ...APP_PAGE_OPTION_DEFAULTS, ...SYS_APP_PAGE_OPTION_OVERRIDES };
    }

    return { ...APP_PAGE_OPTION_DEFAULTS };
}

function ensureBootstrapGlobals(scope) {
    if (typeof window.gettext !== 'function') {
        window.gettext = (message) => message;
    }

    if (typeof window.ngettext !== 'function') {
        window.ngettext = (singular, plural, count) => (count === 1 ? singular : plural);
    }

    window.app = window.app || {};
    window.app.config = {
        ...getDefaultAppConfig(),
        ...(window.app.config || {}),
        ...(typeof window.SESAMEFS_CONFIG === 'object' ? window.SESAMEFS_CONFIG : {}),
    };
    window.app.pageOptions = { ...getAppPageOptionDefaults(scope), ...(window.app.pageOptions || {}) };
    window.org = window.org || {};
    window.org.pageOptions = { ...ORG_PAGE_OPTION_DEFAULTS, ...(window.org.pageOptions || {}) };
    window.sysadmin = window.sysadmin || {};
    window.sysadmin.pageOptions = { ...SYSADMIN_PAGE_OPTION_DEFAULTS, ...(window.sysadmin.pageOptions || {}) };
}

function buildBootstrapUrl() {
    const siteRoot = window.app?.config?.siteRoot || '/';
    const normalizedSiteRoot = siteRoot.endsWith('/') ? siteRoot : `${siteRoot}/`;
    return `${normalizedSiteRoot}api/v2.1/bootstrap/`;
}

export async function loadBootstrap(scope = 'app') {
    ensureBootstrapGlobals(scope);

    try {
        const response = await fetch(buildBootstrapUrl(), { credentials: 'same-origin' });
        if (!response.ok) {
            return {
                bootstrapError: true,
                status: response.status,
            };
        }

        const data = await response.json();
        if (data?.app_page_options) {
            Object.assign(window.app.pageOptions, data.app_page_options);
        }

        const orgPageOptions = data?.org_page_options || data?.page_options;
        if (orgPageOptions) {
            Object.assign(window.org.pageOptions, orgPageOptions);
        }

        if (data?.sysadmin_page_options) {
            Object.assign(window.sysadmin.pageOptions, data.sysadmin_page_options);
        }

        window.app.pageOptions.bootstrapReady = true;
        window.__SESAMEFS_BOOTSTRAP__ = data;
        return data;
    } catch (error) {
        return {
            bootstrapError: true,
            reason: 'network',
        };
    }
}

export function BootstrapLoadError({ message, title }) {
    return (
        <div id="main" className="d-flex align-items-center justify-content-center min-vh-100">
            <div className="cur-view-container text-center p-4" style={{ maxWidth: '32rem' }}>
                <h3 className="sf-heading mb-3">{title}</h3>
                <p className="text-secondary mb-3">{message}</p>
                <button type="button" className="btn btn-outline-primary" onClick={() => window.location.reload()}>
                    {window.gettext('Retry')}
                </button>
            </div>
        </div>
    );
}

export function getBootstrapErrorProps(data) {
    if (!data?.bootstrapError) {
        return null;
    }

    return {
        title: window.gettext('Unable to load page'),
        message: window.gettext('The admin bootstrap request failed. Check backend connectivity and try again.'),
    };
}

export function AdminAccessDenied({ message, title }) {
    const next = encodeURIComponent(window.location.pathname + window.location.search + window.location.hash);
    const loginUrl = `${window.app?.config?.loginUrl || '/login/'}?next=${next}`;

    return (
        <div id="main" className="d-flex align-items-center justify-content-center min-vh-100">
            <div className="cur-view-container text-center p-4" style={{ maxWidth: '32rem' }}>
                <h3 className="sf-heading mb-3">{title}</h3>
                <p className="text-secondary mb-3">{message}</p>
                <a href={loginUrl} className="btn btn-outline-primary">{window.gettext('Log in')}</a>
            </div>
        </div>
    );
}

export function getAdminDeniedProps(data, scope) {
    if (data?.bootstrapError) {
        return getBootstrapErrorProps(data);
    }

    const permissions = data?.permissions;
    if (!permissions) {
        return null;
    }

    if (scope === 'org' && permissions.canAccessOrgAdmin) {
        return null;
    }

    if (scope === 'sys' && permissions.canAccessSysAdmin) {
        return null;
    }

    if (permissions.isAuthenticated) {
        return {
            title: window.gettext('Permission denied'),
            message: scope === 'org'
                ? window.gettext('Your account does not have organization admin access for this panel.')
                : window.gettext('Your account does not have system admin access for this panel.'),
        };
    }

    return {
        title: window.gettext('Authentication required'),
        message: scope === 'org'
            ? window.gettext('Log in to continue to the organization admin panel.')
            : window.gettext('Log in to continue to the system admin panel.'),
    };
}