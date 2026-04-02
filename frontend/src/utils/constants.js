export const defaultContentForSDoc = {
  version: 0,
  children: [{ id: 'aaaa', type: 'paragraph', children: [{ text: '' }] }]
};

const appConfig = window.app?.config || {};
// constants.js snapshots globals at import time. Entry points must run bootstrap
// before importing app bundles, and any values that can change post-bootstrap
// should be exported as `let` with an explicit updater.
const appPageOptions = window.app?.pageOptions || {};
const orgPageOptions = window.org?.pageOptions || {};
const sysAdminPageOptions = window.sysadmin?.pageOptions || {};
const adminPermissions = sysAdminPageOptions.admin_permissions || {};
const parseTrue = (value) => value === true || value === 'True';

export const dirPath = '/';
export const gettext = window.gettext || ((message) => message);

export const internalFilePath = '/_Internal/seatable-integration.json';

export const siteRoot = appConfig.siteRoot || '/';
export const loginUrl = appConfig.loginUrl || '/login/';
export const avatarInfo = appConfig.avatarInfo || {};
export const logoPath = appConfig.logoPath || '';
export const mediaUrl = appConfig.mediaUrl || '';
export const siteTitle = appConfig.siteTitle || '';
export const siteName = appConfig.siteName || '';
export const logoWidth = appConfig.logoWidth || '';
export const logoHeight = appConfig.logoHeight || '';
export const isPro = appConfig.isPro === 'True';
export const isDBSqlite3 = appConfig.isDBSqlite3 || false;
export const isDocs = appConfig.isDocs === 'True';
export const lang = appConfig.lang || 'en';
export const fileServerRoot = appConfig.fileServerRoot || '';
export const useGoFileserver = appConfig.useGoFileserver || false;
export const seafileVersion = appConfig.seafileVersion || '';
export const serviceURL = appConfig.serviceURL || '';
export const billingUrl = appConfig.billingUrl || `${siteRoot}billing/`;
export const subscriptionDetailsUrl = appConfig.subscriptionDetailsUrl || billingUrl;
export const appAvatarURL = appConfig.avatarURL || '';
export const faviconPath = appConfig.faviconPath || '';
export const loginBGPath = appConfig.loginBGPath || '';
export const enableRepoAutoDel = appConfig.enableRepoAutoDel || false;

