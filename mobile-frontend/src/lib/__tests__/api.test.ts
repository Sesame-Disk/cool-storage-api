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
