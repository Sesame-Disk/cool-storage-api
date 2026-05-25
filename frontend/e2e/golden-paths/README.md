# Golden path e2e tests

The critical user journeys that must keep working through the entire
modernization. Each test runs at three viewports (`mobile-chromium`,
`tablet-chromium`, `desktop-chromium`).

| Spec | Verifies |
|---|---|
| `login.spec.ts` | Login page renders, no overflow at all viewports |
| `repo-browse.spec.ts` | Authenticated user reaches a library view |
| `hamburger.spec.ts` | Mobile hamburger opens drawer; backdrop closes; hidden on desktop |

Auth-required tests use the `loggedInPage` fixture from
`../auth.fixture.ts`. If the test environment doesn't have a working
backend / login flow, these tests **skip** rather than fail — letting
the suite stay green on environments where only the UI is available.

To run:

```bash
# All viewports
npm run test:e2e

# Just mobile
npm run test:e2e:mobile

# Live UI
npm run test:e2e:ui
```

Backend must be reachable at `DESKTOP_BASE_URL` (default
`http://localhost:3000`). In docker compose, the `frontend-e2e` service
points at `http://frontend`.

## Adding more golden paths

Phase 0 covers the lightest set. Subsequent jobs (upload, share, logout)
should add specs in this directory using the same `loggedInPage`
fixture. Always include a "no horizontal overflow" assertion to protect
against mobile regressions.
