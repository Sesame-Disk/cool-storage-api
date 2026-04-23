import { SeafileAPI } from 'seafile-js';
import { serviceURL } from './constants';
import { quotaWarningInterceptor } from './quota-warning';
import {
  clearAuth,
  redirectToLogin,
} from './auth-state';

// Login bypass for testing - set REACT_APP_BYPASS_LOGIN=true to skip login
// When enabled, uses 'dev-token-admin' which the backend accepts in dev mode
const BYPASS_LOGIN = process.env.REACT_APP_BYPASS_LOGIN === 'true';
const BYPASS_TOKEN = 'dev-token-admin'; // Default admin token for testing

let seafileAPI = new SeafileAPI();

function initCookieBackedAPI(server) {
  seafileAPI.initForSeahubUsage({ siteRoot: server + '/', xcsrfHeaders: '' });
  if (seafileAPI.req) {
    seafileAPI.req.defaults.withCredentials = true;
  }
}

function normalizeAccountInfo(data = {}) {
  const email = data.email || data.contact_email || data.login_id || '';
  return {
    ...data,
    email,
    contact_email: data.contact_email || email,
    login_id: data.login_id || email,
    name: data.name || email,
    avatar_url: data.avatar_url || '/static/img/default-avatar.png',
  };
}

function createAPIError(message, responseData, status) {
  const error = new Error(message);
  error.response = {
    data: responseData,
  };
  if (status) {
    error.response.status = status;
  }
  return error;
}

function mapAdminSearchUsersToUserSelectResponse(response) {
  const userList = response?.data?.user_list || [];
  return {
    data: {
      users: userList.map(user => normalizeAccountInfo({
        email: user.email,
        contact_email: user.contact_email || user.email,
        login_id: user.login_id || user.email,
        name: user.name,
        avatar_url: user.avatar_url,
      })),
    },
  };
}

// Global response interceptor:
// 1. On success: show quota warning toast if X-Quota-Warning header is present.
// 2. On 401 error: clear auth state and redirect to login.
function setupResponseInterceptor() {
  if (!seafileAPI.req) return;
  seafileAPI.req.interceptors.response.use(
    quotaWarningInterceptor,
    error => {
      if (error.response && error.response.status === 401) {
        clearAuth();
        // Avoid redirect loops: only redirect if not already on login page.
        if (window.location.pathname !== '/login/' && window.location.pathname !== '/login') {
          redirectToLogin('expired');
          // Return a pending promise to prevent further .catch() handling
          return new Promise(() => { });
        }
      }
      return Promise.reject(error);
    }
  );
}

// Initialize the browser client against the backend cookie-backed session.
function initAPI() {
  const server = serviceURL || window.location.origin;

  if (BYPASS_LOGIN) {
    seafileAPI.init({ server, token: BYPASS_TOKEN });
  } else {
    initCookieBackedAPI(server);
  }

  // Set up global 401 interceptor after this.req is created
  setupResponseInterceptor();
}

async function hasActiveSession() {
  if (BYPASS_LOGIN) {
    return true;
  }

  const server = serviceURL || window.location.origin;
  try {
    const response = await fetch(server + '/api2/auth/ping/', {
      credentials: 'same-origin',
    });
    return response.ok;
  } catch (err) {
    return false;
  }
}

// Legacy password login is kept in-memory only for the current page lifetime.
async function login(username, password) {
  const server = serviceURL || window.location.origin;

  // Call the auth-token endpoint
  const response = await fetch(`${server}/api2/auth-token/`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: new URLSearchParams({
      username: username,
      password: password,
    }),
  });

  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    // Handle non_field_errors as either string or array (Seafile compatibility)
    let errorMsg = 'Login failed';
    if (error.non_field_errors) {
      errorMsg = Array.isArray(error.non_field_errors)
        ? error.non_field_errors[0]
        : error.non_field_errors;
    } else if (error.detail) {
      errorMsg = error.detail;
    } else if (error.error) {
      errorMsg = error.error;
    }
    throw new Error(errorMsg);
  }

  const data = await response.json();

  if (data.token) {
    seafileAPI.init({ server, token: data.token });
    if (seafileAPI.req) {
      seafileAPI.req.defaults.withCredentials = true;
    }
    setupResponseInterceptor();
    return data;
  }

  throw new Error('No token received');
}

// Logout - clear token and redirect to OIDC logout if available
async function logout() {
  const server = serviceURL || window.location.origin;

  try {
    // Try to get the OIDC logout URL for single logout
    const response = await fetch(server + '/api/v2.1/auth/oidc/logout/');
    if (response.ok) {
      const data = await response.json();
      clearAuth();
      if (data.logout_url) {
        // Redirect to OIDC provider's logout endpoint for single logout.
        // This will clear the SSO session and redirect back to our login page.
        window.location.href = data.logout_url;
        return;
      }
    }
  } catch (err) {
    // OIDC logout not available, fall back to local logout
  }

  // Fallback: just clear local state and redirect to login.
  clearAuth();
  redirectToLogin('required', '/');
}

// Invalidate only the local SesameFS session and clear client auth state.
// This intentionally does NOT log out from the OIDC provider, so a follow-up
// OIDC login can reuse the current Accounts browser session.
async function invalidateSession() {
  const server = serviceURL || window.location.origin;

  try {
    await fetch(server + '/api/v2.1/auth/session/', {
      method: 'DELETE',
      credentials: 'same-origin',
    });
  } catch (err) {
    // Best-effort local logout. We still clear client state below.
  }

  clearAuth();
}

// Browser sessions are cookie-backed. Do not expose the session token to JS.
function getToken() {
  return BYPASS_LOGIN ? BYPASS_TOKEN : null;
}

// Reinitialize the client after OIDC login. The backend session cookie is the
// source of truth; the returned token is intentionally not persisted in JS.
function setAuthToken(_token) {
  const server = serviceURL || window.location.origin;
  initCookieBackedAPI(server);
  setupResponseInterceptor();
}

// Initialize on load
initAPI();

seafileAPI.invalidateSession = invalidateSession;

// ============================================================================
// OIDC API methods - for SSO authentication
// These use fetch directly because they're called before user is authenticated
// ============================================================================

seafileAPI.getAccountInfo = function () {
  const server = this.server || serviceURL || window.location.origin;
  const url = server + '/api2/account/info/';
  return this.req.get(url).then((response) => ({
    ...response,
    data: normalizeAccountInfo(response.data),
  }));
};

// Settings/account info compatibility with SesameFS backend.
seafileAPI.getUserInfo = function () {
  return this.getAccountInfo();
};

seafileAPI.updateUserInfo = function (data = {}) {
  const server = this.server || serviceURL || window.location.origin;
  const url = server + '/api2/account/info/';
  const payload = {};

  if (typeof data.name === 'string') {
    payload.name = data.name;
  }

  if (Object.keys(payload).length === 0) {
    return Promise.reject(createAPIError('No supported profile fields were provided', {
      error: 'no supported profile fields were provided'
    }, 400));
  }

  return this.req.put(url, payload).then((response) => ({
    ...response,
    data: normalizeAccountInfo(response.data),
  }));
};

seafileAPI.listAPIKeys = function () {
  const server = this.server || serviceURL || window.location.origin;
  return this.req.get(server + '/api/v2.1/api-keys/');
};

seafileAPI.createAPIKey = function (label, scope, expiresInDays) {
  const server = this.server || serviceURL || window.location.origin;
  const payload = { label, scope };
  if (expiresInDays !== undefined && expiresInDays !== null) {
    payload.expires_in_days = expiresInDays;
  }
  return this.req.post(server + '/api/v2.1/api-keys/', payload);
};

seafileAPI.revokeAPIKey = function (keyHash) {
  const server = this.server || serviceURL || window.location.origin;
  return this.req.delete(server + '/api/v2.1/api-keys/' + encodeURIComponent(keyHash) + '/');
};

