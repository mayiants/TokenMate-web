const { chromium } = require('playwright');
const path = require('path');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const outDir = path.join(__dirname, '..', 'screenshots');

  // Login
  const loginResp = await page.goto('https://api.woodgit.com/login');
  await page.waitForSelector('input[name="email"]', { timeout: 10000 });

  // Extract CSRF token
  const csrfToken = await page.$eval('input[name="csrf_token"]', el => el.value);
  console.log('CSRF token:', csrfToken ? 'found' : 'missing');

  // Use direct POST to login
  const postResp = await page.evaluate(async ({ email, password, csrf }) => {
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = '/login';
    form.style.display = 'none';
    const add = (name, value) => {
      const input = document.createElement('input');
      input.name = name;
      input.value = value;
      form.appendChild(input);
    };
    add('email', email);
    add('password', password);
    add('csrf_token', csrf);
    document.body.appendChild(form);
    form.submit();
  }, { email: 'screenshot-test@woodgit.local', password: 'TestPass123!', csrf: csrfToken });

  await page.waitForTimeout(3000);
  await page.screenshot({ path: path.join(outDir, 'login-after-submit.png') });
  console.log('URL after login submit:', page.url());

  if (page.url().includes('/login')) {
    const bodyText = await page.evaluate(() => document.body.innerText);
    console.log('Login page body:', bodyText.substring(0, 800));
    await browser.close();
    return;
  }

  // Go to enterprise dashboard
  await page.goto('https://api.woodgit.com/enterprise');
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(2000);

  await page.waitForSelector('#chart-tabs', { timeout: 10000 });

  const tabs = await page.$('#chart-tabs');
  await tabs.screenshot({ path: path.join(outDir, 'enterprise-tabs.png') });
  await page.screenshot({ path: path.join(outDir, 'enterprise-dashboard.png'), fullPage: false });

  await browser.close();
  console.log('Screenshots saved to', outDir);
})();
