import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  getAuthToken,
  setAuthToken,
  clearAuthToken,
  login,
  listRepos,
  listDir,
  listGroups,
  createGroup,
  renameFile,
  renameDir,
  deleteFile,
  starFile,
  createShareLink,
  searchUsers,
  getOnlyOfficeConfig,
  shareToUser,
  listRepoShareItems,
  removeUserShare,
  createRepo,
  setRepoPassword,
  addGroupMember,
  removeGroupMember,
  setGroupAdmin,
  deleteGroup,
  listBeSharedRepos,
} from '../api';

// Mock serviceURL to return a stable base URL
vi.mock('../config', () => ({
  serviceURL: () => 'http://localhost:8080',
}));

const TOKEN = 'test-auth-token-abc123';

function mockFetchOk(body: unknown = {}) {
  return vi.fn().mockResolvedValue({
    ok: true,
    json: () => Promise.resolve(body),
  });
}

function mockFetchFail(status = 400, body: unknown = {}) {
  return vi.fn().mockResolvedValue({
    ok: false,
    status,
    json: () => Promise.resolve(body),
  });
}

describe('Auth token management', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('returns null when no token is set', () => {
    expect(getAuthToken()).toBeNull();
  });

  it('stores and retrieves a token', () => {
    setAuthToken(TOKEN);
    expect(getAuthToken()).toBe(TOKEN);
  });

  it('clears the token', () => {
    setAuthToken(TOKEN);
    clearAuthToken();
    expect(getAuthToken()).toBeNull();
  });
});

describe('login', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('sends credentials and stores the returned token', async () => {
    const fetchMock = mockFetchOk({ token: 'returned-token' });
    vi.stubGlobal('fetch', fetchMock);

    const token = await login('user@example.com', 'password123');

    expect(token).toBe('returned-token');
    expect(getAuthToken()).toBe('returned-token');

    // Verify fetch was called with correct URL and body
    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api2/auth-token/',
      expect.objectContaining({
        method: 'POST',
        body: expect.any(URLSearchParams),
      }),
    );
  });

  it('throws on failed login with server error message', async () => {
    vi.stubGlobal('fetch', mockFetchFail(401, { non_field_errors: ['Invalid credentials'] }));

    await expect(login('user@example.com', 'wrong')).rejects.toThrow('Invalid credentials');
  });

  it('throws generic message when server returns no error details', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      json: () => Promise.reject(new Error('parse error')),
    }));

    await expect(login('user@example.com', 'wrong')).rejects.toThrow('Login failed');
  });
});

