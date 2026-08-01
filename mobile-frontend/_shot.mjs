// Mobile-viewport PWA driver. Usage: node shot.mjs <path> <outName> [--bypass]
import { chromium, devices } from 'playwright';

const path = process.argv[2] || '/login/';
const outName = process.argv[3] || 'shot';
const bypass = process.argv.includes('--bypass');
const BASE = 'http://localhost:4321';
const OUT = '/tmp/claude-1001/-workspace-cool-storage-api/7cce162b-13fc-422e-ac2d-dae727d39f29/scratchpad';

const iphone = devices['iPhone 13'];
const browser = await chromium.launch({ args: ['--no-sandbox'] });
const ctx = await browser.newContext({ ...iphone });
const page = await ctx.newPage();

const errors = [];
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
page.on('pageerror', (e) => errors.push('PAGEERROR: ' + e.message));

if (bypass) {
  await page.goto(BASE + '/login/');
  await page.evaluate(() => {
    localStorage.setItem('dev_bypass', '1');
    localStorage.setItem('seahub_token', 'dev-bypass-token');
  });
}

await page.goto(BASE + path, { waitUntil: 'networkidle' }).catch(() => {});
await page.waitForTimeout(1200);
await page.screenshot({ path: `${OUT}/${outName}.png`, fullPage: false });
console.log('URL:', page.url());
console.log('TITLE:', await page.title());
console.log('CONSOLE ERRORS:', errors.length ? errors.slice(0, 10).join('\n  ') : 'none');
await browser.close();
