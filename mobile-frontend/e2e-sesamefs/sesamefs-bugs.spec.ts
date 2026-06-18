/**
 * SesameFS BUG fix-targets — the "definition of done" for known issues.
 *
 * Every test here is tagged @bug and asserts the CORRECT, post-fix behavior with
 * NO test.fail() marker — so today they FAIL (the bugs are still present) and they
 * turn GREEN once the corresponding fix lands. They are the counterpart to the
 * @bug "proof" tests (which use test.fail() to show the bug exists today):
 *
 *   - proofs (test.fail, elsewhere): green now, alarm when fixed → remove the proof.
 *   - fix-targets (here): red now, green when fixed → the fix is done.
 *
 * Both are excluded from the normal suite and run only with `--bugs`
 * (./scripts/run-mr-cluster.sh test --bugs). See:
 *   - docs/BUG-FILE-DETAIL-MODIFIER-20260618.md
 *   - docs/FILE-LOCKING-DESIGN.md
 */
import { expect, test } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  authHeaders,
  cleanupTestArtifacts,
  createRepo,
  downloadFileContent,
  fileDetail,
  listDir,
  setLock,
  shareWithUser,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const OWNER = DEV_TOKENS.admin;
const RECIPIENT = DEV_TOKENS.user;
const RECIPIENT_EMAIL = DEV_EMAILS.user;
const OWNER_UID = '00000000-0000-0000-0000-000000000001'; // admin
const RECIPIENT_UID = '00000000-0000-0000-0000-000000000002'; // user
const PREFIX = SUITE_PREFIX.bugs;
const name = (tag: string) => uniqueName(tag, PREFIX);

const direntOf = (list: Awaited<ReturnType<typeof listDir>>, n: string) => list.find((d) => d.name === n);

test.describe('SesameFS bug fix-targets (@bug — fail until fixed)', () => {
  test.afterEach(async ({ playwright, baseURL }) => {
    const request = await playwright.request.newContext({ baseURL });
    await cleanupTestArtifacts(request, OWNER, PREFIX);
    await request.dispose();
  });

  // FIX TARGET for docs/BUG-FILE-DETAIL-MODIFIER-20260618.md
  // GetFileDetail must report the file's real author, not the requesting user.
  test('file/detail attributes the real author regardless of who asks', { tag: '@bug' }, async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('modifier'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'authored.txt', 'written by the owner');
    expect((await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);

    // The recipient asks for the detail; the modifier must identify the OWNER (admin),
    // never the requester. FAILS today (returns the caller, i.e. the recipient).
    const detail = await fileDetail(request, RECIPIENT, id, '/authored.txt');
    expect(detail.status).toBe(200);
    expect(detail.payload.last_modifier_email).toContain(OWNER_UID);
    expect(detail.payload.last_modifier_email).not.toContain(RECIPIENT_UID);
  });

  // FIX TARGET for docs/FILE-LOCKING-DESIGN.md (lock ownership)
  // A lock must not be steal-able or releasable by a non-owner.
  test('a file lock cannot be stolen or released by another user', { tag: '@bug' }, async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('lock-own'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'doc.txt', 'owner content');
    expect((await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);

    // Owner locks the file.
    expect((await setLock(request, OWNER, id, '/doc.txt', 'lock')).status).toBe(200);

    // Recipient tries to grab the lock — must NOT succeed in taking ownership.
    await setLock(request, RECIPIENT, id, '/doc.txt', 'lock');
    let entry = direntOf(await listDir(request, OWNER, id), 'doc.txt');
    expect(entry?.is_locked).toBe(true);
    expect(entry?.lock_owner).toContain(OWNER_UID); // FAILS today: lock got reassigned to the recipient

    // Recipient tries to release the owner's lock — must be refused; lock survives.
    await setLock(request, RECIPIENT, id, '/doc.txt', 'unlock');
    entry = direntOf(await listDir(request, OWNER, id), 'doc.txt');
    expect(entry?.is_locked).toBe(true); // FAILS today: the recipient's unlock removed it
  });

  // FIX TARGET for docs/FILE-LOCKING-DESIGN.md (write enforcement)
  // A file locked by one user must be write-protected against everyone else.
  test('a locked file is write-protected from other users (content preserved)', { tag: '@bug' }, async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('lock-enforce'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'doc.txt', 'OWNER-ORIGINAL');
    expect((await shareWithUser(request, OWNER, id, RECIPIENT_EMAIL, 'rw')).ok).toBe(true);
    expect((await setLock(request, OWNER, id, '/doc.txt', 'lock')).status).toBe(200);

    // Recipient overwrites the locked file — must be rejected.
    const status = await uploadFile(request, RECIPIENT, id, '/', 'doc.txt', 'HIJACKED', true);
    expect(status, 'overwrite of a file locked by another user must be rejected').not.toBe(200);

    // And the owner's content must be intact.
    const dl = await downloadFileContent(request, OWNER, id, '/doc.txt');
    expect(dl.status).toBe(200);
    expect(dl.body).toBe('OWNER-ORIGINAL');
  });

  // FIX TARGET for docs/BUG-LANGUAGE-LIST-ENGLISH-ONLY-20260618.md
  // The bootstrap langList must offer the translated locales, not just English.
  test('profile language list offers more than English (translations exist)', { tag: '@bug' }, async ({ request }) => {
    const res = await request.get('/api/v2.1/bootstrap/', { headers: authHeaders(OWNER) });
    expect(res.status()).toBe(200);
    const data = await res.json().catch(() => ({}));
    const langList = (data?.app_page_options?.langList ?? []) as Array<{ langCode?: string }>;
    expect(Array.isArray(langList)).toBe(true);
    // Translations exist for en, zh-CN, fr, de, cs, es, es-AR, es-MX, ru — see the bug doc.
    expect(langList.length, 'language list should offer the translated locales, not only English').toBeGreaterThan(1);
  });

  // FIX TARGET for docs/BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md
  // A share link requested as internal/org-only must not be openable anonymously.
  // Today share links are public token-only, so the scope is ignored.
  test('an internal/org-scoped share link is not accessible anonymously', { tag: '@bug' }, async ({ request }) => {
    const repo = await createRepo(request, OWNER, name('slink-internal'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, OWNER, id, '/', 'secret.txt', 'internal only');

    // Request an INTERNAL / org-scoped link (the scope hint is ignored today).
    const createRes = await request.post('/api/v2.1/share-links/', {
      headers: { ...authHeaders(OWNER), 'Content-Type': 'application/json' },
      data: {
        repo_id: id,
        path: '/',
        scope: 'internal',
        link_type: 'internal',
        permissions: JSON.stringify({ can_edit: false, can_download: true }),
      },
    });
    test.skip(!createRes.ok(), `share link creation unavailable: ${createRes.status()}`);
    const token = (await createRes.json())?.token as string;
    expect(typeof token).toBe('string');

    // Anonymous access to an internal link must be refused. FAILS today (public token).
    const anon = await request.get(`/api/v2.1/share-links/${encodeURIComponent(token)}/dirents/?p=/`);
    expect(anon.status(), 'an internal/org-scoped link must reject anonymous access').not.toBe(200);
  });
});