// Get OIDC configuration (public endpoint)
seafileAPI.getOIDCConfig = async function () {
  const server = this.server || serviceURL || window.location.origin;
  const url = server + '/api/v2.1/auth/oidc/config/';
  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error('Failed to get OIDC config');
    }
    return { data: await response.json() };
  } catch (err) {
    throw err.message ? err : new Error('Network error: unable to reach OIDC config endpoint');
  }
};

// Get OIDC login URL
seafileAPI.getOIDCLoginURL = async function (redirectURI, returnURL) {
  const server = this.server || serviceURL || window.location.origin;
  let url = server + '/api/v2.1/auth/oidc/login/';
  const params = new URLSearchParams();
  if (redirectURI) params.set('redirect_uri', redirectURI);
  if (returnURL) params.set('return_url', returnURL);
  if (params.toString()) url += '?' + params.toString();
  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error('Failed to get OIDC login URL');
    }
    return { data: await response.json() };
  } catch (err) {
    throw err.message ? err : new Error('Network error: unable to reach OIDC login endpoint');
  }
};

// Exchange OIDC authorization code for tokens
seafileAPI.exchangeOIDCCode = async function (code, state, redirectURI) {
  const server = this.server || serviceURL || window.location.origin;
  const url = server + '/api/v2.1/auth/oidc/callback/';
  try {
    const response = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ code, state, redirect_uri: redirectURI }),
    });
    if (!response.ok) {
      const error = await response.json().catch(() => ({}));
      throw createAPIError('OIDC callback failed', error, response.status);
    }
    return { data: await response.json() };
  } catch (err) {
    if (err.response) throw err;
    throw new Error('Network error: unable to reach OIDC callback endpoint');
  }
};

// Get OIDC logout URL for single logout
seafileAPI.getOIDCLogoutURL = async function (postLogoutRedirectURI) {
  const server = this.server || serviceURL || window.location.origin;
  let url = server + '/api/v2.1/auth/oidc/logout/';
  if (postLogoutRedirectURI) {
    url += '?post_logout_redirect_uri=' + encodeURIComponent(postLogoutRedirectURI);
  }
  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error('Failed to get OIDC logout URL');
    }
    return { data: await response.json() };
  } catch (err) {
    throw err.message ? err : new Error('Network error: unable to reach OIDC logout endpoint');
  }
};

// ============================================================================
// Tag API methods - not in upstream seafile-js, added for SesameFS
// ============================================================================

// List all tags for a repository
seafileAPI.listRepoTags = function (repoID) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/repo-tags/';
  return this.req.get(url);
};

// Create a new tag in a repository
seafileAPI.createRepoTag = function (repoID, name, color) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/repo-tags/';
  const data = { name, color };
  return this.req.post(url, data);
};

// Update a tag
seafileAPI.updateRepoTag = function (repoID, tagID, name, color) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/repo-tags/' + tagID + '/';
  const data = { name, color };
  return this.req.put(url, data);
};

// Delete a tag
seafileAPI.deleteRepoTag = function (repoID, tagID) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/repo-tags/' + tagID + '/';
  return this.req.delete(url);
};

// Get tags for a specific file
seafileAPI.getFileTags = function (repoID, filePath) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/file-tags/?file_path=' + encodeURIComponent(filePath);
  return this.req.get(url);
};

// Add a tag to a file
seafileAPI.addFileTag = function (repoID, filePath, repoTagID) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/file-tags/';
  const data = { file_path: filePath, repo_tag_id: repoTagID };
  return this.req.post(url, data);
};

// Remove a tag from a file
seafileAPI.deleteFileTag = function (repoID, fileTagID) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/file-tags/' + fileTagID + '/';
  return this.req.delete(url);
};

// List all files with a specific tag
seafileAPI.listTaggedFiles = function (repoID, tagID) {
  const url = this.server + '/api/v2.1/repos/' + repoID + '/tagged-files/' + tagID + '/';
  return this.req.get(url);
};

// List tagged files for share link
seafileAPI.getShareLinkTaggedFiles = function (shareLinkToken, tagID) {
  const url = this.server + '/api/v2.1/share-links/' + shareLinkToken + '/tagged-files/' + tagID + '/';
  return this.req.get(url);
};

// Copy/move with conflict resolution policy
seafileAPI.copyDirWithPolicy = function (repoID, dstRepoID, dstPath, srcDir, dirents, conflictPolicy) {
  let paths = Array.isArray(dirents) ? dirents : [dirents];
  let url = this.server;
  url += repoID === dstRepoID ? '/api/v2.1/repos/sync-batch-copy-item/' : '/api/v2.1/repos/async-batch-copy-item/';
  let data = {
    'src_repo_id': repoID,
    'src_parent_dir': srcDir,
    'dst_repo_id': dstRepoID,
    'dst_parent_dir': dstPath,
    'src_dirents': paths,
    'conflict_policy': conflictPolicy,
  };
  return this._sendPostRequest(url, data, { headers: { 'Content-Type': 'application/json' } });
};

seafileAPI.moveDirWithPolicy = function (repoID, dstRepoID, dstPath, srcDir, dirents, conflictPolicy) {
  let paths = Array.isArray(dirents) ? dirents : [dirents];
  let url = this.server;
  url += repoID === dstRepoID ? '/api/v2.1/repos/sync-batch-move-item/' : '/api/v2.1/repos/async-batch-move-item/';
  let data = {
    'src_repo_id': repoID,
    'src_parent_dir': srcDir,
    'dst_repo_id': dstRepoID,
    'dst_parent_dir': dstPath,
    'src_dirents': paths,
    'conflict_policy': conflictPolicy,
  };
  return this._sendPostRequest(url, data, { headers: { 'Content-Type': 'application/json' } });
};

// ============================================================================
// File/Folder Trash (Recycle Bin) API methods
// ============================================================================

seafileAPI.getRepoFolderTrash = function (repoID, path, scanStat) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/trash/';
  const params = new URLSearchParams();
  if (path) params.set('parent_dir', path);
  if (scanStat) params.set('scan_stat', scanStat);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

seafileAPI.deleteRepoTrash = function (repoID, keepDays) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/trash/';
  if (keepDays !== undefined) url += '?keep_days=' + keepDays;
  return this.req.delete(url);
};

seafileAPI.restoreFile = function (repoID, commitID, path) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/file/restore/';
  return this.req.post(url, { commit_id: commitID, p: path });
};

seafileAPI.restoreFolder = function (repoID, commitID, path) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/dir/restore/';
  return this.req.post(url, { commit_id: commitID, p: path });
};

seafileAPI.listCommitDir = function (repoID, commitID, path) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/commit/' + commitID + '/dir/';
  if (path) url += '?p=' + encodeURIComponent(path);
  return this.req.get(url);
};

// ============================================================================
// Deleted Libraries (Library Recycle Bin) API methods
// ============================================================================

seafileAPI.listDeletedRepo = function () {
  let url = this.server + '/api/v2.1/deleted-repos/';
  return this.req.get(url);
};

seafileAPI.restoreDeletedRepo = function (repoID) {
  let url = this.server + '/api/v2.1/repos/deleted/' + repoID + '/';
  return this.req.put(url);
};

// ============================================================================
// Group API methods (user-facing)
// ============================================================================

// Get group details
seafileAPI.getGroup = function (groupID) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/';
  return this.req.get(url);
};

// List libraries shared with a group
seafileAPI.listGroupRepos = function (groupID, page, perPage) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/libraries/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Create a library shared to a group (non-department)
seafileAPI.createGroupRepo = function (groupID, repo) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/group-owned-libraries/';
  let data = { name: repo.repo_name };
  if (repo.password) data.passwd = repo.password;
  if (repo.permission) data.permission = repo.permission;
  if (repo.storage_id) data.storage_id = repo.storage_id;
  return this.req.post(url, data);
};

// Create a group-owned library (department)
seafileAPI.createGroupOwnedLibrary = function (groupID, repo) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/group-owned-libraries/';
  let data = { name: repo.repo_name, permission: 'rw' };
  if (repo.passwd) data.passwd = repo.passwd;
  if (repo.storage_id) data.storage_id = repo.storage_id;
  return this.req.post(url, data);
};