describe('API methods with auth headers', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('listRepos sends GET with auth header', async () => {
    const repos = [{ id: 'repo1', name: 'My Library', size: 100, permission: 'rw', owner: 'user@test.com', owner_name: 'User', encrypted: 0, mtime: 1700000000 }];
    const fetchMock = mockFetchOk(repos);
    vi.stubGlobal('fetch', fetchMock);

    const result = await listRepos();

    expect(result[0].repo_id).toBe('repo1');
    expect(result[0].repo_name).toBe('My Library');
    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api2/repos/',
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: `Token ${TOKEN}`,
          Accept: 'application/json',
        }),
      }),
    );
  });

  it('listDir sends correct path parameter', async () => {
    const entries = [{ name: 'file.txt', type: 'file' }];
    const fetchMock = mockFetchOk(entries);
    vi.stubGlobal('fetch', fetchMock);

    const result = await listDir('repo-id', '/documents');

    expect(result).toEqual(entries);
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain('/api2/repos/repo-id/dir/');
    expect(url).toContain('p=%2Fdocuments');
  });

  it('listGroups fetches groups', async () => {
    const groups = [{ id: 1, name: 'Team' }];
    vi.stubGlobal('fetch', mockFetchOk(groups));

    const result = await listGroups();
    expect(result).toEqual(groups);
  });

  it('createGroup sends POST with group name', async () => {
    const group = { id: 2, name: 'New Group' };
    const fetchMock = mockFetchOk(group);
    vi.stubGlobal('fetch', fetchMock);

    const result = await createGroup('New Group');

    expect(result).toEqual(group);
    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api/v2.1/groups/',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ name: 'New Group' }),
      }),
    );
  });

  it('renameFile sends rename operation', async () => {
    const fetchMock = mockFetchOk();
    vi.stubGlobal('fetch', fetchMock);

    await renameFile('repo-id', '/old.txt', 'new.txt');

    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api2/repos/repo-id/file/?p=%2Fold.txt&operation=rename',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ newname: 'new.txt' }),
      }),
    );
  });

  it('renameDir sends rename operation', async () => {
    const fetchMock = mockFetchOk();
    vi.stubGlobal('fetch', fetchMock);

    await renameDir('repo-id', '/old-dir', 'new-dir');

    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api2/repos/repo-id/dir/?p=%2Fold-dir&operation=rename',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ newname: 'new-dir' }),
      }),
    );
  });

  it('deleteFile sends DELETE request', async () => {
    const fetchMock = mockFetchOk();
    vi.stubGlobal('fetch', fetchMock);

    await deleteFile('repo-id', '/file.txt');

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api2/repos/repo-id/file/'),
      expect.objectContaining({ method: 'DELETE' }),
    );
  });

  it('starFile sends POST to starredfiles', async () => {
    const fetchMock = mockFetchOk();
    vi.stubGlobal('fetch', fetchMock);

    await starFile('repo-id', '/important.doc');

    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api2/starredfiles/',
      expect.objectContaining({
        method: 'POST',
        body: expect.any(URLSearchParams),
      }),
    );
  });

  it('createShareLink sends POST with options', async () => {
    const link = { token: 'abc', link: 'http://example.com/share/abc' };
    const fetchMock = mockFetchOk(link);
    vi.stubGlobal('fetch', fetchMock);

    const result = await createShareLink('repo-id', '/file.pdf', { expire_days: 7 });

    expect(result).toEqual(link);
    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:8080/api/v2.1/share-links/',
      expect.objectContaining({
        method: 'POST',
        body: expect.any(String),
      }),
    );
    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.expire_days).toBe(7);
  });

  it('searchUsers sends query parameter', async () => {
    const users = [{ email: 'bob@example.com', name: 'Bob' }];
    vi.stubGlobal('fetch', mockFetchOk({ users }));

    const result = await searchUsers('bob');

    expect(result).toEqual(users);
  });
});

describe('Error handling for failed requests', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('listRepos throws on failure', async () => {
    vi.stubGlobal('fetch', mockFetchFail(500));
    await expect(listRepos()).rejects.toThrow('Failed to load libraries');
  });

  it('listDir throws on failure', async () => {
    vi.stubGlobal('fetch', mockFetchFail(500));
    await expect(listDir('repo-id', '/')).rejects.toThrow('Failed to load directory');
  });

  it('createGroup throws with server error message', async () => {
    vi.stubGlobal('fetch', mockFetchFail(400, { error_msg: 'Group name exists' }));
    await expect(createGroup('Dupe')).rejects.toThrow('Group name exists');
  });

  it('createShareLink throws with server error message', async () => {
    vi.stubGlobal('fetch', mockFetchFail(400, { error_msg: 'Invalid path' }));
    await expect(createShareLink('repo-id', '/bad')).rejects.toThrow('Invalid path');
  });

  it('deleteFile throws on failure', async () => {
    vi.stubGlobal('fetch', mockFetchFail(404));
    await expect(deleteFile('repo-id', '/missing.txt')).rejects.toThrow('Failed to delete file');
  });
});

