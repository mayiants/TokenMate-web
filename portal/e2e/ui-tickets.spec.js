const { test, expect } = require('@playwright/test');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
  password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
};
const USER = {
  email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com',
  password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!',
};

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`);
  await expect(page.locator('input[name="email"]')).toBeVisible();
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await Promise.all([
    page.waitForURL(/\/dashboard/),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
}

async function shot(page, name) {
  try { await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true }); } catch (e) {}
}

// End-to-end smoke: user opens a ticket (via API for determinism, then verifies
// the /support UI renders it), an admin replies from /admin/tickets, and the
// user sees the reply. Self-cleaning: the ticket is left 'closed'.
test.describe('Support tickets smoke', () => {
  const subject = `E2E-SMOKE-Ticket-${Date.now()}`;
  let ticketId = null;

  test('user can create a ticket and see it in /support', async ({ page }) => {
    await login(page, USER);

    // Create via the user API (exercises the real create handler).
    const res = await page.evaluate(async (subj) => {
      const r = await fetch('/api/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ subject: subj, category: 'general', priority: 'normal', body: 'E2E smoke: please ignore.' }),
      });
      return { status: r.status, json: await r.json().catch(() => ({})) };
    }, subject);
    expect(res.status).toBe(200);
    expect(res.json.success).toBeTruthy();
    expect(res.json.id).toBeTruthy();
    ticketId = res.json.id;

    // Upload an attachment (multipart) to the ticket.
    const up = await page.evaluate(async (id) => {
      const fd = new FormData();
      const blob = new Blob(['e2e attachment content'], { type: 'text/plain' });
      fd.append('files', blob, 'e2e-note.txt');
      const r = await fetch(`/api/tickets/${id}/attachments`, { method: 'POST', credentials: 'same-origin', body: fd });
      return { status: r.status, json: await r.json().catch(() => ({})) };
    }, ticketId);
    expect(up.status).toBe(200);
    expect(up.json.success).toBeTruthy();
    expect((up.json.attachments || []).length).toBe(1);
    const attId = up.json.attachments[0].id;

    // Download it back and verify the bytes.
    const dl = await page.evaluate(async ({ id, aid }) => {
      const r = await fetch(`/api/tickets/${id}/attachments/${aid}`, { credentials: 'same-origin' });
      return { status: r.status, text: await r.text() };
    }, { id: ticketId, aid: attId });
    expect(dl.status).toBe(200);
    expect(dl.text).toContain('e2e attachment content');

    // The ticket shows up on the /support page.
    await page.goto(`${BASE_URL}/support`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('#st-tbody')).toContainText(subject, { timeout: 10000 });
    await shot(page, 'ticket-smoke-01-user-list');
  });

  test('admin sees the ticket and replies', async ({ page }) => {
    test.skip(!ticketId, 'ticket not created');
    await login(page, ADMIN);
    await page.goto(`${BASE_URL}/admin/tickets`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('#at-tbody')).toContainText(subject, { timeout: 10000 });

    // Reply via the admin API (the UI reply path calls the same endpoint).
    const res = await page.evaluate(async (id) => {
      const r = await fetch(`/api/admin/tickets/${id}/messages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ body: 'E2E smoke reply from support.' }),
      });
      return { status: r.status, json: await r.json().catch(() => ({})) };
    }, ticketId);
    expect(res.status).toBe(200);
    expect(res.json.success).toBeTruthy();
    await shot(page, 'ticket-smoke-02-admin-queue');
  });

  test('user sees the admin reply, then closes the ticket', async ({ page }) => {
    test.skip(!ticketId, 'ticket not created');
    await login(page, USER);

    const detail = await page.evaluate(async (id) => {
      const r = await fetch(`/api/tickets/${id}`, { credentials: 'same-origin' });
      return await r.json();
    }, ticketId);
    const bodies = (detail.messages || []).map((m) => m.body).join(' | ');
    expect(bodies).toContain('E2E smoke reply from support.');
    // Internal notes must never leak to the user surface.
    for (const m of detail.messages || []) {
      expect(m.internal_note).toBeFalsy();
    }

    // Clean up: close the ticket.
    const res = await page.evaluate(async (id) => {
      const r = await fetch(`/api/tickets/${id}/close`, { method: 'POST', credentials: 'same-origin' });
      return r.status;
    }, ticketId);
    expect([200, 0]).toContain(res);
  });
});
