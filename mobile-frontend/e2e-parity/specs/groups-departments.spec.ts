import { test, expect } from '@playwright/test';
import {
  API_URL,
  CREDENTIALS,
  artifact,
  authHeaders,
  authStateFile,
  dismissPwaBanner,
  fetchToken,
} from '../helpers/parity-helpers';

// Group & department MANAGEMENT with role-gated access. Sharer/manager is the
// org admin (admin@); the member added is the end user (user@) — same org, which
// group/department membership requires.
const MEMBER = CREDENTIALS.user.email; // user@sesamefs.local

test.describe('Group management (owner)', () => {
  test.use({ storageState: authStateFile('admin') });

  test('owner adds, promotes, removes members and deletes the group', async ({ page, request }) => {
    const token = await fetchToken(request, 'admin');
    // Arrange: create a group via API — the creator (admin@) becomes its owner.
    const res = await request.post(`${API_URL}/api/v2.1/groups/`, {
      headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
      data: { name: artifact('grp') },
    });
    expect(res.ok(), `create group: ${res.status()}`).toBeTruthy();
    const gid = (await res.json()).id;
    try {
      await page.goto(`/groups/${gid}/`);
      await dismissPwaBanner(page);
      await page.getByTestId('members-tab').click();

      // Owner-only affordances are present.
      await expect(page.getByTestId('delete-group-btn')).toBeVisible();

      // Add a member.
      await page.getByTestId('add-member-btn').click();
      await page.getByLabel('Search users').fill('user');
      await page.locator('button', { hasText: MEMBER }).first().click();
      const row = page.locator(`[data-testid="group-member-row"][data-email="${MEMBER}"]`);
      await expect(row).toBeVisible({ timeout: 10_000 });

      // Promote to admin (owner-only) → role badge updates after refresh.
      await page.getByTestId(`member-toggle-admin-${MEMBER}`).click();
      await expect(row).toHaveAttribute('data-role', /admin/i, { timeout: 10_000 });

      // Remove the member.
      await page.getByTestId(`member-remove-${MEMBER}`).click();
      await expect(row).toBeHidden({ timeout: 10_000 });

      // Delete the group → back to the groups list.
      await page.getByTestId('delete-group-btn').click();
      await page.getByTestId('group-confirm-yes').click();
      await page.waitForURL(/\/groups\/?$/, { timeout: 10_000 });

      // Backend confirms it's gone.
      await expect
        .poll(async () => {
          const r = await request.get(`${API_URL}/api/v2.1/groups/`, { headers: authHeaders(token) });
          const gs = await r.json();
          return (Array.isArray(gs) ? gs : []).some((g: any) => String(g.id) === String(gid));
        })
        .toBe(false);
    } finally {
      await request
        .delete(`${API_URL}/api/v2.1/groups/${gid}`, { headers: authHeaders(token) })
        .catch(() => {});
    }
  });
});

test.describe('Group access gating (non-owner member)', () => {
  test.use({ storageState: authStateFile('user') });

  test('a plain member sees Leave (not Delete) and can leave', async ({ page, request }) => {
    const adminToken = await fetchToken(request, 'admin');
    const userToken = await fetchToken(request, 'user');
    // admin@ owns a group and adds user@ as a plain member.
    const gid = (
      await (
        await request.post(`${API_URL}/api/v2.1/groups/`, {
          headers: { ...authHeaders(adminToken), 'Content-Type': 'application/json' },
          data: { name: artifact('grp') },
        })
      ).json()
    ).id;
    await request.post(`${API_URL}/api/v2.1/groups/${gid}/members/`, {
      headers: { ...authHeaders(adminToken), 'Content-Type': 'application/json' },
      data: { email: MEMBER },
    });
    try {
      await page.goto(`/groups/${gid}/`);
      await dismissPwaBanner(page);
      await page.getByTestId('members-tab').click();

      // Access level: a plain member gets Leave, NOT Delete, and no Add.
      await expect(page.getByTestId('leave-group-btn')).toBeVisible();
      await expect(page.getByTestId('delete-group-btn')).toHaveCount(0);
      await expect(page.getByTestId('add-member-btn')).toHaveCount(0);

      // Leaving removes the user from the group.
      await page.getByTestId('leave-group-btn').click();
      await page.getByTestId('group-confirm-yes').click();
      await page.waitForURL(/\/groups\/?$/, { timeout: 10_000 });
      await expect
        .poll(async () => {
          const r = await request.get(`${API_URL}/api/v2.1/groups/`, { headers: authHeaders(userToken) });
          const gs = await r.json();
          return (Array.isArray(gs) ? gs : []).some((g: any) => String(g.id) === String(gid));
        })
        .toBe(false);
    } finally {
      await request
        .delete(`${API_URL}/api/v2.1/groups/${gid}`, { headers: authHeaders(adminToken) })
        .catch(() => {});
    }
  });
});

test.describe('Department management (org admin)', () => {
  test.use({ storageState: authStateFile('admin') });

  test('org admin creates a department, adds a member, and deletes it', async ({ page }) => {
    const deptName = artifact('dept');
    await page.goto('/org/departments/');
    await dismissPwaBanner(page);
    await expect(page.getByTestId('departments-page')).toBeVisible({ timeout: 15_000 });

    // Create.
    await page.getByTestId('new-department-btn').click();
    await page.getByTestId('department-name-input').fill(deptName);
    await page.getByTestId('create-department-submit').click();
    const row = page.locator(`[data-testid="department-row"][data-name="${deptName}"]`);
    await expect(row).toBeVisible({ timeout: 10_000 });

    // Open it → add a member.
    await row.getByText(deptName).click();
    await expect(page.getByTestId('department-detail')).toBeVisible();
    await page.getByTestId('add-member-btn').click();
    await page.getByLabel('Search users').fill('user');
    await page.locator('button', { hasText: MEMBER }).first().click();
    await expect(
      page.locator(`[data-testid="group-member-row"][data-email="${MEMBER}"]`),
    ).toBeVisible({ timeout: 10_000 });

    // Back to the list → delete.
    await page.getByRole('button', { name: 'Departments' }).click();
    await page.getByTestId(`delete-department-${deptName}`).click();
    await page.getByTestId('department-confirm-yes').click();
    await expect(row).toBeHidden({ timeout: 10_000 });
  });
});