//pageOptions
export const trashReposExpireDays = appPageOptions.trashReposExpireDays || 0;
export const seafileCollabServer = appPageOptions.seafileCollabServer || '';
export const name = appPageOptions.name || '';
export const contactEmail = appPageOptions.contactEmail || '';
export const username = appPageOptions.username || '';
export const canAddRepo = appPageOptions.canAddRepo || false;
export const canShareRepo = appPageOptions.canShareRepo || false;
export const canAddGroup = appPageOptions.canAddGroup || false;
export const groupImportMembersExtraMsg = appPageOptions.groupImportMembersExtraMsg || '';
export const canGenerateShareLink = appPageOptions.canGenerateShareLink || false;
export const canGenerateUploadLink = appPageOptions.canGenerateUploadLink || false;
export const canSendShareLinkEmail = appPageOptions.canSendShareLinkEmail || false;
export const canViewOrg = parseTrue(appPageOptions.canViewOrg);
export const fileAuditEnabled = appPageOptions.fileAuditEnabled || false;
export const folderPermEnabled = appPageOptions.folderPermEnabled || false;
export const enableResetEncryptedRepoPassword = appPageOptions.enableResetEncryptedRepoPassword === 'True';
export const isEmailConfigured = appPageOptions.isEmailConfigured === 'True';
export const enableUploadFolder = appPageOptions.enableUploadFolder === 'True';
export const enableResumableFileUpload = appPageOptions.enableResumableFileUpload === 'True';
export const resumableUploadFileBlockSize = appPageOptions.resumableUploadFileBlockSize || 0;
export const storages = appPageOptions.storages || []; // storage backends
export const libraryTemplates = appPageOptions.libraryTemplates || []; // library templates
export const enableRepoSnapshotLabel = appPageOptions.enableRepoSnapshotLabel || false;
export const shareLinkForceUsePassword = appPageOptions.shareLinkForceUsePassword || false;
export const shareLinkPasswordMinLength = appPageOptions.shareLinkPasswordMinLength || 0;
export const shareLinkPasswordStrengthLevel = appPageOptions.shareLinkPasswordStrengthLevel || '';
export const shareLinkExpireDaysMin = appPageOptions.shareLinkExpireDaysMin || 0;
// export const shareLinkExpireDaysMax = window.app.pageOptions.shareLinkExpireDaysMax;
export const sideNavFooterCustomHtml = appPageOptions.sideNavFooterCustomHtml || '';
export const aboutDialogCustomHtml = appPageOptions.aboutDialogCustomHtml || '';
// export const shareLinkExpireDaysDefault = window.app.pageOptions.shareLinkExpireDaysDefault;
export const uploadLinkExpireDaysMin = appPageOptions.uploadLinkExpireDaysMin || 0;
// export const uploadLinkExpireDaysMax = window.app.pageOptions.uploadLinkExpireDaysMax;
// export const uploadLinkExpireDaysDefault = window.app.pageOptions.uploadLinkExpireDaysDefault;
export const enableShareToDepartment = appPageOptions.enableShareToDepartment || false;
export const maxFileName = appPageOptions.maxFileName || 255;
export const canPublishRepo = appPageOptions.canPublishRepo || false;
export const enableEncryptedLibrary = appPageOptions.enableEncryptedLibrary || false;
export const enableRepoHistorySetting = appPageOptions.enableRepoHistorySetting || false;
export const isSystemStaff = appPageOptions.isSystemStaff || false;
export const thumbnailSizeForOriginal = appPageOptions.thumbnailSizeForOriginal || 0;
export const repoPasswordMinLength = appPageOptions.repoPasswordMinLength || 0;
export const canAddPublicRepo = appPageOptions.canAddPublicRepo || false;
export const canInvitePeople = appPageOptions.canInvitePeople || false;
export const canLockUnlockFile = appPageOptions.canLockUnlockFile || false;
export const customNavItems = appPageOptions.customNavItems || [];
export const enableShowContactEmailWhenSearchUser = appPageOptions.enableShowContactEmailWhenSearchUser || false;
export const enableShowLoginIDWhenSearchUser = appPageOptions.enableShowLoginIDWhenSearchUser || false;
export const maxUploadFileSize = appPageOptions.maxUploadFileSize || 0;
export const maxNumberOfFilesForFileupload = appPageOptions.maxNumberOfFilesForFileupload || 0;
export const enableOCM = appPageOptions.enableOCM || false;
export const ocmRemoteServers = appPageOptions.ocmRemoteServers || [];
export const enableOCMViaWebdav = appPageOptions.enableOCMViaWebdav || false;
export const enableSSOToThirdpartWebsite = appPageOptions.enableSSOToThirdpartWebsite || false;
export const enableSeadoc = appPageOptions.enableSeadoc || false;

export const curNoteMsg = appPageOptions.curNoteMsg || '';
export const curNoteID = appPageOptions.curNoteID || '';

export const enableTC = appPageOptions.enableTC || false;

export const enableVideoThumbnail = appPageOptions.enableVideoThumbnail || false;
export const enableThumbnail = appPageOptions.enableThumbnail || false;  // SesameFS: disabled by default (no thumbnail backend)

export const enableOnlyoffice = appPageOptions.enableOnlyoffice || false;
export const onlyofficeConverterExtensions = appPageOptions.onlyofficeConverterExtensions || [];

export const canSetExProps = appPageOptions.canSetExProps || false;

// seafile_ai
export const enableSeafileAI = appPageOptions.enableSeafileAI || false;

// dtable
export const workspaceID = appPageOptions.workspaceID || '';
export const showLogoutIcon = appPageOptions.showLogoutIcon || false;
export const additionalShareDialogNote = appPageOptions.additionalShareDialogNote || '';
export const additionalAppBottomLinks = appPageOptions.additionalAppBottomLinks || [];
export const additionalAboutDialogLinks = appPageOptions.additionalAboutDialogLinks || [];
export const enableSeaTableIntegration = appPageOptions.enableSeaTableIntegration || false;