// Rename a group-owned library
seafileAPI.renameGroupOwnedLibrary = function (groupID, repoID, newName) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/group-owned-libraries/' + repoID + '/';
  return this.req.put(url, { name: newName });
};

// Delete a group-owned library
seafileAPI.deleteGroupOwnedLibrary = function (groupID, repoID) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/group-owned-libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Unshare a library from a group
seafileAPI.unshareRepoToGroup = function (repoID, groupID) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/group-owned-libraries/' + repoID + '/';
  return this.req.delete(url);
};

// List group members
seafileAPI.listGroupMembers = function (groupID) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/members/';
  return this.req.get(url);
};

// Import group members via file
seafileAPI.importGroupMembersViaFile = function (groupID, file) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/members/import/';
  let form = new FormData();
  form.append('file', file);
  return this.req.post(url, form);
};

// Add group members (bulk)
seafileAPI.addGroupMembers = function (groupID, emails) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/members/bulk/';
  return this.req.post(url, { emails: emails });
};

// Delete a group member
seafileAPI.deleteGroupMember = function (groupID, email) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Set group admin role
seafileAPI.setGroupAdmin = function (groupID, email, isAdmin) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { is_admin: !!isAdmin });
};

// Search users
seafileAPI.searchUsers = function (query) {
  let url = this.server + '/api2/search-user/?q=' + encodeURIComponent(query);
  return this.req.get(url);
};

// Star/unstar items
seafileAPI.starItem = function (repoID, path) {
  let url = this.server + '/api2/starredfiles/';
  let form = new FormData();
  form.append('repo_id', repoID);
  form.append('p', path);
  return this.req.post(url, form);
};

seafileAPI.unstarItem = function (repoID, path) {
  let url = this.server + '/api2/starredfiles/?repo_id=' + repoID + '&p=' + encodeURIComponent(path);
  return this.req.delete(url);
};

// Monitor/unmonitor repos
seafileAPI.monitorRepo = function (repoID) {
  let url = this.server + '/api/v2.1/monitored-repos/';
  return this.req.post(url, { repo_id: repoID });
};

seafileAPI.unMonitorRepo = function (repoID) {
  let url = this.server + '/api/v2.1/monitored-repos/' + repoID + '/';
  return this.req.delete(url);
};

// ============================================================================
// Admin Library Management API methods
// ============================================================================

// Admin: list all libraries (paginated, sortable)
seafileAPI.sysAdminListAllRepos = function (page, perPage, sortBy) {
  let url = this.server + '/api/v2.1/admin/libraries/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: search libraries by name or ID
seafileAPI.sysAdminSearchRepos = function (name, page, perPage) {
  let url = this.server + '/api/v2.1/admin/search-libraries/';
  const params = new URLSearchParams();
  if (name) params.set('name_or_id', name);
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: create a new library
seafileAPI.sysAdminCreateRepo = function (repoName, owner) {
  let url = this.server + '/api/v2.1/admin/libraries/';
  let data = { name: repoName, owner: owner };
  return this.req.post(url, data);
};

// Admin: get library info
seafileAPI.sysAdminGetRepoInfo = function (repoID) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/';
  return this.req.get(url);
};

// Admin: delete a library (soft-delete)
seafileAPI.sysAdminDeleteRepo = function (repoID) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Admin: transfer library ownership
seafileAPI.sysAdminTransferRepo = function (repoID, email) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/transfer/';
  let data = { owner: email };
  return this.req.put(url, data);
};

// Admin: list libraries by owner email
seafileAPI.sysAdminListReposByOwner = function (email) {
  let url = this.server + '/api/v2.1/admin/libraries/?owner=' + encodeURIComponent(email);
  return this.req.get(url);
};

// Admin: list libraries for a specific org
seafileAPI.sysAdminListOrgRepos = function (orgID) {
  let url = this.server + '/api/v2.1/admin/libraries/?org_id=' + encodeURIComponent(orgID);
  return this.req.get(url);
};

// Admin: get system repo info (stub — SesameFS doesn't use a system repo)
seafileAPI.sysAdminGetSystemRepoInfo = function () {
  return Promise.resolve({ data: { name: 'System', id: '', encrypted: false, file_count: 0, size: 0 } });
};

// Admin: list directory entries in a library
seafileAPI.sysAdminListRepoDirents = function (repoID, path) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/dirents/';
  if (path) url += '?path=' + encodeURIComponent(path);
  return this.req.get(url);
};

// Admin: create folder in library (via existing dir API with admin auth)
seafileAPI.sysAdminCreateSysRepoFolder = function (repoID, path, folderName) {
  let dirPath = path.endsWith('/') ? path + folderName : path + '/' + folderName;
  let url = this.server + '/api2/repos/' + repoID + '/dir/?p=' + encodeURIComponent(dirPath);
  let form = new FormData();
  form.append('operation', 'mkdir');
  return this.req.post(url, form);
};

// Admin: delete a dirent (file or folder) in a library
seafileAPI.sysAdminDeleteRepoDirent = function (repoID, path) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/file/?p=' + encodeURIComponent(path);
  return this.req.delete(url);
};

// Admin: get download URL for a file in a library
seafileAPI.sysAdminGetRepoFileDownloadURL = function (repoID, path) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/download-link/?path=' + encodeURIComponent(path);
  return this.req.get(url);
};

// Admin: get upload URL for system repo (stub)
seafileAPI.sysAdminGetSysRepoItemUploadURL = function (path) {
  return Promise.resolve({ data: { upload_link: '' } });
};

// Admin: get library history setting
seafileAPI.sysAdminGetRepoHistorySetting = function (repoID) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/history-setting/';
  return this.req.get(url);
};

// Admin: update library history setting
seafileAPI.sysAdminUpdateRepoHistorySetting = function (repoID, days) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/history-setting/';
  let data = { keep_days: days };
  return this.req.put(url, data);
};

// Admin: list shared items for a library
seafileAPI.sysAdminListRepoSharedItems = function (repoID, shareType) {
  let url = this.server + '/api/v2.1/admin/libraries/' + repoID + '/shared-items/';
  if (shareType) url += '?share_type=' + shareType;
  return this.req.get(url);
};

// Admin: add shared item to a library (uses standard share API)
seafileAPI.sysAdminAddRepoSharedItem = function (repoID, shareType, shareToList, permission) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=/';
  const data = { share_type: shareType, permission };
  if (shareType === 'user') {
    data.username = shareToList;
  } else {
    data.group_id = shareToList;
  }
  return this.req.put(url, data);
};

// Admin: delete shared item from a library
seafileAPI.sysAdminDeleteRepoSharedItem = function (repoID, shareType, shareToID) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=/&share_type=' + shareType;
  if (shareType === 'user') {
    url += '&username=' + encodeURIComponent(shareToID);
  } else {
    url += '&group_id=' + shareToID;
  }
  return this.req.delete(url);
};

// Admin: update shared item permission
seafileAPI.sysAdminUpdateRepoSharedItemPermission = function (repoID, shareType, shareToID, permission) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=/&share_type=' + shareType;
  if (shareType === 'user') {
    url += '&username=' + encodeURIComponent(shareToID);
  } else {
    url += '&group_id=' + shareToID;
  }
  return this.req.post(url, { permission });
};

// Admin: list group libraries
seafileAPI.sysAdminListGroupRepos = function (groupID) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/libraries/';
  return this.req.get(url);
};

// Admin: unshare repo from group
seafileAPI.sysAdminUnshareRepoFromGroup = function (groupID, repoID) {
  let url = this.server + '/api/v2.1/groups/' + groupID + '/libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Admin: list repos shared to a user
seafileAPI.sysAdminListShareInRepos = function (email) {
  let url = this.server + '/api/v2.1/admin/libraries/?shared_to=' + encodeURIComponent(email);
  return this.req.get(url);
};

// Admin: add library in department group
seafileAPI.sysAdminAddRepoInDepartment = function (groupID, repoName) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/group-owned-libraries/';
  return this.req.post(url, { repo_name: repoName });
};

