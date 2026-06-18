/**
 * SesameFS concurrency coverage.
 *
 * SesameFS stores each library as a commit chain (Seafile-style), so simultaneous
 * writers race to promote a new HEAD. These tests assert the system converges:
 * no write is silently lost when many clients hit one library at once.
 *
 *   - parallel library creation -> every library is created with a unique id
 *   - parallel uploads to ONE library -> all files survive (HEAD-promotion retry)
 *   - parallel folder creation in ONE library -> all folders survive
 *   - two users writing the SAME shared library at once -> both sets survive
 *   - file-lock contention between two users (incl. a documented known gap)
 *
 * `test.fail()` marks an assertion of *desired* behaviour the backend currently
 * violates; it keeps the suite green while flagging the gap (and will alarm if
 * the backend is later fixed, so the marker gets removed).
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  accountInfo,
  cleanupTestArtifacts,
  createRepo,
  listDir,
  listDirNames,
  listRepos,
  mkdir,
  setLock,
  shareWithUser,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const ADMIN = DEV_TOKENS.admin;
const USER = DEV_TOKENS.user;
const PREFIX = SUITE_PREFIX.concurrency;
const name = (tag: string) => uniqueName(tag, PREFIX);

// Each test owns its own library (unique name), so tests are independent and may
// run in parallel; the concurrency under test happens *within* each test via
// Promise.all against a single library.
test.describe('SesameFS concurrency', () => {
  test.afterEach(async ({ playwright, baseURL }) => {
    const request: APIRequestContext = await playwright.request.newContext({ baseURL });
    await cleanupTestArtifacts(request, ADMIN, PREFIX);
    await cleanupTestArtifacts(request, USER, PREFIX);
    await request.dispose();
  });

  test('parallel library creation yields unique libraries', async ({ request }) => {
    const N = 3; // free-plan max_libraries=3; this is the concurrent-create ceiling
    const results = await Promise.all(
      Array.from({ length: N }, (_, i) => createRepo(request, ADMIN, name(`conc-lib-${i}`))),
    );
    const created = results.filter((r): r is { repoId: string } => 'repoId' in r);
    test.skip(created.length === 0, 'library creation unavailable on this plan');

    const ids = new Set(created.map((r) => r.repoId));
    expect(ids.size).toBe(created.length); // no duplicate ids handed out

    const mineIds = new Set((await listRepos(request, ADMIN, 'mine')).map((r) => r.repo_id));
    for (const r of created) expect(mineIds.has(r.repoId)).toBe(true);
  });

  test('parallel uploads to one library all survive', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('conc-upload'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;

    const N = 12;
    const statuses = await Promise.all(
      Array.from({ length: N }, (_, i) =>
        uploadFile(request, ADMIN, id, '/', `c_${i}.txt`, `content ${i} ${Date.now()}`),
      ),
    );
    expect(statuses.every((s) => s === 200)).toBe(true);

    const names = await listDirNames(request, ADMIN, id, '/');
    for (let i = 0; i < N; i++) expect(names).toContain(`c_${i}.txt`);
    expect(names.length).toBe(N); // convergence: nothing lost, nothing duplicated
  });

  test('parallel folder creation in one library all survive', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('conc-mkdir'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;

    const N = 10;
    const oks = await Promise.all(
      Array.from({ length: N }, (_, i) => mkdir(request, ADMIN, id, `/folder_${i}`)),
    );
    expect(oks.every(Boolean)).toBe(true);

    const names = await listDirNames(request, ADMIN, id, '/');
    for (let i = 0; i < N; i++) expect(names).toContain(`folder_${i}`);
  });

  test('two users writing the same shared library at once both converge', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('conc-shared'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;

    const share = await shareWithUser(request, ADMIN, id, DEV_EMAILS.user, 'rw');
    expect(share.ok).toBe(true);

    const N = 6;
    const jobs = [
      ...Array.from({ length: N }, (_, i) => uploadFile(request, ADMIN, id, '/', `admin_${i}.txt`, `a${i}`)),
      ...Array.from({ length: N }, (_, i) => uploadFile(request, USER, id, '/', `user_${i}.txt`, `u${i}`)),
    ];
    const statuses = await Promise.all(jobs);
    expect(statuses.every((s) => s === 200)).toBe(true);

    const names = await listDirNames(request, ADMIN, id, '/');
    for (let i = 0; i < N; i++) {
      expect(names).toContain(`admin_${i}.txt`);
      expect(names).toContain(`user_${i}.txt`);
    }
    expect(names.length).toBe(2 * N);
  });

  test('after concurrent lock attempts the file ends locked with a single owner', async ({ request }) => {
    const repo = await createRepo(request, ADMIN, name('conc-lock'));
    test.skip('skipReason' in repo, 'skipReason' in repo ? repo.skipReason : '');
    if ('skipReason' in repo) return;
    const id = repo.repoId;
    await uploadFile(request, ADMIN, id, '/', 'contended.txt', 'lock me');
    await shareWithUser(request, ADMIN, id, DEV_EMAILS.user, 'rw');

    // Both users race to lock the same file.
    const [a, u] = await Promise.all([
      setLock(request, ADMIN, id, '/contended.txt', 'lock'),
      setLock(request, USER, id, '/contended.txt', 'lock'),
    ]);
    expect(a.status).toBe(200);
    expect(u.status).toBe(200);

    // Whatever the race outcome, the file must be locked by exactly one owner.
    const dirent = (await listDir(request, ADMIN, id, '/')).find((d) => d.name === 'contended.txt');
    expect(dirent?.is_locked).toBe(true);
    expect(typeof dirent?.lock_owner).toBe('string');
    expect((dirent?.lock_owner || '').length).toBeGreaterThan(0);
  });

  // KNOWN GAP (flagged 2026-06-16): the lock endpoint does not enforce ownership.
  // A second rw user can silently steal an existing lock (lock_owner is reassigned)
  // and can release another user's lock — both return success:true. This encodes
  // the *safe* expectation; test.fail() keeps the suite green while documenting it.
  // If the backend starts enforcing ownership, Playwright will report this as an
  // unexpected pass so the marker can be removed.
  test('rw user must not be able to steal another user\'s file lock (known gap)', async ({ request }) => {
      test.fail(); // expected-to-fail: documents the lock-ownership gap described above
      const repo = await createRepo(request, ADMIN, name('lock-steal'));
      if ('skipReason' in repo) {
        test.skip(true, repo.skipReason);
        return;
      }
      const id = repo.repoId;
      await uploadFile(request, ADMIN, id, '/', 'owned.txt', 'admin owns this');
      await shareWithUser(request, ADMIN, id, DEV_EMAILS.user, 'rw');

      const adminId = (await accountInfo(request, ADMIN)).email; // owner identity reference
      expect(adminId).toBeTruthy();

      const owner = await setLock(request, ADMIN, id, '/owned.txt', 'lock');
      expect(owner.payload.is_locked).toBe(true);
      const ownerLockId = owner.payload.lock_owner;

      // The recipient attempts to grab the lock the admin already holds.
      await setLock(request, USER, id, '/owned.txt', 'lock');

      // Desired behaviour: ownership is unchanged (the steal was refused).
      const dirent = (await listDir(request, ADMIN, id, '/')).find((d) => d.name === 'owned.txt');
      expect(dirent?.lock_owner).toBe(ownerLockId); // FAILS today: lock_owner got reassigned
  });
});