// wiki
export const slug = window.wiki ? window.wiki.config.slug : '';
export const repoID = window.wiki ? window.wiki.config.repoId : '';
export const initialPath = window.wiki ? window.wiki.config.initial_path : '';
export const permission = window.wiki ? window.wiki.config.permission === 'True' : '';
export const isDir = window.wiki ? window.wiki.config.isDir : '';
export const serviceUrl = window.wiki ? window.wiki.config.serviceUrl : '';
export const isPublicWiki = window.wiki ? window.wiki.config.isPublicWiki === 'True' : '';
export const sharedToken = window.wiki ? window.wiki.config.sharedToken : '';
export const sharedType = window.wiki ? window.wiki.config.sharedType : '';
export const hasIndex = window.wiki ? window.wiki.config.hasIndex : '';
export const assetsUrl = window.wiki ? window.wiki.config.assetsUrl : '';

// file history
export const PER_PAGE = 25;
export const historyRepoID = window.fileHistory ? window.fileHistory.pageOptions.repoID : '';
export const repoName = window.fileHistory ? window.fileHistory.pageOptions.repoName : '';
export const filePath = window.fileHistory ? window.fileHistory.pageOptions.filePath : '';
export const fileName = window.fileHistory ? window.fileHistory.pageOptions.fileName : '';
export const useNewAPI = window.fileHistory ? window.fileHistory.pageOptions.use_new_api : '';
export const canDownload = window.fileHistory ? window.fileHistory.pageOptions.can_download_file : '';
export const canCompare = window.fileHistory ? window.fileHistory.pageOptions.can_compare : '';

// org admin — exported as `let` so that _updateOrgContext() can update them
// via ES module live bindings after the async bootstrap fetch resolves.
// Do NOT change these back to `const`.
export let orgID = orgPageOptions.orgID || '';
export let orgName = orgPageOptions.orgName || '';
export let invitationLink = orgPageOptions.invitationLink || '';
export let orgMemberQuotaEnabled = parseTrue(orgPageOptions.orgMemberQuotaEnabled);
export let orgMembers = orgPageOptions.orgMembers || 0;
export let orgMembersQuota = orgPageOptions.orgMembersQuota || 0;
export let hasUserAvailability = Object.prototype.hasOwnProperty.call(orgPageOptions, 'hasUserAvailability') ? orgPageOptions.hasUserAvailability : true;
export let orgEnableAdminCustomLogo = orgPageOptions.orgEnableAdminCustomLogo === 'True';
export let orgEnableAdminCustomName = orgPageOptions.orgEnableAdminCustomName === 'True';
export let orgEnableAdminInviteUser = orgPageOptions.orgEnableAdminInviteUser === 'True';
export let enableMultiADFS = orgPageOptions.enableMultiADFS === 'True';
export let enableSubscription = orgPageOptions.enableSubscription || false;

// NOTE: _updateOrgContext is currently unused (dead code) because bootstrap-entry.js
// uses dynamic import() which guarantees window.org.pageOptions is populated before
// constants.js evaluates. Kept as a safety net in case the entry point changes.
export function _updateOrgContext(pageOptions) {
  if (!pageOptions) return;
  if (pageOptions.orgID !== undefined) orgID = pageOptions.orgID;
  if (pageOptions.orgName !== undefined) orgName = pageOptions.orgName;
  if (pageOptions.invitationLink !== undefined) invitationLink = pageOptions.invitationLink;
  if (pageOptions.orgMemberQuotaEnabled !== undefined) orgMemberQuotaEnabled = parseTrue(pageOptions.orgMemberQuotaEnabled);
  if (pageOptions.orgMembers !== undefined) orgMembers = pageOptions.orgMembers;
  if (pageOptions.orgMembersQuota !== undefined) orgMembersQuota = pageOptions.orgMembersQuota;
  if (Object.prototype.hasOwnProperty.call(pageOptions, 'hasUserAvailability')) hasUserAvailability = pageOptions.hasUserAvailability;
  if (pageOptions.orgEnableAdminCustomLogo !== undefined) orgEnableAdminCustomLogo = pageOptions.orgEnableAdminCustomLogo === 'True';
  if (pageOptions.orgEnableAdminCustomName !== undefined) orgEnableAdminCustomName = pageOptions.orgEnableAdminCustomName === 'True';
  if (pageOptions.orgEnableAdminInviteUser !== undefined) orgEnableAdminInviteUser = pageOptions.orgEnableAdminInviteUser === 'True';
  if (pageOptions.enableMultiADFS !== undefined) enableMultiADFS = pageOptions.enableMultiADFS === 'True';
  if (pageOptions.enableSubscription !== undefined) enableSubscription = pageOptions.enableSubscription || false;
}