// Admin: delete library in department group
seafileAPI.sysAdminDeleteRepoInDepartment = function (groupID, repoID) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/group-owned-libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Admin: list all groups (paginated)
seafileAPI.sysAdminListAllGroups = function (page, perPage) {
  let url = this.server + '/api/v2.1/admin/groups/?page=' + page + '&per_page=' + perPage;
  return this.req.get(url);
};

// Admin: search groups by name
seafileAPI.sysAdminSearchGroups = function (name) {
  let url = this.server + '/api/v2.1/admin/search-group/?query=' + encodeURIComponent(name);
  return this.req.get(url);
};

// Admin: create a new group
seafileAPI.sysAdminCreateNewGroup = function (groupName, ownerEmail) {
  let url = this.server + '/api/v2.1/admin/groups/';
  return this.req.post(url, { group_name: groupName, group_owner: ownerEmail });
};

// Admin: delete a group
seafileAPI.sysAdminDismissGroupByID = function (groupID) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/';
  return this.req.delete(url);
};

// Admin: transfer a group to a new owner
seafileAPI.sysAdminTransferGroup = function (receiverEmail, groupID) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/';
  return this.req.put(url, { new_owner: receiverEmail });
};

// Admin: list group members (paginated)
seafileAPI.sysAdminListGroupMembers = function (groupID, page, perPage) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/members/?page=' + page + '&per_page=' + perPage;
  return this.req.get(url);
};

// Admin: add members to a group
seafileAPI.sysAdminAddGroupMember = function (groupID, emails) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/members/';
  return this.req.post(url, { emails: emails });
};

// Admin: remove a member from a group
seafileAPI.sysAdminDeleteGroupMember = function (groupID, email) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Admin: update group member role
seafileAPI.sysAdminUpdateGroupMemberRole = function (groupID, email, isAdmin) {
  let url = this.server + '/api/v2.1/admin/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { is_admin: !!isAdmin });
};

// ============================================================================
// Admin Trash Library API methods
// ============================================================================

// Admin: list all deleted libraries (paginated)
seafileAPI.sysAdminListTrashRepos = function (page, perPage) {
  let url = this.server + '/api/v2.1/admin/trash-libraries/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: permanently delete a trashed library
seafileAPI.sysAdminDeleteTrashRepo = function (repoID) {
  let url = this.server + '/api/v2.1/repos/deleted/' + repoID + '/';
  return this.req.delete(url);
};

// Admin: restore a trashed library
seafileAPI.sysAdminRestoreTrashRepo = function (repoID) {
  let url = this.server + '/api/v2.1/repos/deleted/' + repoID + '/';
  return this.req.put(url);
};

// Admin: permanently delete ALL trashed libraries
seafileAPI.sysAdminCleanTrashRepos = function () {
  let url = this.server + '/api/v2.1/admin/trash-libraries/';
  return this.req.delete(url);
};

// Admin: search trashed libraries by owner
seafileAPI.sysAdminSearchTrashRepos = function (owner) {
  let url = this.server + '/api/v2.1/admin/trash-libraries/?owner=' + encodeURIComponent(owner);
  return this.req.get(url);
};

// ============================================================================
// Admin Share Link & Upload Link Management
// ============================================================================

// Admin: list all share links (paginated, sortable)
seafileAPI.sysAdminListShareLinks = function (page, perPage, sortBy, sortOrder, status, active, expired, search) {
  let url = this.server + '/api/v2.1/admin/share-links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (status && status !== 'all') params.set('status', status);
  if (active !== undefined && active !== null && active !== 'all') params.set('active', active);
  if (expired !== undefined && expired !== null && expired !== 'all') params.set('expired', expired);
  if (search) params.set('search', search);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: delete any share link by token
seafileAPI.sysAdminDeleteShareLink = function (token) {
  let url = this.server + '/api/v2.1/admin/share-links/' + token + '/';
  return this.req.delete(url);
};

// Admin: activate/deactivate share link by token
seafileAPI.sysAdminSetShareLinkActive = function (token, active) {
  let url = this.server + '/api/v2.1/admin/share-links/' + token + '/active/';
  return this.req.put(url, { active: !!active });
};

// Admin: list all upload links (paginated, sortable)
seafileAPI.sysAdminListAllUploadLinks = function (page, perPage, sortBy, sortOrder, status, active, expired, search) {
  let url = this.server + '/api/v2.1/admin/upload-links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (status && status !== 'all') params.set('status', status);
  if (active !== undefined && active !== null && active !== 'all') params.set('active', active);
  if (expired !== undefined && expired !== null && expired !== 'all') params.set('expired', expired);
  if (search) params.set('search', search);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: delete any upload link by token
seafileAPI.sysAdminDeleteUploadLink = function (token) {
  let url = this.server + '/api/v2.1/admin/upload-links/' + token + '/';
  return this.req.delete(url);
};

// Admin: activate/deactivate upload link by token
seafileAPI.sysAdminSetUploadLinkActive = function (token, active) {
  let url = this.server + '/api/v2.1/admin/upload-links/' + token + '/active/';
  return this.req.put(url, { active: !!active });
};

// Admin: list share links created by a specific user
seafileAPI.sysAdminListShareLinksByUser = function (email, page, perPage, sortBy, sortOrder, activeFilter, expiredFilter) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/share-links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (activeFilter && activeFilter !== 'all') params.set('active', activeFilter);
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: list upload links created by a specific user
seafileAPI.sysAdminListUploadLinksByUser = function (email, page, perPage, sortBy, sortOrder, activeFilter, expiredFilter) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/upload-links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (activeFilter && activeFilter !== 'all') params.set('active', activeFilter);
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// ============================================================================
// Admin User Management API methods
// ============================================================================

// Admin: list all users (paginated, sortable)
seafileAPI.sysAdminListUsers = function (page, perPage, isLDAPImported, sortBy, sortOrder, status) {
  let url = this.server + '/api/v2.1/admin/users/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (isLDAPImported) params.set('source', 'LDAPImport');
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (status && status !== 'all') params.set('status', status);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: list admin users
seafileAPI.sysAdminListAdmins = function () {
  let url = this.server + '/api/v2.1/admin/admins/';
  return this.req.get(url);
};

// Admin: get user info by email
seafileAPI.sysAdminGetUser = function (email) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.get(url);
};

// Admin: list API keys for a platform-org user
seafileAPI.sysAdminListUserAPIKeys = function (email) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/api-keys/';
  return this.req.get(url);
};

// Admin: create an API key for a platform-org user
seafileAPI.sysAdminCreateUserAPIKey = function (email, label, scope, expiresInDays) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/api-keys/';
  const data = { label, scope };
  if (expiresInDays !== undefined && expiresInDays !== null) {
    data.expires_in_days = expiresInDays;
  }
  return this.req.post(url, data);
};

// Admin: revoke an API key for a platform-org user
seafileAPI.sysAdminRevokeUserAPIKey = function (email, keyHash) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/api-keys/' + encodeURIComponent(keyHash) + '/';
  return this.req.delete(url);
};

// Admin: update user
// Supports: sysAdminUpdateUser(email, key, value) or sysAdminUpdateUser(email, object)
seafileAPI.sysAdminUpdateUser = function (email, keyOrData, value) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/';
  const data = typeof keyOrData === 'string' ? { [keyOrData]: value } : keyOrData;
  return this.req.put(url, data);
};

// Admin: delete user
seafileAPI.sysAdminDeleteUser = function (email) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Admin: restore soft-deleted user
seafileAPI.sysAdminRestoreUser = function (email) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/restore/';
  return this.req.put(url);
};

// Admin: add new user
seafileAPI.sysAdminAddUser = function (email, name, role) {
  let url = this.server + '/api/v2.1/admin/users/';
  let data = { email, name };
  if (role) data.role = role;
  return this.req.post(url, data);
};

