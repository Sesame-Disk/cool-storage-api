# End-to-end tests (Playwright)

Verifies the frontend works across mobile, tablet, and desktop viewports. Tests run against a running frontend container served by nginx.

## Running locally

```bash
# Install Playwright browsers (one-time)
npx playwright install --with-deps chromium

# Bring up the stack
docker compose up -d sesamefs frontend

# Run all e2e
npm run test:e2e

# Mobile only
npm run test:e2e:mobile

# Interactive UI
npm run test:e2e:ui
```

## Running in CI / Docker

```bash
docker compose --profile test up --abort-on-container-exit frontend frontend-e2e
```

The `frontend-e2e` compose service builds Playwright with the chromium browser preinstalled and runs against `http://frontend` inside the docker network.

## Projects (viewports)

| Project              | Device          | Viewport      |
|----------------------|-----------------|---------------|
| `mobile-chromium`    | iPhone 13       | 390x844       |
| `tablet-chromium`    | iPad Pro 11     | 834x1194      |
| `desktop-chromium`   | Desktop Chrome  | 1280x800      |

## Adding a test

Place spec files under `e2e/`. Use `import { test, expect } from '@playwright/test';`.

For tests that require login, use the `loggedInPage` fixture from `auth.fixture.ts` (added in job-009).

## Mobile-friendliness assertions

A common assertion is "no horizontal scroll":

```ts
const overflow = await page.evaluate(
  () => document.documentElement.scrollWidth - window.innerWidth,
);
expect(overflow).toBeLessThanOrEqual(1);
```

Allow 1px to absorb sub-pixel rendering rounding.