// sys admin
export const constanceEnabled = sysAdminPageOptions.constance_enabled || '';
export const multiTenancy = sysAdminPageOptions.multi_tenancy || '';
export const multiInstitution = sysAdminPageOptions.multi_institution || '';
export const sysadminExtraEnabled = sysAdminPageOptions.sysadmin_extra_enabled || '';
export const enableGuestInvitation = sysAdminPageOptions.enable_guest_invitation || '';
export const enableTermsAndConditions = sysAdminPageOptions.enable_terms_and_conditions || '';
export const isDefaultAdmin = sysAdminPageOptions.is_default_admin || '';
export const enableFileScan = sysAdminPageOptions.enable_file_scan || '';
export const canViewSystemInfo = adminPermissions.can_view_system_info || '';
export const canViewStatistic = adminPermissions.can_view_statistic || '';
export const canConfigSystem = adminPermissions.can_config_system || '';
export const canManageLibrary = adminPermissions.can_manage_library || '';
export const canManageUser = adminPermissions.can_manage_user || '';
export const canManageGroup = adminPermissions.can_manage_group || '';
export const canViewUserLog = adminPermissions.can_view_user_log || '';
export const canViewAdminLog = adminPermissions.can_view_admin_log || '';
export const otherPermission = adminPermissions.other_permission || '';
export const enableWorkWeixin = sysAdminPageOptions.enable_work_weixin || '';
export const enableDingtalk = sysAdminPageOptions.enable_dingtalk || '';
export const enableSysAdminViewRepo = sysAdminPageOptions.enableSysAdminViewRepo || '';
export const haveLDAP = sysAdminPageOptions.haveLDAP || '';
export const enableShareLinkReportAbuse = sysAdminPageOptions.enable_share_link_report_abuse || '';

// institution admin
export const institutionName = appPageOptions.institutionName || '';

// canUpgrade: true when the org is on the free tier (any owner) or when a paid
// owner is approaching/exceeding quota. Set dynamically by app.js after the
// account/info response arrives. Defaults to false so components stay hidden
// until we know the real state.
// NOTE: This is a mutable reference — components that render at startup should
// read window.app.pageOptions.canUpgrade directly if they need the live value
// after the async load completes.
export const canUpgrade = appPageOptions.canUpgrade === true;

// isOrgOwner: true when the current user holds the owner role in the org.
export const isOrgOwner = appPageOptions.isOrgOwner === true;

// upgradeFeatures: list of short feature names blocked by the enforcement
// profile (e.g. ["add_group", "invite_guest"]). Empty for paid orgs.
// NOTE: populated asynchronously; use window.app.pageOptions.upgradeFeatures
// for live reads inside event handlers.
export const upgradeFeatures = Array.isArray(appPageOptions.upgradeFeatures) ? appPageOptions.upgradeFeatures : [];

// isFreeUser: backward-compat alias. True when canUpgrade and the user is the
// org owner, matching the old "free user sees upgrade CTA" semantic.
// New code should prefer canUpgrade + isOrgOwner directly.
// @deprecated — use canUpgrade / upgradeFeatures instead.
export const isFreeUser = canUpgrade && isOrgOwner;

// Share/upload link expiry caps from enforcement profile.
// Backend caps the value server-side; the frontend uses these to constrain
// the date picker so the user sees the right range before submitting.
// 0 means no cap (unlimited / paid plan with no restriction).
export const shareLinkExpireDaysMax = appPageOptions.shareLinkExpireDaysMax || 0;
export const shareLinkExpireDaysDefault = appPageOptions.shareLinkExpireDaysDefault || 0;
export const uploadLinkExpireDaysMax = appPageOptions.uploadLinkExpireDaysMax || 0;
export const uploadLinkExpireDaysDefault = appPageOptions.uploadLinkExpireDaysDefault || 0;