// Admin: search users
seafileAPI.sysAdminSearchUsers = function (query, page, perPage, orgId) {
  let url = this.server + '/api/v2.1/admin/search-user/';
  const params = new URLSearchParams();
  params.set('query', query);
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (orgId) params.set('org_id', orgId);
  url += '?' + params.toString();
  return this.req.get(url);
};

seafileAPI.sysAdminSearchUsersForSelect = function (query, orgId) {
  return this.sysAdminSearchUsers(query, null, null, orgId).then(mapAdminSearchUsersToUserSelectResponse);
};

seafileAPI.shareFolder = function (repoID, path, shareType, permission, shareTargets) {
  const encodedPath = encodeURIComponent(path);
  const url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=' + encodedPath;
  const data = { share_type: shareType, permission };
  if (shareType === 'user') {
    data.username = shareTargets;
  } else {
    data.group_id = shareTargets;
  }
  return this.req.put(url, data);
};

// Admin: list repos owned by user
seafileAPI.sysAdminListUserRepos = function (email) {
  let url = this.server + '/api/v2.1/admin/libraries/?owner=' + encodeURIComponent(email);
  return this.req.get(url);
};

// Admin: list repos shared to user
seafileAPI.sysAdminListUserSharedRepos = function (email) {
  let url = this.server + '/api/v2.1/admin/libraries/?shared_to=' + encodeURIComponent(email);
  return this.req.get(url);
};

// Admin: batch delete users
seafileAPI.sysAdminBatchDeleteUsers = function (emails) {
  let url = this.server + '/api/v2.1/admin/users/batch/';
  return this.req.delete(url, { data: { emails } });
};

// Admin: set user quota in batch
seafileAPI.sysAdminSetUserQuotaInBatch = function (emails, quotaTotal) {
  let url = this.server + '/api/v2.1/admin/users/batch/';
  return this.req.put(url, { emails, quota_total: quotaTotal });
};

// Admin: import users via CSV/file
seafileAPI.sysAdminImportUsers = function (file) {
  let url = this.server + '/api/v2.1/admin/users/batch/';
  let form = new FormData();
  form.append('file', file);
  return this.req.post(url, form);
};

// Admin: set user admin status
seafileAPI.sysAdminSetAdminUsers = function (emails) {
  let url = this.server + '/api/v2.1/admin/admins/';
  return this.req.post(url, { emails });
};

// Admin: update admin role (e.g., admin <-> superadmin)
seafileAPI.sysAdminUpdateAdminRole = function (email, role) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { role });
};

// Admin: add admins in batch
seafileAPI.sysAdminAddAdminInBatch = function (emails) {
  let url = this.server + '/api/v2.1/admin/admins/';
  return this.req.post(url, { emails });
};

// Admin: update org user (sys-admin panel)
seafileAPI.sysAdminUpdateOrgUser = function (orgID, email, key, value) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { [key]: value });
};

// Admin: list organizations
seafileAPI.sysAdminListOrgs = function (page, perPage, status) {
  let url = this.server + '/api/v2.1/admin/organizations/';
  let params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (status && status !== 'all') params.set('status', status);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: search organizations
seafileAPI.sysAdminSearchOrgs = function (query) {
  let url = this.server + '/api/v2.1/admin/search-organization/?query=' + encodeURIComponent(query);
  return this.req.get(url);
};

// Admin: get single organization
seafileAPI.sysAdminGetOrg = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/';
  return this.req.get(url);
};

// Admin: create organization
seafileAPI.sysAdminAddOrg = function (orgName, ownerEmail) {
  let url = this.server + '/api/v2.1/admin/organizations/';
  let data = { org_name: orgName, owner_email: ownerEmail };
  return this.req.post(url, data);
};

// Admin: update organization
seafileAPI.sysAdminUpdateOrg = function (orgID, data) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/';
  return this.req.put(url, data);
};

// Admin: delete (deactivate) organization
seafileAPI.sysAdminDeleteOrg = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/';
  return this.req.delete(url);
};

// Admin: transfer organization ownership
seafileAPI.sysAdminTransferOrgOwnership = function (orgID, newOwnerEmail) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/transfer-ownership/';
  return this.req.put(url, { new_owner: newOwnerEmail });
};

// Admin: deactivate organization
seafileAPI.sysAdminDeactivateOrg = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/deactivate/';
  return this.req.post(url);
};

// Admin: reactivate organization
seafileAPI.sysAdminReactivateOrg = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/reactivate/';
  return this.req.post(url);
};

// Admin: restore deleted organization
seafileAPI.sysAdminRestoreOrg = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/restore/';
  return this.req.post(url);
};

// Admin: list org users (sys-admin panel)
seafileAPI.sysAdminListOrgUsers = function (orgID, status) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/users/';
  if (status && status !== 'all') {
    const params = new URLSearchParams();
    params.set('status', status);
    url += '?' + params.toString();
  }
  return this.req.get(url);
};

// Admin: add user to org (sys-admin panel)
seafileAPI.sysAdminAddOrgUser = function (orgID, email, name) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/users/';
  let data = { email: email, name: name };
  return this.req.post(url, data);
};

// Admin: delete user from org (sys-admin panel)
seafileAPI.sysAdminDeleteOrgUser = function (orgID, email) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/users/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Admin: list org groups (sys-admin panel)
seafileAPI.sysAdminListOrgGroups = function (orgID) {
  let url = this.server + '/api/v2.1/admin/organizations/' + orgID + '/groups/';
  return this.req.get(url);
};

// Admin: list share links for a specific org
seafileAPI.sysAdminListOrgShareLinks = function (orgID, page, perPage, sortBy, sortOrder, activeFilter, expiredFilter, search) {
  let url = this.server + '/api/v2.1/org/' + encodeURIComponent(orgID) + '/admin/links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (activeFilter && activeFilter !== 'all') params.set('active', activeFilter === 'active');
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter === 'expired');
  if (search) params.set('search', search);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: list upload links for a specific org
seafileAPI.sysAdminListOrgUploadLinks = function (orgID, page, perPage, sortBy, sortOrder, activeFilter, expiredFilter, search) {
  let url = this.server + '/api/v2.1/org/' + encodeURIComponent(orgID) + '/admin/upload-links/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (activeFilter && activeFilter !== 'all') params.set('active', activeFilter === 'active');
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter === 'expired');
  if (search) params.set('search', search);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Admin: reset user password
seafileAPI.sysAdminResetUserPassword = function (email) {
  let url = this.server + '/api/v2.1/admin/users/' + encodeURIComponent(email) + '/reset-password/';
  return this.req.put(url);
};

// ============================================================================
// Repository History API methods
// ============================================================================

seafileAPI.getRepoHistory = function (repoID, page, perPage) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/history/';
  const params = new URLSearchParams();
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (params.toString()) url += '?' + params.toString();
  return this.req.get(url);
};

// Fallback for getRepoInfo if not provided by seafile-js
if (!seafileAPI.getRepoInfo) {
  seafileAPI.getRepoInfo = function (repoID) {
    let url = this.server + '/api/v2.1/repos/' + repoID + '/';
    return this.req.get(url);
  };
}

// Get a single custom share permission by ID
// Used by lib-content-view, file-toolbar, markdown-editor when permission is "custom-{uuid}"
seafileAPI.getCustomPermission = function (repoID, permissionID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/custom-share-permissions/' + permissionID + '/';
  return this.req.get(url);
};

// List custom share permissions for a repo
seafileAPI.listCustomSharePermissions = function (repoID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/custom-share-permissions/';
  return this.req.get(url);
};

// Create a custom share permission
seafileAPI.createCustomSharePermission = function (repoID, permissionName, description, permission) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/custom-share-permissions/';
  return this.req.post(url, { permission_name: permissionName, description, permission });
};

// Update a custom share permission
seafileAPI.updateCustomSharePermission = function (repoID, permissionID, permissionName, description, permission) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/custom-share-permissions/' + permissionID + '/';
  return this.req.put(url, { permission_name: permissionName, description, permission });
};

