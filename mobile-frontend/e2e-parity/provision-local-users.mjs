#!/usr/bin/env node
// Idempotently provision the parity harness's local-auth users on a non-dev
// (AUTH_DEV_MODE=false) stack. In non-dev mode only the bootstrap superadmin is
// seeded; the parity suite also needs `admin@` (org admin) and `user@` (end
// user) with known passwords, in a real tenant org (NOT the superadmin-only
// platform org, whose org-admin endpoints 403).
//
// Safe to run repeatedly: users that can already log in are left untouched.
//
// Env:
//   PARITY_BASE_URL           default http://localhost:18073
//   PARITY_BOOTSTRAP_EMAIL    default superadmin@sesamefs.local
//   PARITY_BOOTSTRAP_PASSWORD default dev-token-superadmin
//   PARITY_DEFAULT_ORG_ID     default 00000000-0000-0000-0000-000000000001
//
// Usage: node e2e-parity/provision-local-users.mjs

const BASE = process.env.PARITY_BASE_URL ?? 'http://localhost:18073';
const BOOT_EMAIL = process.env.PARITY_BOOTSTRAP_EMAIL ?? 'superadmin@sesamefs.local';
const BOOT_PASS = process.env.PARITY_BOOTSTRAP_PASSWORD ?? 'dev-token-superadmin';
const ORG = process.env.PARITY_DEFAULT_ORG_ID ?? '00000000-0000-0000-0000-000000000001';

// Users the parity harness logs in as (see helpers/parity-helpers.ts CREDENTIALS).
const USERS = [
  { email: 'user@sesamefs.local', name: 'Test User', role: 'user', password: 'dev-token-user' },
  { email: 'admin@sesamefs.local', name: 'Admin User', role: 'admin', password: 'dev-token-admin' },
];

async function login(email, password) {
  const res = await fetch(`${BASE}/api/v2.1/auth/local/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) return null;
  return res.json();
}

async function main() {
  const boot = await login(BOOT_EMAIL, BOOT_PASS);
  if (!boot?.token) {
    throw new Error(
      `bootstrap superadmin login failed for ${BOOT_EMAIL} at ${BASE} — is the stack up in local-auth mode?`,
    );
  }
  const auth = { Authorization: `Token ${boot.token}`, 'Content-Type': 'application/json' };
  const orgUsers = `${BASE}/api/v2.1/admin/organizations/${ORG}/users/`;

  // Ensure the tenant org can hold both users and isn't capped on libraries /
  // share links: raise max_users and move it onto the unlimited "soft"
  // enforcement profile (the default "hard" profile caps max_libraries at 3,
  // which the parity sharing/upload tests exceed across parallel viewports).
  await fetch(`${BASE}/api/v2.1/admin/organizations/${ORG}/`, {
    method: 'PUT',
    headers: auth,
    body: JSON.stringify({ max_users: -1, quota_policy: 'soft' }),
  });

  for (const u of USERS) {
    if (await login(u.email, u.password)) {
      console.log(`[provision] ${u.email} already provisioned — skipping`);
      continue;
    }
    // Create in the tenant org (role forced to "user" by the endpoint) ...
    await fetch(orgUsers, {
      method: 'POST',
      headers: auth,
      body: JSON.stringify({ email: u.email, name: u.name, password: u.password }),
    });
    // ... set the local password (the org create endpoint does not) ...
    await fetch(`${BASE}/api/v2.1/admin/users/${encodeURIComponent(u.email)}/set-password/`, {
      method: 'PUT',
      headers: auth,
      body: JSON.stringify({ password: u.password }),
    });
    // ... and promote to org admin where required.
    if (u.role === 'admin') {
      await fetch(`${orgUsers}${encodeURIComponent(u.email)}/`, {
        method: 'PUT',
        headers: auth,
        body: JSON.stringify({ role: 'admin', is_staff: true }),
      });
    }
    const ok = await login(u.email, u.password);
    console.log(`[provision] ${u.email} (${u.role}) -> ${ok ? 'ready' : 'FAILED'}`);
    if (!ok) process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error(`[provision] ${err.message}`);
  process.exit(1);
});
