# Dashboard Redesign Plan

**Last Updated**: 2026-03-26
**Status**: Proposed, documentation only

## Current Scope Decision

The dashboard redesign remains a later-phase initiative.

For now, the active documented scope is limited to:

- Correcting sysadmin KPI accuracy in `/admin/sysinfo`
- Adding "this month" and "this year" traffic KPIs to the sysadmin overview payload and screen
- Changing 1-year charts to use monthly aggregation instead of 365 daily points

Visual modernization, layout changes, shared template rollout, and Tailwind-led redesign are deferred until after those functional dashboard improvements are completed.

## Goal

Modernize the superadmin and org admin dashboards with a cleaner information hierarchy, stronger visual design, and reusable layout primitives, without changing backend semantics or breaking existing admin workflows.

This is a design and implementation planning document only. No frontend rewrite is included here.

## Why This Exists

The current admin UI works, but it still looks and behaves like a legacy Seahub-derived panel:

- Dense screens with weak visual hierarchy
- KPI and system health information spread across multiple views
- Styling based on Bootstrap-era patterns that feels dated
- Repeated admin page structures without a clear shared dashboard shell
- Statistics views that expose data but do not present it as a modern operational dashboard

The intent is to move toward dashboards that feel like a product control plane rather than a legacy admin console.

## Scope

This redesign initiative covers:

- Superadmin dashboard
- Org admin dashboard
- Shared dashboard shell, cards, filters, section headers, and KPI modules
- Statistics views for storage, traffic, users, and organization health
- Common page templates for list/detail/admin workflows

This initiative does not yet include:

- End-user library/file browsing UI
- Login page redesign
- Mobile app redesign
- Backend API contract changes, except where new dashboard metrics are explicitly approved

## Product Direction

### Superadmin Dashboard

The superadmin experience should answer these questions immediately:

- What is the current platform state?
- Are storage and traffic trends healthy?
- Which organizations need attention?
- Are there quota, lifecycle, or audit risks?
- What actions does the operator need to take today?

Recommended default sections:

- Top KPI strip: organizations, active users, total storage, monthly traffic, yearly traffic
- Platform health: storage growth, traffic growth, queue or service health, system warnings
- Organization watchlist: recently created, near quota, deactivated, deleted in grace period
- Activity panels: storage trend, traffic trend, top orgs by usage, top users by traffic
- Action center: shortcuts for org creation, quota changes, lifecycle actions, reports

### Org Admin Dashboard

The org admin experience should answer these questions immediately:

- How much storage and traffic has this org consumed?
- Are we close to member, storage, or traffic limits?
- Which users or libraries drive the most usage?
- Are there deleted or deactivated lifecycle states that need action?
- What admin operations are most common for this tenant?

Recommended default sections:

- Top KPI strip: members, libraries, storage used, monthly upload, monthly download, combined traffic
- Quota status: storage, monthly traffic, member quota, billing cycle
- Usage breakdown: top users, top libraries, trend charts
- Admin actions: add user, create library, quota management, traffic reports
- Risk and status area: deleted-state warnings, deactivated users, pending cleanup or policy reminders

## Design Principles

- Preserve admin density, but replace clutter with hierarchy.
- Put the most important numbers above navigation-heavy content.
- Use a shared component vocabulary across superadmin and org admin.
- Keep the visual style modern, operational, and product-grade.
- Prefer composable cards and panels over one-off page layouts.
- Avoid a generic white-label SaaS look; the UI should feel intentional.

## Proposed Frontend Architecture

### Shared Template System

Build a reusable dashboard template layer that both admin areas can share.

Suggested template primitives:

- `AdminDashboardShell`
- `DashboardHeader`
- `KpiCard`
- `MetricDelta`
- `ChartPanel`
- `StatusPanel`
- `ActionPanel`
- `DataTableCard`
- `FilterToolbar`
- `WarningBanner`
- `EmptyState`

Suggested template families:

- Overview dashboard template
- Statistic page template
- List-and-detail admin template
- Quota and subscription template
- Lifecycle/risk template