// Delete a custom share permission
seafileAPI.deleteCustomSharePermission = function (repoID, permissionID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/custom-share-permissions/' + permissionID + '/';
  return this.req.delete(url);
};

// ============================================================================
// Rename API methods
// ============================================================================

seafileAPI.renameDir = function (repoID, dirPath, newDirName) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/?p=' + encodeURIComponent(dirPath) + '&operation=rename';
  return this.req.post(url, { newname: newDirName });
};

seafileAPI.renameFile = function (repoID, filePath, newFileName) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/file/?p=' + encodeURIComponent(filePath) + '&operation=rename';
  return this.req.post(url, { newname: newFileName });
};

// ============================================================================
// Revert API methods (for restoring files/folders to a specific commit version)
// ============================================================================

// Revert a file to its state at a specific commit
// conflictPolicy: 'replace' | 'skip' | undefined (undefined = return conflict error)
seafileAPI.revertFile = function (repoID, path, commitID, conflictPolicy) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/file/?p=' + encodeURIComponent(path) + '&operation=revert';
  const data = { commit_id: commitID };
  if (conflictPolicy) data.conflict_policy = conflictPolicy;
  return this.req.post(url, data);
};

// Revert a folder to its state at a specific commit
// conflictPolicy: 'replace' | 'skip' | undefined (undefined = return conflict error)
seafileAPI.revertFolder = function (repoID, path, commitID, conflictPolicy) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/dir/?p=' + encodeURIComponent(path) + '&operation=revert';
  const data = { commit_id: commitID };
  if (conflictPolicy) data.conflict_policy = conflictPolicy;
  return this.req.post(url, data);
};

// Revert entire library to a specific commit
seafileAPI.revertRepo = function (repoID, commitID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/?operation=revert';
  return this.req.put(url, { commit_id: commitID });
};

// Update a share link's permissions and/or expiration
seafileAPI.updateShareLink = function (token, permissions, expirationTime) {
  let url = this.server + '/api/v2.1/share-links/' + token + '/';
  let data = {};
  if (permissions) data.permissions = permissions;
  if (expirationTime) data.expiration_time = expirationTime;
  return this.req.put(url, data);
};

// Update the password of a share link. Pass null or '' to remove the password.
seafileAPI.updateShareLinkPassword = function (token, newPassword) {
  let url = this.server + '/api/v2.1/share-links/' + token + '/';
  return this.req.put(url, { password: newPassword === null || newPassword === '' ? '__remove__' : newPassword });
};

export { seafileAPI, hasActiveSession, login, logout, invalidateSession, getToken, setAuthToken, initAPI };

// ============================================================================
// Upload Link API methods (for public upload link pages)
// ============================================================================

// Get the upload URL for a shared upload link
seafileAPI.sharedUploadLinkGetFileUploadUrl = function (token) {
  let url = this.server + '/api/v2.1/upload-links/' + token + '/upload/';
  return this.req.get(url);
};

// Notify server that a file was uploaded via upload link
seafileAPI.shareLinksUploadDone = function (token) {
  let url = this.server + '/api/v2.1/upload-links/' + token + '/upload-done/';
  return this.req.post(url);
};

// ============================================================================
// Share Link ZIP Task API methods
// ============================================================================

// Get a zip download task for an entire shared folder
if (!seafileAPI.getShareLinkZipTask) {
  seafileAPI.getShareLinkZipTask = function (token, path) {
    let url = this.server + '/api/v2.1/share-link-zip-task/?share_link_token=' + token + '&path=' + encodeURIComponent(path);
    return this.req.get(url);
  };
}

// Get a zip download task for specific items in a shared folder
if (!seafileAPI.getShareLinkDirentsZipTask) {
  seafileAPI.getShareLinkDirentsZipTask = function (token, path, dirents) {
    let url = this.server + '/api/v2.1/share-link-zip-task/?share_link_token=' + token + '&path=' + encodeURIComponent(path);
    return this.req.get(url);
  };
}

// ============================================================================
// Org Admin API methods
// ============================================================================

// Org Admin: get org info
seafileAPI.orgAdminGetOrgInfo = function () {
  let url = this.server + '/api/v2.1/org/admin/info/';
  return this.req.get(url);
};

// Org Admin: get org web settings
seafileAPI.orgAdminGetWebSettings = function (orgID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/web-settings/';
  return this.req.get(url);
};

// Org Admin: update org info
seafileAPI.orgAdminUpdateOrgInfo = function (data) {
  let url = this.server + '/api/v2.1/org/admin/info/';
  return this.req.put(url, data);
};

// Org Admin: update org name
seafileAPI.orgAdminUpdateName = function (orgID, newOrgName) {
  return this.orgAdminUpdateOrgInfo({ org_name: newOrgName }).then(() => {
    return this.orgAdminGetOrgInfo();
  });
};

// Org Admin: update org logo
seafileAPI.orgAdminUpdateLogo = function (orgID, file) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/logo/';
  let form = new FormData();
  form.append('logo', file);
  return this.req.post(url, form);
};

// Org Admin: set org system setting
seafileAPI.orgAdminSetSysSettingInfo = function (orgID, key, value) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/web-settings/';
  return this.req.put(url, { [key]: value });
};

// Org Admin: list org users
seafileAPI.orgAdminListOrgUsers = function (orgID, isStaff, page, sortBy, sortOrder, statusFilter) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/';
  const params = new URLSearchParams();
  params.set('page', page || 1);
  if (isStaff === true) params.set('is_staff', 'true');
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (statusFilter) params.set('status', statusFilter);
  url += '?' + params.toString();
  return this.req.get(url);
};

// Org Admin: add org user
seafileAPI.orgAdminAddOrgUser = function (orgID, email, name) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/';
  return this.req.post(url, { email, name });
};

// Org Admin: get org user info
seafileAPI.orgAdminGetOrgUserInfo = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.get(url);
};

// Org Admin: delete org user
seafileAPI.orgAdminDeleteOrgUser = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Org Admin: restore deleted org user
seafileAPI.orgAdminRestoreOrgUser = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/restore/';
  return this.req.put(url);
};

// Org Admin: change org user status (activate/deactivate)
seafileAPI.orgAdminChangeOrgUserStatus = function (orgID, email, isActive) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { is_active: !!isActive });
};

// Org Admin: reset org user password
seafileAPI.orgAdminResetOrgUserPassword = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/set-password/';
  return this.req.put(url);
};

// Org Admin: set org user name
seafileAPI.orgAdminSetOrgUserName = function (orgID, email, name) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { name });
};

// Org Admin: set org user contact email
seafileAPI.orgAdminSetOrgUserContactEmail = function (orgID, email, contactEmail) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { contact_email: contactEmail });
};

// Org Admin: set org user quota
seafileAPI.orgAdminSetOrgUserQuota = function (orgID, email, quota) {
  return this.orgAdminUpdateOrgUserQuotas(orgID, email, { quotaTotal: quota });
};

// Org Admin: update org user storage and traffic quotas
seafileAPI.orgAdminUpdateOrgUserQuotas = function (orgID, email, { quotaTotal, trafficUploadQuota, trafficDownloadQuota }) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  const data = {};
  if (quotaTotal != null) data.quota_total = quotaTotal;
  if (trafficUploadQuota != null) data.traffic_upload_quota = trafficUploadQuota;
  if (trafficDownloadQuota != null) data.traffic_download_quota = trafficDownloadQuota;
  return this.req.put(url, data);
};

// Org Admin: set org admin role
seafileAPI.orgAdminSetOrgAdmin = function (orgID, email, isStaff) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { is_staff: !!isStaff });
};

// Org Admin: get org user owned repos
seafileAPI.orgAdminGetOrgUserOwnedRepos = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/repos/';
  return this.req.get(url);
};

seafileAPI.leaveShareRepo = function (repoID, options) {
  let url = this.server + '/api2/beshared-repos/' + repoID + '/?';
  url += new URLSearchParams(options).toString();
  return this.req.delete(url);
};

