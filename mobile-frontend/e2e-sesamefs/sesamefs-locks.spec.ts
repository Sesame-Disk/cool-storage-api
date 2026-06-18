/**
 * SesameFS file LOCKING — exclusive locks and (future) OnlyOffice co-editing.
 *
 * Two real browsers, two users (owner = admin, recipient = user) against the web
 * frontend, sharing one library rw. Covers:
 *   - lock perception (works today): a lock taken by one user is visible to the
 *     other (is_locked / lock_owner / locked_by_me), through the frontend session.
 *   - lock ENFORCEMENT on writes (known gap): a file locked by user A must reject a
 *     write from user B. Today nothing enforces the lock, so this is a test.fail()
 *     guard — it flips to an unexpected pass once enforcement lands.
 *   - OnlyOffice co-editing: how it SHOULD behave (many users co-edit through
 *     OnlyOffice while sync/direct writes are blocked). Skipped here because no
 *     OnlyOffice document server is configured (onlyoffice.enabled=false); set
 *     MR_ONLYOFFICE to enable.
 *
 * See docs/FILE-LOCKING-DESIGN.md for the full design and docs/ for the lock model.
 */
import { expect, test, type APIRequestContext, type BrowserContext } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  browserDevLogin,
  cleanupTestArtifacts,
  createRepo,
  listDir,
  setLock,
  shareWithUser,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const FE = process.env.DESKTOP_BASE_URL || 'http://localhost:5173';
const ONLYOFFICE = process.env.MR_ONLYOFFICE; // set only when a document server is wired
const OWNER = DEV_TOKENS.admin;
const RECIPIENT = DEV_TOKENS.user;
const RECIPIENT_EMAIL = DEV_EMAILS.user;
const PREFIX = SUITE_PREFIX.locks;
const name = (tag: string) => uniqueName(tag, PREFIX);

const direntOf = (list: Awaited<ReturnType<typeof listDir>>, n: string) => list.find((d) => d.name === n);

