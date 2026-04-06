export interface AppConfig {
  siteRoot: string;
  loginUrl: string;
  serviceURL: string;
  fileServerRoot: string;
  mediaUrl: string;
  siteTitle: string;
  siteName: string;
  logoPath: string;
  faviconPath: string;
  logoWidth: number;
  logoHeight: number;
  isPro: boolean;
  isDocs: boolean;
  useGoFileserver: boolean;
  seafileVersion: string;
  lang: string;
  enableRepoAutoDel: boolean;
}

export interface PageOptions {
  name: string;
  username: string;
  contactEmail: string;
  avatarURL: string;
  canAddRepo: boolean;
  canShareRepo: boolean;
  canAddGroup: boolean;
  canGenerateShareLink: boolean;
  canGenerateUploadLink: boolean;
  fileAuditEnabled: boolean;
  folderPermEnabled: boolean;
  enableUploadFolder: boolean;
  enableResumableFileUpload: boolean;
  resumableUploadFileBlockSize: number;
  maxUploadFileSize: number;
  maxFileName: number;
  enableEncryptedLibrary: boolean;
  enableResetEncryptedRepoPassword: boolean;
  isEmailConfigured: boolean;
  enableOnlyoffice: boolean;
  storages: any[];
  libraryTemplates: any[];
  thumbnailSizeForOriginal: number;
  customNavItems: any[];
  enableSubscription: boolean;
}

declare global {
  interface Window {
    app?: {
      config: Record<string, unknown>;
      pageOptions: Record<string, unknown>;
    };
  }
}

const DEV_CONFIG: AppConfig = {
  siteRoot: '/',
  loginUrl: '/login/',
  serviceURL: '',
  fileServerRoot: '/seafhttp/',
  mediaUrl: '/static/',
  siteTitle: 'Sesame Disk',
  siteName: 'Sesame Disk',
  logoPath: 'img/logo.png',
  faviconPath: '/favicon.png',
  logoWidth: 147,
  logoHeight: 64,
  isPro: true,
  isDocs: false,
  useGoFileserver: true,
  seafileVersion: '11.0.0',
  lang: 'en',
  enableRepoAutoDel: true,
};

const DEV_PAGE_OPTIONS: PageOptions = {
  name: 'Dev User',
  username: 'dev@sesamefs.local',
  contactEmail: 'dev@sesamefs.local',
  avatarURL: '/default-avatar.png',
  canAddRepo: true,
  canShareRepo: true,
  canAddGroup: true,
  canGenerateShareLink: true,
  canGenerateUploadLink: true,
  fileAuditEnabled: false,
  folderPermEnabled: false,
  enableUploadFolder: true,
  enableResumableFileUpload: true,
  resumableUploadFileBlockSize: 8388608,
  maxUploadFileSize: -1,
  maxFileName: 255,
  enableEncryptedLibrary: true,
  enableResetEncryptedRepoPassword: false,
  isEmailConfigured: false,
  enableOnlyoffice: false,
  storages: [],
  libraryTemplates: [],
  thumbnailSizeForOriginal: 1024,
  customNavItems: [],
  enableSubscription: false,
};

export function parseBoolean(val: string | boolean): boolean {
  if (typeof val === 'boolean') return val;
  return val === 'True';
}

export function getConfig(): AppConfig {
  const raw = window.app?.config;
  if (!raw) return { ...DEV_CONFIG };

  return {
    siteRoot: (raw.siteRoot as string) ?? DEV_CONFIG.siteRoot,
    loginUrl: (raw.loginUrl as string) ?? DEV_CONFIG.loginUrl,
    serviceURL: (raw.serviceURL as string) ?? DEV_CONFIG.serviceURL,
    fileServerRoot: (raw.fileServerRoot as string) ?? DEV_CONFIG.fileServerRoot,
    mediaUrl: (raw.mediaUrl as string) ?? DEV_CONFIG.mediaUrl,
    siteTitle: (raw.siteTitle as string) ?? DEV_CONFIG.siteTitle,
    siteName: (raw.siteName as string) ?? DEV_CONFIG.siteName,
    logoPath: (raw.logoPath as string) ?? DEV_CONFIG.logoPath,
    faviconPath: (raw.faviconPath as string) ?? DEV_CONFIG.faviconPath,
    logoWidth: (raw.logoWidth as number) ?? DEV_CONFIG.logoWidth,
    logoHeight: (raw.logoHeight as number) ?? DEV_CONFIG.logoHeight,
    isPro: raw.isPro !== undefined ? parseBoolean(raw.isPro as string | boolean) : DEV_CONFIG.isPro,
    isDocs: raw.isDocs !== undefined ? parseBoolean(raw.isDocs as string | boolean) : DEV_CONFIG.isDocs,
    useGoFileserver: raw.useGoFileserver !== undefined ? parseBoolean(raw.useGoFileserver as string | boolean) : DEV_CONFIG.useGoFileserver,
    seafileVersion: (raw.seafileVersion as string) ?? DEV_CONFIG.seafileVersion,
    lang: (raw.lang as string) ?? DEV_CONFIG.lang,
    enableRepoAutoDel: raw.enableRepoAutoDel !== undefined ? parseBoolean(raw.enableRepoAutoDel as string | boolean) : DEV_CONFIG.enableRepoAutoDel,
  };
}