// Org Admin: get org user beshared repos
seafileAPI.orgAdminGetOrgUserBesharedRepos = function (orgID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/users/' + encodeURIComponent(email) + '/beshared-repos/';
  return this.req.get(url);
};

// Org Admin: search org user
seafileAPI.orgAdminSearchUser = function (orgID, query, page, perPage, statusFilter) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/search-user/';
  const params = new URLSearchParams();
  params.set('query', query || '');
  if (page) params.set('page', page);
  if (perPage) params.set('per_page', perPage);
  if (statusFilter) params.set('status', statusFilter);
  url += '?' + params.toString();
  return this.req.get(url);
};

// Org Admin: transfer organization ownership
seafileAPI.orgAdminTransferOwnership = function (orgID, newOwnerEmail) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/transfer-ownership/';
  return this.req.put(url, { new_owner: newOwnerEmail });
};

// Org Admin: import users via file
seafileAPI.orgAdminImportUsersViaFile = function (orgID, file) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/import-users/';
  let form = new FormData();
  form.append('file', file);
  return this.req.post(url, form);
};

// Org Admin: invite org users
seafileAPI.orgAdminInviteOrgUsers = function (orgID, emails) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/invite-users/';
  return this.req.post(url, { email_list: emails });
};

// Org Admin: list org groups
seafileAPI.orgAdminListOrgGroups = function (orgID, page) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/?page=' + (page || 1);
  return this.req.get(url);
};

// Org Admin: get single group info
seafileAPI.orgAdminGetGroup = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/';
  return this.req.get(url);
};

// Org Admin: delete org group
seafileAPI.orgAdminDeleteOrgGroup = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/';
  return this.req.delete(url);
};

// Org Admin: set group quota
seafileAPI.orgAdminSetGroupQuota = function (orgID, groupID, quota) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/';
  return this.req.put(url, { quota });
};

// Org Admin: transfer group to a new owner
seafileAPI.orgAdminTransferGroup = function (orgID, groupID, newOwnerEmail) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/transfer/';
  return this.req.put(url, { new_owner: newOwnerEmail });
};

// Org Admin: search org group
seafileAPI.orgAdminSearchGroup = function (orgID, query) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/search-group/?query=' + encodeURIComponent(query);
  return this.req.get(url);
};

// Org Admin: list group members
seafileAPI.orgAdminListGroupMembers = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/members/';
  return this.req.get(url);
};

// Org Admin: add group member
seafileAPI.orgAdminAddGroupMember = function (orgID, groupID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/members/';
  return this.req.post(url, { email });
};

// Org Admin: delete group member
seafileAPI.orgAdminDeleteGroupMember = function (orgID, groupID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.delete(url);
};

// Org Admin: set group member role
seafileAPI.orgAdminSetGroupMemberRole = function (orgID, groupID, email, isAdmin) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/members/' + encodeURIComponent(email) + '/';
  return this.req.put(url, { is_admin: !!isAdmin });
};

// Org Admin: list group libraries (repos)
seafileAPI.orgAdminListGroupRepos = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/libraries/';
  return this.req.get(url);
};

// Org Admin: list org repos
seafileAPI.orgAdminListOrgRepos = function (orgID, page, perPage, orderBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/repos/?page=' + (page || 1);
  if (perPage) url += '&per_page=' + perPage;
  if (orderBy) url += '&order_by=' + orderBy;
  return this.req.get(url);
};

// Org Admin: delete org repo
seafileAPI.orgAdminDeleteOrgRepo = function (orgID, repoID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/repos/' + repoID + '/';
  return this.req.delete(url);
};

// Org Admin: transfer org repo
seafileAPI.orgAdminTransferOrgRepo = function (orgID, repoID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/repos/' + repoID + '/';
  return this.req.put(url, { email });
};

// Org Admin: list org links
seafileAPI.orgAdminListOrgLinks = function (page, perPage, sortBy, sortOrder, expiredFilter, search) {
  let url = this.server + '/api/v2.1/org/admin/links/';
  const params = new URLSearchParams();
  params.set('page', page || 1);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter === 'expired');
  if (search) params.set('search', search);
  url += '?' + params.toString();
  return this.req.get(url);
};

// Org Admin: delete org link
seafileAPI.orgAdminDeleteOrgLink = function (token) {
  let url = this.server + '/api/v2.1/org/admin/links/' + token + '/';
  return this.req.delete(url);
};

// Org Admin: list org upload links
seafileAPI.orgAdminListOrgUploadLinks = function (page, perPage, sortBy, sortOrder, expiredFilter, search) {
  let url = this.server + '/api/v2.1/org/admin/upload-links/';
  const params = new URLSearchParams();
  params.set('page', page || 1);
  if (perPage) params.set('per_page', perPage);
  if (sortBy) params.set('order_by', sortBy);
  if (sortOrder) params.set('direction', sortOrder);
  if (expiredFilter && expiredFilter !== 'all') params.set('expired', expiredFilter === 'expired');
  if (search) params.set('search', search);
  url += '?' + params.toString();
  return this.req.get(url);
};

// Org Admin: delete org upload link
seafileAPI.orgAdminDeleteOrgUploadLink = function (token) {
  let url = this.server + '/api/v2.1/org/admin/upload-links/' + token + '/';
  return this.req.delete(url);
};

// Org Admin: list departments
seafileAPI.orgAdminListDepartments = function (orgID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/';
  return this.req.get(url);
};

// Org Admin: list department groups
seafileAPI.orgAdminListDepartGroups = function (orgID, parentID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/';
  if (parentID) url += '?parent_group=' + parentID;
  return this.req.get(url);
};

// Org Admin: add department group
seafileAPI.orgAdminAddDepartGroup = function (orgID, groupName, parentGroup) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/';
  const data = { group_name: groupName };
  if (parentGroup) data.parent_group = parentGroup;
  return this.req.post(url, data);
};

// Org Admin: get department group info
seafileAPI.orgAdminListGroupInfo = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/' + groupID + '/?return_ancestors=true';
  return this.req.get(url);
};

// Org Admin: update department group
seafileAPI.orgAdminUpdateDepartGroup = function (orgID, groupID, groupName) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/' + groupID + '/';
  return this.req.put(url, { group_name: groupName });
};

// Org Admin: delete department group
seafileAPI.orgAdminDeleteDepartGroup = function (orgID, groupID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/address-book/groups/' + groupID + '/';
  return this.req.delete(url);
};

// Org Admin: add department repo
seafileAPI.orgAdminAddDepartmentRepo = function (orgID, groupID, repoName) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/group-owned-libraries/';
  return this.req.post(url, { repo_name: repoName });
};

// Org Admin: delete department repo
seafileAPI.orgAdminDeleteDepartmentRepo = function (orgID, groupID, repoID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/groups/' + groupID + '/group-owned-libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Org Admin: list trash libraries
seafileAPI.orgAdminListTrashRepos = function (orgID, page, perPage) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/trash-libraries/?page=' + (page || 1);
  if (perPage) url += '&per_page=' + perPage;
  return this.req.get(url);
};

// Org Admin: delete trash library
seafileAPI.orgAdminDeleteTrashRepo = function (orgID, repoID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/trash-libraries/' + repoID + '/';
  return this.req.delete(url);
};

// Org Admin: restore trash library
seafileAPI.orgAdminRestoreTrashRepo = function (orgID, repoID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/trash-libraries/' + repoID + '/';
  return this.req.put(url);
};

// Org Admin: clean all trash libraries
seafileAPI.orgAdminCleanTrashRepo = function (orgID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/trash-libraries/';
  return this.req.delete(url);
};

