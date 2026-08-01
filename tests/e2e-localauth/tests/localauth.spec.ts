import { test, expect, request, APIRequestContext } from '@playwright/test';
import { randomUUID } from 'node:crypto';

// Both services are reached over the compose network by service name.
const AUTH_URL = process.env.AUTH_URL ?? 'http://sesameauth:8080';
const API_URL = process.env.API_URL ?? 'http://sesamefs:8080';
const ADMIN_EMAIL = process.env.ADMIN_EMAIL ?? 'superadmin@sesamefs.local';
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD ?? 'BootstrapAdmin123';
// Platform org has unlimited max_users; safe for repeated test runs.
const TEST_ORG_ID = process.env.TEST_ORG_ID ?? '00000000-0000-0000-0000-000000000000';
const MAX_FAILED = Number(process.env.AUTH_LOCAL_MAX_FAILED_ATTEMPTS ?? '5');

let auth: APIRequestContext; // sesameauth login service
let api: APIRequestContext; // sesamefs storage service

test.beforeAll(async () => {
  auth = await request.newContext({ baseURL: AUTH_URL });
  api = await request.newContext({ baseURL: API_URL });
});

test.afterAll(async () => {
  await auth.dispose();
  await api.dispose();
});

function uniqueEmail(prefix: string): string {
  return `${prefix}-${randomUUID().slice(0, 8)}@e2e.example.com`;
}

async function login(email: string, password: string) {
  return auth.post('/api/v2.1/auth/local/login', { data: { email, password } });
}

async function adminToken(): Promise<string> {
  const res = await login(ADMIN_EMAIL, ADMIN_PASSWORD);
  expect(res.status(), 'bootstrap admin must be able to log in').toBe(200);
  return (await res.json()).token as string;
}

async function createUser(token: string, body: Record<string, unknown>) {
  return api.post(`/api/v2.1/org/${TEST_ORG_ID}/admin/users/`, {
    headers: { Authorization: `Token ${token}`, 'Content-Type': 'application/json' },
    data: body,
  });
}

test('auth/methods advertises local auth as enabled', async () => {
  const res = await auth.get('/api/v2.1/auth/methods');
  expect(res.status()).toBe(200);
  expect(await res.json()).toMatchObject({ local: true });
});

test('bootstrap admin login mints a session accepted by the storage service', async () => {
  const res = await login(ADMIN_EMAIL, ADMIN_PASSWORD);
  expect(res.status()).toBe(200);
  const body = await res.json();
  expect(body.token).toBeTruthy();
  expect(body.role).toBe('superadmin');

  // The crux of the separate-container design: a session minted by sesameauth
  // is honored by the independent storage service via the shared sessions table.
  const acct = await api.get('/api2/account/info/', {
    headers: { Authorization: `Token ${body.token}` },
  });
  expect(acct.status()).toBe(200);
  expect((await acct.json()).email).toBe(ADMIN_EMAIL);
});

test('wrong password is rejected with 401', async () => {
  const res = await login(ADMIN_EMAIL, 'definitely-not-the-password');
  expect(res.status()).toBe(401);
});

test('admin creates a user with an explicit password who can then log in', async () => {
  const token = await adminToken();
  const email = uniqueEmail('alice');

  const created = await createUser(token, { email, name: 'Alice', password: 'AlicePass123' });
  expect(created.status(), await created.text()).toBe(201);

  const res = await login(email, 'AlicePass123');
  expect(res.status()).toBe(200);
  const body = await res.json();
  expect(body.email).toBe(email);
  expect(body.must_change_password).toBe(false);

  // Session is usable against the storage service.
  const acct = await api.get('/api2/account/info/', {
    headers: { Authorization: `Token ${body.token}` },
  });
  expect(acct.status()).toBe(200);
});

test('admin creates a user without a password and receives a temp password', async () => {
  const token = await adminToken();
  const email = uniqueEmail('bob');

  const created = await createUser(token, { email, name: 'Bob' });
  expect(created.status(), await created.text()).toBe(201);
  const body = await created.json();
  expect(body.temp_password, 'a temp password must be returned once').toBeTruthy();
  expect(body.must_change_password).toBe(true);

  const res = await login(email, body.temp_password);
  expect(res.status()).toBe(200);
  expect((await res.json()).must_change_password).toBe(true);
});

test('a user can change their password; the old one stops working', async () => {
  const token = await adminToken();
  const email = uniqueEmail('carol');
  const oldPass = 'CarolPass123';
  const newPass = 'CarolNew456';

  expect((await createUser(token, { email, name: 'Carol', password: oldPass })).status()).toBe(201);

  const first = await login(email, oldPass);
  const userToken = (await first.json()).token as string;

  const changed = await auth.post('/api/v2.1/auth/local/change-password', {
    headers: { Authorization: `Token ${userToken}`, 'Content-Type': 'application/json' },
    data: { current_password: oldPass, new_password: newPass },
  });
  expect(changed.status(), await changed.text()).toBe(200);

  expect((await login(email, oldPass)).status()).toBe(401);
  expect((await login(email, newPass)).status()).toBe(200);
});

test('repeated failures lock the account, blocking even the correct password', async () => {
  const token = await adminToken();
  const email = uniqueEmail('dave');
  const pass = 'DavePass123';
  expect((await createUser(token, { email, name: 'Dave', password: pass })).status()).toBe(201);

  // First MAX_FAILED wrong attempts are 401 (invalid), then the actor is locked.
  let sawLockout = false;
  for (let i = 0; i < MAX_FAILED + 1; i++) {
    const res = await login(email, `wrong-${i}`);
    if (res.status() === 429) sawLockout = true;
  }
  expect(sawLockout, 'lockout (429) must trigger after repeated failures').toBe(true);

  // Correct password is now blocked while the lockout window is open.
  const blocked = await login(email, pass);
  expect(blocked.status()).toBe(429);
});