export function getPageOptions(): PageOptions {
  if (typeof window === 'undefined') {
    return { ...DEV_PAGE_OPTIONS };
  }

  const raw = window.app?.pageOptions;
  if (!raw) return { ...DEV_PAGE_OPTIONS };

  return {
    name: (raw.name as string) ?? DEV_PAGE_OPTIONS.name,
    username: (raw.username as string) ?? DEV_PAGE_OPTIONS.username,
    contactEmail: (raw.contactEmail as string) ?? DEV_PAGE_OPTIONS.contactEmail,
    avatarURL: (raw.avatarURL as string) ?? DEV_PAGE_OPTIONS.avatarURL,
    canAddRepo: raw.canAddRepo !== undefined ? parseBoolean(raw.canAddRepo as string | boolean) : DEV_PAGE_OPTIONS.canAddRepo,
    canShareRepo: raw.canShareRepo !== undefined ? parseBoolean(raw.canShareRepo as string | boolean) : DEV_PAGE_OPTIONS.canShareRepo,
    canAddGroup: raw.canAddGroup !== undefined ? parseBoolean(raw.canAddGroup as string | boolean) : DEV_PAGE_OPTIONS.canAddGroup,
    canGenerateShareLink: raw.canGenerateShareLink !== undefined ? parseBoolean(raw.canGenerateShareLink as string | boolean) : DEV_PAGE_OPTIONS.canGenerateShareLink,
    canGenerateUploadLink: raw.canGenerateUploadLink !== undefined ? parseBoolean(raw.canGenerateUploadLink as string | boolean) : DEV_PAGE_OPTIONS.canGenerateUploadLink,
    fileAuditEnabled: raw.fileAuditEnabled !== undefined ? parseBoolean(raw.fileAuditEnabled as string | boolean) : DEV_PAGE_OPTIONS.fileAuditEnabled,
    folderPermEnabled: raw.folderPermEnabled !== undefined ? parseBoolean(raw.folderPermEnabled as string | boolean) : DEV_PAGE_OPTIONS.folderPermEnabled,
    enableUploadFolder: raw.enableUploadFolder !== undefined ? parseBoolean(raw.enableUploadFolder as string | boolean) : DEV_PAGE_OPTIONS.enableUploadFolder,
    enableResumableFileUpload: raw.enableResumableFileUpload !== undefined ? parseBoolean(raw.enableResumableFileUpload as string | boolean) : DEV_PAGE_OPTIONS.enableResumableFileUpload,
    resumableUploadFileBlockSize: (raw.resumableUploadFileBlockSize as number) ?? DEV_PAGE_OPTIONS.resumableUploadFileBlockSize,
    maxUploadFileSize: (raw.maxUploadFileSize as number) ?? DEV_PAGE_OPTIONS.maxUploadFileSize,
    maxFileName: (raw.maxFileName as number) ?? DEV_PAGE_OPTIONS.maxFileName,
    enableEncryptedLibrary: raw.enableEncryptedLibrary !== undefined ? parseBoolean(raw.enableEncryptedLibrary as string | boolean) : DEV_PAGE_OPTIONS.enableEncryptedLibrary,
    enableResetEncryptedRepoPassword: raw.enableResetEncryptedRepoPassword !== undefined ? parseBoolean(raw.enableResetEncryptedRepoPassword as string | boolean) : DEV_PAGE_OPTIONS.enableResetEncryptedRepoPassword,
    isEmailConfigured: raw.isEmailConfigured !== undefined ? parseBoolean(raw.isEmailConfigured as string | boolean) : DEV_PAGE_OPTIONS.isEmailConfigured,
    enableOnlyoffice: raw.enableOnlyoffice !== undefined ? parseBoolean(raw.enableOnlyoffice as string | boolean) : DEV_PAGE_OPTIONS.enableOnlyoffice,
    storages: (raw.storages as any[]) ?? DEV_PAGE_OPTIONS.storages,
    libraryTemplates: (raw.libraryTemplates as any[]) ?? DEV_PAGE_OPTIONS.libraryTemplates,
    thumbnailSizeForOriginal: (raw.thumbnailSizeForOriginal as number) ?? DEV_PAGE_OPTIONS.thumbnailSizeForOriginal,
    customNavItems: (raw.customNavItems as any[]) ?? DEV_PAGE_OPTIONS.customNavItems,
    enableSubscription: raw.enableSubscription !== undefined ? parseBoolean(raw.enableSubscription as string | boolean) : DEV_PAGE_OPTIONS.enableSubscription,
  };
}

// Getter-style exports that read at call time, not import time
export const siteRoot = () => getConfig().siteRoot;
export const loginUrl = () => getConfig().loginUrl;
export const serviceURL = () => getConfig().serviceURL;
export const fileServerRoot = () => getConfig().fileServerRoot;
export const mediaUrl = () => getConfig().mediaUrl;
export const siteTitle = () => getConfig().siteTitle;
export const siteName = () => getConfig().siteName;
export const logoPath = () => getConfig().logoPath;
export const faviconPath = () => getConfig().faviconPath;
export const logoWidth = () => getConfig().logoWidth;
export const logoHeight = () => getConfig().logoHeight;
export const isPro = () => getConfig().isPro;
export const isDocs = () => getConfig().isDocs;
export const useGoFileserver = () => getConfig().useGoFileserver;
export const seafileVersion = () => getConfig().seafileVersion;
export const lang = () => getConfig().lang;
export const enableRepoAutoDel = () => getConfig().enableRepoAutoDel;