describe('getOnlyOfficeConfig', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('requests the editor config for the given repo + path and returns it', async () => {
    const payload = {
      doc: { document: {}, documentType: 'word', editorConfig: {}, token: 'jwt.signed.token' },
      api_js_url: 'http://localhost:8088/web-apps/apps/api/documents/api.js',
    };
    const fetchMock = mockFetchOk(payload);
    vi.stubGlobal('fetch', fetchMock);

    const res = await getOnlyOfficeConfig('repo-1', '/docs/report.docx');

    expect(res).toEqual(payload);
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toContain('/api/v2.1/repos/repo-1/onlyoffice/');
    expect(url).toContain('p=%2Fdocs%2Freport.docx');
    expect(opts.headers.Authorization).toBe(`Token ${TOKEN}`);
  });

  it("surfaces the backend's error_msg on failure (e.g. OnlyOffice disabled)", async () => {
    vi.stubGlobal('fetch', mockFetchFail(503, { error_msg: 'OnlyOffice is not enabled' }));
    await expect(getOnlyOfficeConfig('repo-1', '/x.docx')).rejects.toThrow(
      'OnlyOffice is not enabled',
    );
  });

  it('falls back to a generic message when the error body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: () => Promise.reject(new Error('not json')),
    }));
    await expect(getOnlyOfficeConfig('repo-1', '/x.docx')).rejects.toThrow('Failed to open document');
  });
});

describe('User sharing (dir/shared_items contract)', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shareToUser sends username as an ARRAY with the path as the `p` query param', async () => {
    const fetchMock = mockFetchOk({});
    vi.stubGlobal('fetch', fetchMock);

    await shareToUser('repo-1', '/team', 'bob@example.com', 'rw');

    const [url, opts] = fetchMock.mock.calls[0];
    // path must ride in `p`, not the body (the backend ignores a body path).
    expect(url).toContain('/api/v2.1/repos/repo-1/dir/shared_items/');
    expect(url).toContain('p=%2Fteam');
    expect(opts.method).toBe('PUT');
    const body = JSON.parse(opts.body);
    expect(body).toEqual({ share_type: 'user', username: ['bob@example.com'], permission: 'rw' });
    // Regression guard: username must be an array, never a bare string.
    expect(Array.isArray(body.username)).toBe(true);
    expect(body).not.toHaveProperty('path');
  });

  it('shareToUser surfaces the backend error_msg on failure', async () => {
    vi.stubGlobal('fetch', mockFetchFail(400, { error_msg: 'invalid request body' }));
    await expect(shareToUser('r', '/', 'x@y.z', 'r')).rejects.toThrow('invalid request body');
  });

  it('listRepoShareItems queries by `p` and maps user shares onto the UI shape', async () => {
    const fetchMock = mockFetchOk([
      { share_type: 'user', share_to: 'a@b.c', share_to_name: 'Alice', permission: 'r' },
      { share_type: 'group', group_id: 1, permission: 'rw' },
    ]);
    vi.stubGlobal('fetch', fetchMock);

    const items = await listRepoShareItems('repo-1', '/team');

    expect(fetchMock.mock.calls[0][0]).toContain('p=%2Fteam');
    expect(items).toHaveLength(1);
    // Mapped from share_to / share_to_name so the UI (user_email/user_name) renders.
    expect(items[0].user_email).toBe('a@b.c');
    expect(items[0].user_name).toBe('Alice');
  });

  it('removeUserShare deletes with `p` (not `path`) and the username', async () => {
    const fetchMock = mockFetchOk({});
    vi.stubGlobal('fetch', fetchMock);

    await removeUserShare('repo-1', '/team', 'bob@example.com');

    const [url, opts] = fetchMock.mock.calls[0];
    expect(opts.method).toBe('DELETE');
    expect(url).toContain('p=%2Fteam');
    expect(url).toContain('username=bob%40example.com');
    expect(url).not.toContain('path=');
  });
});