This should reduce duplication between sys-admin and org-admin pages and make later UI modernization cheaper.

### Styling Direction

Tailwind CSS is a valid candidate for the redesign.

Current repository state:

- The legacy `frontend` app already declares `tailwindcss` in `package.json`
- The newer `mobile-frontend` stack already uses Tailwind 4 and modern React tooling

That means Tailwind is technically aligned with the repo and can be adopted incrementally.

Recommended styling approach:

- Use Tailwind for new dashboard surfaces and new shared admin components
- Keep legacy pages working during migration
- Introduce design tokens first: color, spacing, radius, shadows, typography, chart palette
- Avoid mixing ad hoc Tailwind utilities with legacy CSS without clear boundaries
- Prefer a small set of semantic wrapper components so the UI does not become utility-sprawl

If Tailwind is adopted in the legacy `frontend` app, do it as an additive migration, not a big-bang restyle.

## Data Requirements

The redesigned dashboards will be strongest if they surface real platform KPIs instead of placeholders.

High-value backend fields for the future dashboard:

- Real platform storage total
- Real platform file count
- Real active user count
- Monthly traffic totals
- This-year traffic totals
- Org quota risk indicators
- Deleted/deactivated org lifecycle visibility

### Documented KPI Correction Scope

The following backend-facing dashboard corrections are part of the documented next phase:

- Correct `/admin/sysinfo` so storage, files, and active users are real
- Keep device counts unavailable until tracking exists
- Add monthly traffic KPI fields to the sysadmin overview payload
- Add this-year traffic KPI fields to the sysadmin overview payload
- Adjust 1-year chart views to use monthly aggregation rather than daily points

Known current gap:

- Device counts are still stubbed and should not be presented as a primary KPI until tracking exists

## Proposed Rollout Phases

Before Phase 1 of visual redesign starts, the currently approved KPI and chart-behavior fixes should be completed first.

### Phase 1: Information Architecture

- Define dashboard sections for superadmin and org admin
- Freeze the KPI list for each role
- Agree on which backend fields are authoritative and which are placeholders
- Approve a visual direction and component inventory

### Phase 2: Shared Template Layer

- Introduce shared dashboard shell and card primitives
- Define tokens for color, typography, spacing, elevation, charts, and states
- Build first reusable templates without replacing all pages yet

### Phase 3: Superadmin Overview Rewrite

- Replace the current superadmin info view with a modern dashboard overview
- Add platform KPI cards and trend panels
- Add org lifecycle watchlist and traffic/storage sections

### Phase 4: Org Admin Overview Rewrite

- Replace current org admin info view with a modern tenant operations dashboard
- Surface quotas, billing cycle, trend charts, and top consumers

### Phase 5: Statistics and Detail Pages

- Normalize chart layouts and filters
- Improve yearly views to group by month instead of returning 365 daily points
- Align tables, exports, and report pages with the new admin templates

## Template Strategy

The next implementation step should not be a direct restyle of individual pages.

Instead, create templates first:

1. A superadmin dashboard template
2. An org admin dashboard template
3. A shared KPI card system
4. A shared chart and filter container
5. A shared risk/status panel system

Once those templates exist, individual admin pages can be migrated onto them in controlled slices.

## Risks

- Mixing legacy Bootstrap patterns and Tailwind utilities without clear ownership will create visual inconsistency.
- Rewriting page-by-page without shared templates will duplicate work.
- Adding too many dashboard modules before validating the data model will produce attractive but misleading screens.
- Device-related cards should stay out of the headline dashboard until real device tracking exists.

## Recommendation

Proceed in this order:

1. Fix the backend KPI accuracy for platform and org dashboards
2. Define a shared admin dashboard template system
3. Pilot the new visual language on superadmin overview first
4. Reuse the same primitives for org admin overview
5. Migrate statistics pages after the overview templates stabilize

## Candidate Follow-Up Deliverables

- Wireframe document for superadmin dashboard
- Wireframe document for org admin dashboard
- Tailwind adoption note for legacy `frontend`
- Shared component inventory for admin dashboards
- Implementation checklist for first template slice