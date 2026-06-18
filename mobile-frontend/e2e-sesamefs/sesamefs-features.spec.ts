/**
 * SesameFS feature coverage (developer / dev-mode).
 *
 * Proves a developer can log into the running stack and that the core library
 * features the web UI depends on actually work end-to-end:
 *   - UI login with the built-in dev account
 *   - library create + appears in "My Libraries"
 *   - folder create, empty-file create, real file upload
 *   - file lock / unlock
 *   - copy file into a subfolder
 *
 * Setup/verification go through the same REST API the React UI uses; one test
 * also drives the browser UI to confirm "access as developer".
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  accountInfo,
  browserDevLogin,
  cleanupTestArtifacts,
  copyFile,
  createFile,
  createRepo,
  listDir,
  listDirNames,
  listRepos,
  mkdir,
  setLock,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const ADMIN = DEV_TOKENS.admin;
const PREFIX = SUITE_PREFIX.features;
const name = (tag: string) => uniqueName(tag, PREFIX);

test.describe('SesameFS features (developer access)', () => {
  test.afterEach(async ({ playwright, baseURL }) => {
    const request: APIRequestContext = await playwright.request.newContext({ baseURL });
    await cleanupTestArtifacts(request, ADMIN, PREFIX);
    await request.dispose();
  });

  test('developer can access the running dev stack via the web UI', async ({ page, context, baseURL }) => {
    // Dev-mode browser login: seed the backend's sesamefs_auth cookie (SSO-only
    // login page means there is no email/password form to drive here).
    await browserDevLogin(context, baseURL!, 'admin');
    await page.goto('/dashboard/');
    await expect(page).toHaveURL(/\/dashboard\/?$/);
    await expect(page.locator('#main')).toBeVisible();
    // The dashboard shell is the proof of an authenticated developer session.
    await expect(page.getByRole('link', { name: 'My Libraries' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'All Activities' })).toBeVisible();
  });

  test('dev token identifies the admin developer account', async ({ request }) => {
    const info = await accountInfo(request, ADMIN);
    expect(info.email).toBe(DEV_EMAILS.admin);
    expect(info.can_add_repo).toBe(true);
  });

  test('create a library and see it in My Libraries', async ({ request }) => {
    const repoName = name('lib');
    const repo = await createRepo(request, ADMIN, repoName);
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;

    const mine = await listRepos(request, ADMIN, 'mine');
    expect(mine.some((r) => r.repo_id === repo.repoId && r.repo_name === repoName)).toBe(true);
  });

  test('folder, empty file, and real upload all land in the library', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('crud'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;

    expect(await mkdir(request, ADMIN, id, '/reports')).toBe(true);
    expect(await createFile(request, ADMIN, id, '/notes.txt')).toBe(true);
    expect(await uploadFile(request, ADMIN, id, '/', 'uploaded.txt', 'real upload body')).toBe(200);

    const root = await listDirNames(request, ADMIN, id, '/');
    expect(root).toEqual(expect.arrayContaining(['reports', 'notes.txt', 'uploaded.txt']));
  });

  test('lock and unlock a file', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('lock'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, ADMIN, id, '/', 'doc.txt', 'lock me');

    const locked = await setLock(request, ADMIN, id, '/doc.txt', 'lock');
    expect(locked.status).toBe(200);
    expect(locked.payload.is_locked).toBe(true);

    const afterLock = await listDir(request, ADMIN, id, '/');
    expect(afterLock.find((d) => d.name === 'doc.txt')?.is_locked).toBe(true);

    const unlocked = await setLock(request, ADMIN, id, '/doc.txt', 'unlock');
    expect(unlocked.status).toBe(200);
    expect(unlocked.payload.is_locked).toBe(false);
  });

  test('copy a file into a subfolder', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('copy'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;

    await mkdir(request, ADMIN, id, '/dest');
    await uploadFile(request, ADMIN, id, '/', 'orig.txt', 'copy me');
    expect(await copyFile(request, ADMIN, id, '/orig.txt', '/dest')).toBe(200);

    expect(await listDirNames(request, ADMIN, id, '/dest')).toContain('orig.txt');
    // Original stays put — copy, not move.
    expect(await listDirNames(request, ADMIN, id, '/')).toContain('orig.txt');
  });
});
