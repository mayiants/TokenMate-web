#!/usr/bin/env node

/**
 * Integration tests for AI Portal Go Admin Handlers
 *
 * This script tests the admin handler endpoints against a running server.
 * Run with: node test-integration.js
 *
 * Prerequisites:
 * - Server running at http://localhost:3001
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:3001';

const results = [];

async function test(name, fn) {
  const start = Date.now();
  try {
    const passed = await fn();
    results.push({
      name,
      passed,
      message: passed ? 'OK' : 'Failed',
      duration: Date.now() - start,
    });
  } catch (error) {
    results.push({
      name,
      passed: false,
      message: error instanceof Error ? error.message : String(error),
      duration: Date.now() - start,
    });
  }
}

// Helper to make HTTP requests
async function request(method, path, body, headers) {
  const url = `${BASE_URL}${path}`;
  const res = await fetch(url, {
    method,
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      ...headers,
    },
    body: body ? new URLSearchParams(body).toString() : undefined,
    redirect: 'manual',
  });
  return {
    status: res.status,
    body: await res.text(),
    headers: res.headers,
  };
}

// ========== Page Rendering Tests ==========

async function testPagesRender() {
  console.log('\n=== Page Rendering Tests ===\n');

  await test('Landing page renders', async () => {
    const res = await fetch(`${BASE_URL}/`);
    const html = await res.text();
    return res.status === 200 && html.includes('<!DOCTYPE html>');
  });

  await test('Login page renders', async () => {
    const res = await fetch(`${BASE_URL}/login`);
    const html = await res.text();
    return res.status === 200 && html.toLowerCase().includes('login');
  });

  await test('Register page renders', async () => {
    const res = await fetch(`${BASE_URL}/register`);
    return res.status === 200;
  });

  await test('Forgot password page renders', async () => {
    const res = await fetch(`${BASE_URL}/forgot-password`);
    return res.status === 200;
  });
}

// ========== Admin Handler CSRF Tests ==========

async function testAdminHandlerCSRF() {
  console.log('\n=== Admin Handler CSRF Tests ===\n');

  // All POST handlers should require CSRF token
  await test('HandleCreateModel - requires CSRF token', async () => {
    const res = await request('POST', '/admin/models/create', {
      model_id: 'gpt-4',
      vendor: 'openai',
      name: 'GPT-4',
    });
    // Should return 403 Forbidden for invalid CSRF
    return res.status === 403;
  });

  await test('HandleAddUpstreamKey - requires CSRF token', async () => {
    const res = await request('POST', '/admin/keypool/add', {
      provider: 'openai',
      api_key: 'sk-test-1234',
    });
    return res.status === 403;
  });

  await test('HandleAddProvider - requires CSRF token', async () => {
    const res = await request('POST', '/admin/providers/add', {
      name: 'OpenAI',
      type: 'international',
    });
    return res.status === 403;
  });

  await test('HandleCreatePlan - requires CSRF token', async () => {
    const res = await request('POST', '/admin/subscriptions/plans/create', {
      name: 'Pro Plan',
      price: '9900',
    });
    return res.status === 403;
  });

  await test('HandleCreateRedemptionCode - requires CSRF token', async () => {
    const res = await request('POST', '/admin/redemption/create', {
      name: 'Test Code',
      type: 'balance',
      amount: '1000',
    });
    return res.status === 403;
  });
}

// ========== Admin Page Access Tests ==========

async function testAdminPageAccess() {
  console.log('\n=== Admin Page Access Tests ===\n');

  // Test that admin pages require authentication
  await test('Admin dashboard redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin`, { redirect: 'manual' });
    // Should redirect to login
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin users page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/users`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin models page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/models`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin keypool page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/keypool`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin providers page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/providers`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin subscriptions page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/subscriptions`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });

  await test('Admin redemption page redirects to login', async () => {
    const res = await fetch(`${BASE_URL}/admin/redemption`, { redirect: 'manual' });
    return res.status === 302 && res.headers.get('Location')?.includes('/login');
  });
}

// ========== CSRF Token Tests ==========

async function testCSRFTokenHandling() {
  console.log('\n=== CSRF Token Tests ===\n');

  await test('CSRF token is set in cookie for pages', async () => {
    const res = await fetch(`${BASE_URL}/login`);
    const setCookie = res.headers.get('Set-Cookie');
    return setCookie?.includes('csrf_token=') || false;
  });

  await test('CSRF token in meta tag for pages', async () => {
    const res = await fetch(`${BASE_URL}/login`);
    const html = await res.text();
    return html.includes('csrf-token');
  });
}

// ========== Static Asset Tests ==========

async function testStaticAssets() {
  console.log('\n=== Static Asset Tests ===\n');

  await test('Tailwind CSS loads from CDN', async () => {
    const res = await fetch(`${BASE_URL}/`);
    const html = await res.text();
    return html.includes('cdn.tailwindcss.com');
  });
}

// ========== API Health Tests ==========

async function testAPIHealth() {
  console.log('\n=== API Health Tests ===\n');

  await test('Server responds to requests', async () => {
    const res = await fetch(`${BASE_URL}/`);
    return res.status === 200;
  });

  await test('Error responses are JSON', async () => {
    const res = await request('POST', '/admin/models/create', {
      model_id: 'gpt-4',
    });
    if (res.status !== 403) return false;
    try {
      const body = JSON.parse(res.body);
      return body.error !== undefined;
    } catch {
      return false;
    }
  });
}

// ========== Print Results ==========

function printResults() {
  console.log('\n' + '='.repeat(60));
  console.log('TEST RESULTS');
  console.log('='.repeat(60) + '\n');

  const passed = results.filter((r) => r.passed);
  const failed = results.filter((r) => !r.passed);

  for (const result of results) {
    const icon = result.passed ? '✓' : '✗';
    const duration = `${result.duration}ms`;
    console.log(`${icon} ${result.name.padEnd(50)} ${duration.padStart(8)}`);
    if (!result.passed && result.message !== 'Failed') {
      console.log(`  Error: ${result.message}`);
    }
  }

  console.log('\n' + '-'.repeat(60));
  console.log(`Total:  ${results.length}`);
  console.log(`Passed: ${passed.length}`);
  console.log(`Failed: ${failed.length}`);
  console.log('-'.repeat(60));

  if (failed.length > 0) {
    console.log('\nFailed tests:');
    for (const f of failed) {
      console.log(`  - ${f.name}: ${f.message}`);
    }
    process.exit(1);
  } else {
    console.log('\nAll tests passed!');
    process.exit(0);
  }
}

// ========== Main ==========

async function main() {
  console.log('AI Portal Go - Integration Tests');
  console.log(`Base URL: ${BASE_URL}`);
  console.log('Time:', new Date().toISOString());

  try {
    await testPagesRender();
    await testAdminHandlerCSRF();
    await testAdminPageAccess();
    await testCSRFTokenHandling();
    await testStaticAssets();
    await testAPIHealth();
  } catch (error) {
    console.error('Test suite error:', error);
    process.exit(1);
  }

  printResults();
}

main();