// Sys Admin: list per-org traffic summary for a month
// Sys Admin: statistic system traffic (time-series)
seafileAPI.sysAdminStatisticTraffic = function (startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/admin/statistics/system-traffic/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Sys Admin: statistic storage (time-series)
seafileAPI.sysAdminStatisticStorages = function (startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/admin/statistics/total-storage/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Sys Admin: statistic active users
seafileAPI.sysAdminStatisticActiveUsers = function (startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/admin/statistics/active-users/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

seafileAPI.sysAdminListOrgTraffic = function (month, page, perPage, orderBy) {
  let url = this.server + '/api/v2.1/admin/statistics/org-traffic/?month=' + month;
  if (page) url += '&page=' + page;
  if (perPage) url += '&per_page=' + perPage;
  if (orderBy) url += '&order_by=' + orderBy;
  return this.req.get(url);
};

// Sys Admin: list per-user traffic for a month, optionally scoped to one org
seafileAPI.sysAdminListUserTraffic = function (month, page, perPage, orderBy, orgID) {
  let url = this.server + '/api/v2.1/admin/statistics/user-traffic/?month=' + month;
  if (orgID) url += '&org_id=' + orgID;
  if (page) url += '&page=' + page;
  if (perPage) url += '&per_page=' + perPage;
  if (orderBy) url += '&order_by=' + orderBy;
  return this.req.get(url);
};

// Org Admin: statistic files
seafileAPI.orgAdminStatisticFiles = function (orgID, startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/statistics/file-operations/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Org Admin: statistic storage
seafileAPI.orgAdminStatisticStorages = function (orgID, startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/statistics/total-storage/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Org Admin: statistic active users
seafileAPI.orgAdminStatisticActiveUsers = function (orgID, startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/statistics/active-users/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Org Admin: statistic system traffic
seafileAPI.orgAdminStatisticSystemTraffic = function (orgID, startTime, endTime, groupBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/statistics/system-traffic/?start=' + startTime + '&end=' + endTime;
  if (groupBy) url += '&group_by=' + groupBy;
  return this.req.get(url);
};

// Org Admin: list user traffic
seafileAPI.orgAdminListUserTraffic = function (orgID, month, page, perPage, orderBy) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/statistics/user-traffic/?month=' + month;
  if (page) url += '&page=' + page;
  if (perPage) url += '&per_page=' + perPage;
  if (orderBy) url += '&order_by=' + orderBy;
  return this.req.get(url);
};

// Org Admin: list file audit logs
seafileAPI.orgAdminListFileAudit = function (orgID, email, repoID, page) {
  let url = this.server + '/api/v2.1/org/admin/logs/file-access/?page=' + (page || 1);
  if (email) url += '&email=' + encodeURIComponent(email);
  if (repoID) url += '&repo_id=' + repoID;
  return this.req.get(url);
};

// Org Admin: list file update logs
seafileAPI.orgAdminListFileUpdate = function (orgID, email, repoID, page) {
  let url = this.server + '/api/v2.1/org/admin/logs/file-update/?page=' + (page || 1);
  if (email) url += '&email=' + encodeURIComponent(email);
  if (repoID) url += '&repo_id=' + repoID;
  return this.req.get(url);
};

// Org Admin: get file update detail
seafileAPI.orgAdminGetFileUpdateDetail = function (orgID, commitID) {
  let url = this.server + '/api/v2.1/org/admin/logs/file-update/' + commitID + '/';
  return this.req.get(url);
};

// Org Admin: list permission audit logs
seafileAPI.orgAdminListPermAudit = function (orgID, email, repoID, page) {
  let url = this.server + '/api/v2.1/org/admin/logs/repo-permission/?page=' + (page || 1);
  if (email) url += '&email=' + encodeURIComponent(email);
  if (repoID) url += '&repo_id=' + repoID;
  return this.req.get(url);
};

// Sys Admin: list devices (platform-wide)
seafileAPI.sysAdminListDevices = function (platform, page, perPage) {
  let url = this.server + '/api/v2.1/admin/devices/?page=' + (page || 1);
  if (platform) url += '&platform=' + platform;
  if (perPage) url += '&per_page=' + perPage;
  return this.req.get(url);
};

// Sys Admin: list device errors (platform-wide)
seafileAPI.sysAdminListDeviceErrors = function (page, perPage) {
  let url = this.server + '/api/v2.1/admin/device-errors/?page=' + (page || 1);
  if (perPage) url += '&per_page=' + perPage;
  return this.req.get(url);
};

// Sys Admin: clear all device errors (platform-wide)
seafileAPI.sysAdminClearDeviceErrors = function () {
  let url = this.server + '/api/v2.1/admin/device-errors/';
  return this.req.delete(url);
};

// Org Admin: list devices
seafileAPI.orgAdminListDevices = function (orgID, platform, page, perPage) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/devices/?page=' + (page || 1);
  if (platform) url += '&platform=' + platform;
  if (perPage) url += '&per_page=' + perPage;
  return this.req.get(url);
};

// Org Admin: unlink device
seafileAPI.orgAdminUnlinkDevice = function (orgID, platform, deviceID, email) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/devices/';
  return this.req.delete(url, { data: { platform, device_id: deviceID, email } });
};

// Org Admin: list device errors
seafileAPI.orgAdminListDevicesErrors = function (orgID, page, perPage) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/devices-errors/?page=' + (page || 1);
  if (perPage) url += '&per_page=' + perPage;
  return this.req.get(url);
};

// Org Admin: clear all device errors
seafileAPI.orgAdminClearDeviceErrors = function (orgID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/devices-errors/';
  return this.req.delete(url);
};

// Org Admin: get SAML config
seafileAPI.orgAdminGetSamlConfig = function (orgID) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/saml-config/';
  return this.req.get(url);
};

// Org Admin: update SAML config
seafileAPI.orgAdminUpdateSamlConfig = function (orgID, data) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/saml-config/';
  return this.req.put(url, data);
};

// Org Admin: verify domain
seafileAPI.orgAdminVerifyDomain = function (orgID, domain) {
  let url = this.server + '/api/v2.1/org/' + orgID + '/admin/verify-domain/';
  return this.req.put(url, { domain });
};

// ============================================================================
// Repo Share Admin API methods
// ============================================================================

// List share links for a specific repo
seafileAPI.listRepoShareLinks = function (repoID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/share-links/';
  return this.req.get(url);
};

// Delete a share link (by token)
seafileAPI.deleteRepoShareLink = function (repoID, token) {
  let url = this.server + '/api/v2.1/share-links/' + token + '/';
  return this.req.delete(url);
};

// List upload links for a specific repo
seafileAPI.listRepoUploadLinks = function (repoID) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/upload-links/';
  return this.req.get(url);
};

// Delete an upload link (by token)
seafileAPI.deleteRepoUploadLink = function (repoID, token) {
  let url = this.server + '/api/v2.1/upload-links/' + token + '/';
  return this.req.delete(url);
};

// Get all folder share info for a repo (user shares or group shares)
seafileAPI.getAllRepoFolderShareInfo = function (repoID, shareType) {
  let url = this.server + '/api/v2.1/repos/' + repoID + '/dir/shared_items/?p=/&share_type=' + shareType;
  return this.req.get(url).then((res) => {
    return { data: { share_info_list: res.data } };
  });
};

// Update permission for a user share item
seafileAPI.updateShareToUserItemPermission = function (repoID, path, shareType, shareToEmail, permission) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=' + encodeURIComponent(path) + '&share_type=user&username=' + encodeURIComponent(shareToEmail);
  return this.req.post(url, { permission });
};

// Delete a user share item
seafileAPI.deleteShareToUserItem = function (repoID, path, shareType, shareToEmail) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=' + encodeURIComponent(path) + '&share_type=user&username=' + encodeURIComponent(shareToEmail);
  return this.req.delete(url);
};

// Update permission for a group share item
seafileAPI.updateShareToGroupItemPermission = function (repoID, path, shareType, groupID, permission) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=' + encodeURIComponent(path) + '&share_type=group&group_id=' + groupID;
  return this.req.post(url, { permission });
};

// Delete a group share item
seafileAPI.deleteShareToGroupItem = function (repoID, path, shareType, groupID) {
  let url = this.server + '/api2/repos/' + repoID + '/dir/shared_items/?p=' + encodeURIComponent(path) + '&share_type=group&group_id=' + groupID;
  return this.req.delete(url);
};