test.describe('SesameFS file locking', () => {
  test.afterEach(async ({ playwright }) => {
    const req: APIRequestContext = await playwright.request.newContext({ baseURL: FE });
    await cleanupTestArtifacts(req, OWNER, PREFIX);
    await req.dispose();
  });

  test('a user lock is visible to other users of the shared library', async ({ browser }) => {
    const ownerCtx: BrowserContext = await browser.newContext({ baseURL: FE });
    const recipientCtx: BrowserContext = await browser.newContext({ baseURL: FE });
    await browserDevLogin(ownerCtx, FE, 'admin');
    await browserDevLogin(recipientCtx, FE, 'user');
    try {
      // Setup: owner creates a library + file, shares rw with the recipient.
      const repoName = name('perceive');
      const repo = await createRepo(ownerCtx.request, OWNER, repoName);
      test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
      if ('skipReason' in repo) return;
      const id = repo.repoId;
      await uploadFile(ownerCtx.request, OWNER, id, '/', 'doc.txt', 'shared content');
      expect((await shareWithUser(ownerCtx.request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);

      // Both users open the library in the real UI.
      const ownerPage = await ownerCtx.newPage();
      const recipientPage = await recipientCtx.newPage();
      await ownerPage.goto(`/library/${id}/${encodeURIComponent(repoName)}/`);
      await expect(ownerPage.getByText('doc.txt').first()).toBeVisible({ timeout: 20_000 });
      await recipientPage.goto(`/library/${id}/${encodeURIComponent(repoName)}/`);
      await expect(recipientPage.getByText('doc.txt').first()).toBeVisible({ timeout: 20_000 });

      // Owner locks the file.
      const locked = await setLock(ownerCtx.request, OWNER, id, '/doc.txt', 'lock');
      expect(locked.status).toBe(200);
      expect(locked.payload.is_locked).toBe(true);

      // Owner sees it as their own lock; recipient sees it locked by someone else.
      const ownerEntry = direntOf(await listDir(ownerCtx.request, OWNER, id), 'doc.txt');
      expect(ownerEntry?.is_locked).toBe(true);
      expect(ownerEntry?.locked_by_me).toBe(true);

      const recipientEntry = direntOf(await listDir(recipientCtx.request, RECIPIENT, id), 'doc.txt');
      expect(recipientEntry?.is_locked).toBe(true);
      expect(recipientEntry?.locked_by_me).toBe(false);
      expect((recipientEntry?.lock_owner || '').length).toBeGreaterThan(0);

      // Owner unlocks; the recipient now sees it unlocked.
      expect((await setLock(ownerCtx.request, OWNER, id, '/doc.txt', 'unlock')).status).toBe(200);
      const afterUnlock = direntOf(await listDir(recipientCtx.request, RECIPIENT, id), 'doc.txt');
      expect(afterUnlock?.is_locked).toBe(false);
    } finally {
      await ownerCtx.close();
      await recipientCtx.close();
    }
  });

  // KNOWN GAP (flagged 2026-06-18): a locked file is NOT protected — no write path
  // checks locked_files, so a different user can overwrite a file someone else locked.
  // This encodes the CORRECT expectation (the write must be rejected); test.fail() keeps
  // the suite green until enforcement lands, then flips to an unexpected pass.
  // See docs/FILE-LOCKING-DESIGN.md.
  test('a locked file rejects writes from a different user (known gap)', { tag: '@bug' }, async ({ browser }) => {
    test.fail(); // expected-to-fail until lock enforcement on writes is implemented
    const ownerCtx = await browser.newContext({ baseURL: FE });
    const recipientCtx = await browser.newContext({ baseURL: FE });
    await browserDevLogin(ownerCtx, FE, 'admin');
    await browserDevLogin(recipientCtx, FE, 'user');
    try {
      const repo = await createRepo(ownerCtx.request, OWNER, name('enforce'));
      if ('skipReason' in repo) {
        test.skip(true, (repo as any).skipReason);
        return;
      }
      const id = repo.repoId;
      await uploadFile(ownerCtx.request, OWNER, id, '/', 'doc.txt', 'owned by admin');
      expect((await shareWithUser(ownerCtx.request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);

      // Owner locks the file.
      expect((await setLock(ownerCtx.request, OWNER, id, '/doc.txt', 'lock')).status).toBe(200);

      // Recipient (a different user) tries to overwrite the locked file. It MUST be
      // rejected (e.g. 403/423). Today there is no enforcement, so this returns 200.
      const status = await uploadFile(recipientCtx.request, RECIPIENT, id, '/', 'doc.txt', 'hijacked', true);
      expect(status, 'overwrite of a file locked by another user must be rejected').not.toBe(200);
    } finally {
      await ownerCtx.close();
      await recipientCtx.close();
    }
  });

  // OnlyOffice collaborative editing. SHOULD: multiple users open the same document
  // and co-edit through OnlyOffice (it merges edits), while sync/direct API writes are
  // blocked by an `online_office` lock that releases when the last editor leaves.
  // Requires an OnlyOffice document server — skipped unless MR_ONLYOFFICE is set.
  test('OnlyOffice: multiple users co-edit while sync/direct writes are blocked', async ({ browser }) => {
    test.skip(!ONLYOFFICE, 'OnlyOffice document server not configured (onlyoffice.enabled=false). Set MR_ONLYOFFICE to run.');
    const ownerCtx = await browser.newContext({ baseURL: FE });
    const recipientCtx = await browser.newContext({ baseURL: FE });
    await browserDevLogin(ownerCtx, FE, 'admin');
    await browserDevLogin(recipientCtx, FE, 'user');
    try {
      const repoName = name('onlyoffice');
      const repo = await createRepo(ownerCtx.request, OWNER, repoName);
      if ('skipReason' in repo) {
        test.skip(true, (repo as any).skipReason);
        return;
      }
      const id = repo.repoId;
      await uploadFile(ownerCtx.request, OWNER, id, '/', 'doc.docx', 'placeholder');
      expect((await shareWithUser(ownerCtx.request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);

      // Intended end-to-end flow once a document server is wired:
      //  1. Both users open doc.docx in the OnlyOffice editor (the SPA renders the
      //     editor iframe); both editor sessions load successfully (co-editing allowed).
      //  2. While open, the file is `online_office`-locked: a desktop/sync-style direct
      //     overwrite by a third party is rejected, but OnlyOffice's save callback is
      //     allowed to publish.
      //  3. When the last editor closes, the online lock is released and the file is
      //     writable again.
      // (Assertions intentionally omitted until a document server is available.)
      expect(id).toBeTruthy();
    } finally {
      await ownerCtx.close();
      await recipientCtx.close();
    }
  });
});
