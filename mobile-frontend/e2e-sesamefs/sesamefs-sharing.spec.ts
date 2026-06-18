/**
 * SesameFS file-sharing coverage.
 *
 *   - user-to-user library share (read/write): recipient sees it under
 *     "Shared with me" and can read + write.
 *   - read-only share: recipient can read but writes are rejected.
 *   - password-protected public share link: locked until the password is
 *     supplied, then dirents are listable (verified through the frontend shell).
 *
 * The owner is the admin dev account; the recipient is the standard dev user.
 * Each is a separate API context so tokens never bleed across roles.
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  cleanupTestArtifacts,
  createRepo,
  createShareLink,
  fileDetail,
  listDirNames,
  listRepos,
  shareWithUser,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const OWNER = DEV_TOKENS.admin;
const RECIPIENT = DEV_TOKENS.user;
const RECIPIENT_EMAIL = DEV_EMAILS.user;
// The backend reports a modifier as "<user_id>@sesamefs.local"; the standard dev
// user (RECIPIENT) is user_id ...0002.
const RECIPIENT_MODIFIER = '00000000-0000-0000-0000-000000000002@sesamefs.local';
const SHARE_LINK_PASSWORD = 'pw-e2e-secret';
const PREFIX = SUITE_PREFIX.sharing;
const name = (tag: string) => uniqueName(tag, PREFIX);

test.describe('SesameFS sharing', () => {
  test.afterEach(async ({ playwright, baseURL }) => {
    const request: APIRequestContext = await playwright.request.newContext({ baseURL });
    await cleanupTestArtifacts(request, OWNER, PREFIX);
    await request.dispose();
  });

  test('read/write share: recipient sees the library and can read + write', async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('share-rw'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'shared.txt', 'owner content');

    const share = await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'rw');
    expect(share.ok).toBe(true);
    expect(Array.isArray(share.payload.success) && share.payload.success.length).toBeTruthy();

    // Recipient sees it under "shared with me".
    const shared = await listRepos(request, RECIPIENT, 'shared');
    expect(shared.some((r) => r.repo_id === id)).toBe(true);

    // Recipient can read the existing file...
    expect(await listDirNames(request, RECIPIENT, id, '/')).toContain('shared.txt');
    // ...and write a new one (rw permission).
    expect(await uploadFile(request, RECIPIENT, id, '/', 'from-recipient.txt', 'recipient content')).toBe(200);
    expect(await listDirNames(request, OWNER, id, '/')).toContain('from-recipient.txt');
  });

  test('read-only share: recipient can read but cannot write', async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('share-ro'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'readonly.txt', 'owner content');

    const share = await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'r');
    expect(share.ok).toBe(true);

    // Can read.
    expect(await listDirNames(request, RECIPIENT, id, '/')).toContain('readonly.txt');
    // Cannot write — the upload must be rejected (not 200).
    const writeStatus = await uploadFile(request, RECIPIENT, id, '/', 'blocked.txt', 'should fail');
    expect(writeStatus).not.toBe(200);
    expect(writeStatus).toBeGreaterThanOrEqual(400);
    // And the rejected file never appears for the owner.
    expect(await listDirNames(request, OWNER, id, '/')).not.toContain('blocked.txt');
  });

  test('password-protected public share link unlocks with the password', async ({ page, request }) => {
    const repo = await createRepo(request, OWNER, name('share-link'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'public.txt', 'public content');

    const link = await createShareLink(request, OWNER, id, { password: SHARE_LINK_PASSWORD });
    test.skip('skipReason' in link, 'skipReason' in link ? link.skipReason : '');
    if ('skipReason' in link) return;

    await page.goto(`/d/${link.token}`);
    await expect(page.getByText('Password Protected')).toBeVisible();

    // Locked: dirents endpoint refuses before the password is supplied.
    const lockedStatus = await page.evaluate(async (t) => {
      const r = await fetch(`/api/v2.1/share-links/${t}/dirents/?p=/`, { credentials: 'same-origin' });
      return r.status;
    }, link.token);
    expect(lockedStatus).toBe(403);

    await page.locator('input[type="password"]').fill(SHARE_LINK_PASSWORD);
    await page.getByRole('button', { name: 'Submit' }).click();
    await expect(page.locator('.shared-dir-view-main')).toBeVisible();

    // Unlocked: dirents now listable and our file is present. (The share-link
    // dirents payload uses a different item field than the repo dir listing, so
    // we assert on count + the raw payload rather than a specific key.)
    const unlocked = await page.evaluate(async (t) => {
      const r = await fetch(`/api/v2.1/share-links/${t}/dirents/?p=/`, { credentials: 'same-origin' });
      const data = await r.json().catch(() => ({}));
      const list = Array.isArray(data.dirent_list) ? data.dirent_list : [];
      return { status: r.status, count: list.length, raw: JSON.stringify(data) };
    }, link.token);
    expect(unlocked.status).toBe(200);
    expect(unlocked.count).toBeGreaterThan(0);
    expect(unlocked.raw).toContain('public.txt');
  });

  // KNOWN GAP (flagged 2026-06-18): GET .../file/detail/ reports the REQUESTING user
  // as last_modifier_email instead of the file's real author — see
  // internal/api/v2/files.go:1694,1721-1722 and docs/BUG-FILE-DETAIL-MODIFIER-20260618.md.
  // This test encodes the CORRECT expectation; test.fail() keeps the suite green while
  // the bug exists. When the backend is fixed it will pass, and Playwright will report an
  // unexpected pass so we know to drop the marker (and the bug doc).
  test("file/detail reports the real author, not the requester (known gap)", async ({ request }) => {
    test.fail(); // expected-to-fail until the attribution bug is fixed
    const repo = await createRepo(request, OWNER, name('detail-attr'));
    if ('skipReason' in repo) {
      test.skip(true, repo.skipReason);
      return;
    }
    const id = repo.repoId;
    // Owner authors the file, then shares the library rw with the recipient.
    await uploadFile(request, OWNER, id, '/', 'authored.txt', 'written by the owner');
    const share = await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'rw');
    expect(share.ok).toBe(true);

    // The RECIPIENT asks for the file's details. The modifier is the OWNER, so it must
    // NOT come back as the recipient's own identity, and it must not be empty.
    const detail = await fileDetail(request, RECIPIENT, id, '/authored.txt');
    expect(detail.status).toBe(200);
    expect(detail.payload.last_modifier_email).toBeTruthy();
    expect(detail.payload.last_modifier_email).not.toBe(RECIPIENT_MODIFIER); // FAILS today
  });
});
