import { test, expect, request, APIRequestContext } from '@playwright/test';
import { randomUUID } from 'node:crypto';

// Exercises the sys-admin user-management endpoints through the real frontend
// nginx origin — proving that /auth/local/* routes to sesameauth while
// /admin/users/* routes to the storage service, all same-origin.
const FRONTEND = process.env.FRONTEND_URL ?? 'http://frontend:80';
const ADMIN_EMAIL = process.env.ADMIN_EMAIL ?? 'superadmin@sesamefs.local';
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD ?? 'BootstrapAdmin123';

let ctx: APIRequestContext;
let adminToken: string;

test.beforeAll(async () => {
  ctx = await request.newContext({ baseURL: FRONTEND });
  const res = await ctx.post('/api/v2.1/auth/local/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  expect(res.status(), 'admin login via proxy must succeed').toBe(200);
  adminToken = (await res.json()).token;
});

test.afterAll(async () => {
  await ctx.dispose();
});

function uniqueEmail(prefix: string) {
  return `${prefix}-${randomUUID().slice(0, 8)}@e2e.example.com`;
}

test('/auth/methods is served through the proxy by sesameauth', async () => {
  const res = await ctx.get('/api/v2.1/auth/methods');
  expect(res.status()).toBe(200);
  expect(await res.json()).toMatchObject({ local: true });
});

test('sys-admin creates a user WITH a password (via proxy) who can then log in', async () => {
  const email = uniqueEmail('ui-alice');
  const created = await ctx.post('/api/v2.1/admin/users/', {
    headers: { Authorization: `Token ${adminToken}` },
    data: { email, name: 'UI Alice', password: 'UiAlicePass123', role: 'default' },
  });
  expect(created.status(), await created.text()).toBe(201);

  const login = await ctx.post('/api/v2.1/auth/local/login', {
    data: { email, password: 'UiAlicePass123' },
  });
  expect(login.status()).toBe(200);
});

test('sys-admin creates a user WITHOUT a password and gets a temp password', async () => {
  const email = uniqueEmail('ui-bob');
  const created = await ctx.post('/api/v2.1/admin/users/', {
    headers: { Authorization: `Token ${adminToken}` },
    data: { email, name: 'UI Bob', role: 'default' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const body = await created.json();
  expect(body.temp_password).toBeTruthy();

  const login = await ctx.post('/api/v2.1/auth/local/login', {
    data: { email, password: body.temp_password },
  });
  expect(login.status()).toBe(200);
  expect((await login.json()).must_change_password).toBe(true);
});

test('sys-admin reset-password issues a new temp password that works', async () => {
  const email = uniqueEmail('ui-carol');
  expect((await ctx.post('/api/v2.1/admin/users/', {
    headers: { Authorization: `Token ${adminToken}` },
    data: { email, name: 'UI Carol', password: 'CarolOriginal123', role: 'default' },
  })).status()).toBe(201);

  // Reset with no body → server generates a temp password.
  const reset = await ctx.put(`/api/v2.1/admin/users/${email}/set-password/`, {
    headers: { Authorization: `Token ${adminToken}` },
    data: {},
  });
  expect(reset.status(), await reset.text()).toBe(200);
  const body = await reset.json();
  expect(body.temp_password).toBeTruthy();

  // Old password no longer works; the new temp password does.
  expect((await ctx.post('/api/v2.1/auth/local/login', {
    data: { email, password: 'CarolOriginal123' },
  })).status()).toBe(401);
  expect((await ctx.post('/api/v2.1/auth/local/login', {
    data: { email, password: body.temp_password },
  })).status()).toBe(200);
});