describe('Encrypted libraries', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('createRepo sends `encrypted` as a JSON boolean (not the string "true")', async () => {
    const fetchMock = mockFetchOk({ repo_id: 'r1', name: 'Secret' });
    vi.stubGlobal('fetch', fetchMock);

    await createRepo('Secret', true, 'pw12345678');

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.encrypted).toBe(true); // boolean, not "true"
    expect(typeof body.encrypted).toBe('boolean');
    expect(body.passwd).toBe('pw12345678');
  });

  it('createRepo omits encryption fields for a plain library', async () => {
    const fetchMock = mockFetchOk({ repo_id: 'r1' });
    vi.stubGlobal('fetch', fetchMock);

    await createRepo('Plain');

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body).not.toHaveProperty('encrypted');
    expect(body).not.toHaveProperty('passwd');
  });

  it('setRepoPassword unlocks via /api/v2.1/repos/:id/set-password/ with a JSON body', async () => {
    const fetchMock = mockFetchOk({ success: true });
    vi.stubGlobal('fetch', fetchMock);

    await setRepoPassword('repo-1', 'SecretPass123');

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8080/api/v2.1/repos/repo-1/set-password/');
    expect(opts.method).toBe('POST');
    expect(opts.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(opts.body)).toEqual({ password: 'SecretPass123' });
  });

  it('setRepoPassword throws on an incorrect password', async () => {
    vi.stubGlobal('fetch', mockFetchFail(400, {}));
    await expect(setRepoPassword('repo-1', 'wrong')).rejects.toThrow('Incorrect password');
  });
});


describe('Group management (access-gated mutations)', () => {
  beforeEach(() => {
    localStorage.clear();
    setAuthToken(TOKEN);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('addGroupMember POSTs the email to the members endpoint', async () => {
    const fetchMock = mockFetchOk({ success: true });
    vi.stubGlobal('fetch', fetchMock);
    await addGroupMember('7', 'bob@example.com');
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8080/api/v2.1/groups/7/members/');
    expect(opts.method).toBe('POST');
    expect(JSON.parse(opts.body)).toEqual({ email: 'bob@example.com' });
  });

  it('addGroupMember surfaces the backend permission error', async () => {
    vi.stubGlobal('fetch', mockFetchFail(403, { error: 'permission denied' }));
    await expect(addGroupMember('7', 'x@y.z')).rejects.toThrow('permission denied');
  });

  it('removeGroupMember DELETEs the encoded email', async () => {
    const fetchMock = mockFetchOk({ success: true });
    vi.stubGlobal('fetch', fetchMock);
    await removeGroupMember('7', 'bob@example.com');
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8080/api/v2.1/groups/7/members/bob%40example.com');
    expect(opts.method).toBe('DELETE');
  });

  it('setGroupAdmin PUTs is_admin', async () => {
    const fetchMock = mockFetchOk({ success: true });
    vi.stubGlobal('fetch', fetchMock);
    await setGroupAdmin('7', 'bob@example.com', true);
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8080/api/v2.1/groups/7/members/bob%40example.com');
    expect(opts.method).toBe('PUT');
    expect(JSON.parse(opts.body)).toEqual({ is_admin: true });
  });

  it('deleteGroup DELETEs the group', async () => {
    const fetchMock = mockFetchOk({ success: true });
    vi.stubGlobal('fetch', fetchMock);
    await deleteGroup('7');
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8080/api/v2.1/groups/7');
    expect(opts.method).toBe('DELETE');
  });
});


describe('Shared-with-me (received shares)', () => {
  beforeEach(() => { localStorage.clear(); setAuthToken(TOKEN); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('listBeSharedRepos uses /api2/repos/?type=shared and maps to SharedRepo', async () => {
    const fetchMock = mockFetchOk([
      { id: 'r1', name: 'FromAlice', permission: 'rw', owner: 'alice@x.io', mtime: 111, encrypted: 0 },
    ]);
    vi.stubGlobal('fetch', fetchMock);
    const repos = await listBeSharedRepos();
    expect(fetchMock.mock.calls[0][0]).toBe('http://localhost:8080/api2/repos/?type=shared');
    expect(repos).toHaveLength(1);
    expect(repos[0].repo_id).toBe('r1');
    expect(repos[0].repo_name).toBe('FromAlice');
    expect(repos[0].user).toBe('alice@x.io');
    expect(repos[0].permission).toBe('rw');
  });
});
