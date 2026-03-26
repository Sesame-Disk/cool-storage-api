# Admin Dashboard Wireframes

**Last Updated**: 2026-03-26
**Status**: Proposed, documentation only

## Current Priority

For now, only these changes are in the active documented scope:

- Correct `/admin/sysinfo` so storage, files, and active users are real
- Keep device information unavailable until real tracking exists
- Add "this month" and "this year" traffic KPIs to the sysadmin endpoint and sysadmin screen
- Change 1-year statistics views to use `group_by=month`

Dashboard visual modernization is explicitly deferred to a later phase.

## Purpose

This document turns the dashboard redesign plan into concrete page structure proposals for:

- Superadmin overview
- Org admin overview
- Shared admin templates

It also carries forward the previously identified KPI corrections:

- `/admin/sysinfo` should stop returning placeholder storage and file totals
- Platform and org dashboards should expose real monthly traffic
- Platform dashboards should add this-year traffic summaries
- Device counts must remain secondary or omitted until device tracking exists

## Approved Proposed Changes

The following items are approved for documentation and later implementation now.

### 1. Correct `/admin/sysinfo` KPI Accuracy

The current sysadmin overview should stop exposing placeholder values where the backend already has authoritative counters.

#### Required changes

- Make `total_storage` real using the platform storage counter
- Make `total_files_count` real using the platform file counter
- Make `active_users_count` real using user lifecycle status, not total-user duplication
- Keep `total_devices_count` and `current_connected_devices_count` unavailable until real device tracking exists

#### UI behavior

- Device cards should be hidden from the default KPI strip, or shown only as unavailable
- Storage, files, and active-users cards should be treated as authoritative dashboard KPIs
- The dashboard should not imply device insight until the platform actually tracks devices

#### Notes

- `users_count` remains useful as a separate total population metric
- `active_users_count` should reflect usable users, not all rows in `users`
- This change affects both the sysadmin info endpoint and the visual trustworthiness of the future dashboard

### 2. Add "This Month" and "This Year" Traffic KPIs

The sysadmin overview should expose traffic summary KPIs directly in the endpoint and in the overview page.

#### Required changes

Add the following fields to the sysadmin overview payload:

- `traffic_month_total`
- `traffic_month_upload`
- `traffic_month_download`
- `traffic_year_total`
- `traffic_year_upload`
- `traffic_year_download`

#### UI behavior

- The sysadmin KPI strip should display at least this-month combined traffic and this-year combined traffic
- Secondary views may display upload/download split explicitly
- The labels should explicitly read `This Month` and `This Year`

#### Year period decision

The agreed meaning is current calendar year to date.

### 3. Fix 1-Year Chart Aggregation

The current admin statistics behavior should not return 365 daily points for the 1-year view.

#### Required changes

- For 1-year mode, frontend requests should use `group_by=month`
- Backend charts should return monthly buckets for yearly views
- 7-day and 30-day modes should remain grouped by day
- Custom date-range submissions may continue using day granularity unless a later UX decision adds automatic aggregation

#### Rationale

- Monthly grouping is more readable for 1-year trend analysis
- Returning daily points for a year makes the charts noisier and operationally less useful
- This change applies to storage and traffic chart views in sysadmin and org-admin statistics pages

## Deferred for Later

The following remains documented, but is not part of the immediate scope:

- Full visual modernization of the dashboard
- Shared dashboard template rollout
- Tailwind-based redesign of admin overview pages
- KPI-first card layout migration across sysadmin and org admin

Those items should be revisited only after the KPI and chart-behavior changes above are implemented.

## Proposed API Contract Additions

These fields are proposed for the sysadmin overview endpoint used by the info/dashboard page.

### Existing fields to keep

- `users_count`
- `repos_count`
- `groups_count`
- `org_count`

### Existing fields to correct

- `active_users_count`
- `total_files_count`
- `total_storage`

### Existing fields to de-emphasize or hide until ready

- `total_devices_count`
- `current_connected_devices_count`

### New fields to add

- `traffic_month_total`
- `traffic_month_upload`
- `traffic_month_download`
- `traffic_year_total`
- `traffic_year_upload`
- `traffic_year_download`

## Shared Template Inventory

This section is future-facing and intentionally deferred until after the KPI corrections and chart aggregation changes.

These templates should be implemented before page-by-page restyling.

### 1. Overview Dashboard Template

Use for the first page of superadmin and org admin.

Sections:

- Header area with title, description, and primary actions
- KPI strip with 4 to 6 cards
- Two-column main content area
- Trend panels
- Risk and lifecycle panels
- Data table card for top entities

### 2. Statistic Page Template

Use for storage, traffic, and activity screens.

Sections:

- Header with title and export action
- Time filter bar
- KPI summary row
- Primary chart panel
- Secondary breakdown table

### 3. Admin List Template

Use for organizations, users, groups, and libraries.

Sections:

- Header with actions
- Filter toolbar
- Table card
- Right-side detail drawer or modal

### 4. Quota and Billing Template

Use for subscription, storage, and traffic views.

Sections:

- KPI cards
- Quota progress bars
- Plan and billing summary card
- Trend chart and top consumers table

## Superadmin Overview Wireframe

### Page Objectives

The superadmin landing page should make platform state obvious within one screen.

### Proposed Structure

#### Row 1: Header

- Title: Platform Overview
- Subtitle: Global storage, traffic, org lifecycle, and operational risk
- Actions:
  - Create organization
  - Export traffic report
  - Open organization management

#### Row 2: KPI Strip

Recommended KPI cards:

- Organizations
- Active users
- Total storage used
- Monthly traffic
- Annual traffic
- Libraries

Notes:

- `Active users` should mean users with usable status, not total users copied into the same field
- `Total storage used` should read from the platform storage counter
- `Libraries` should be paired with a real `total_files_count` in secondary dashboard detail, not a placeholder zero
- `Monthly traffic` should be real current-month combined traffic
- `This year traffic` should mean current calendar year to date
- Device KPIs should not occupy headline card space until tracking exists

#### Row 3: Trends

Left panel:

- Storage growth trend
- Toggle: 30 days / 1 year
- 1-year mode should group by month, not day

Right panel:

- Traffic trend
- Split by upload/download if possible
- Same 30-day / 1-year control
- 1-year mode should group by month, not day

#### Row 4: Risk and Operations

Left panel:

- Organizations near storage quota
- Organizations near traffic quota
- Organizations in deleted grace period

Right panel:

- Platform notices
- Stub/missing data warnings
- Quick admin actions

#### Row 5: Watchlists

Left panel:

- Top organizations by storage

Right panel:

- Top organizations by monthly traffic

Optional lower row:

- Top users by monthly traffic

## Org Admin Overview Wireframe

### Page Objectives

The org admin landing page should feel like a tenant operations dashboard.

### Proposed Structure

#### Row 1: Header

- Title: Organization Overview
- Subtitle: Members, storage, traffic, quotas, and tenant health
- Actions:
  - Add user
  - Create library
  - Download report

#### Row 2: KPI Strip

Recommended KPI cards:

- Members
- Libraries
- Storage used
- Monthly upload traffic
- Monthly download traffic
- Combined monthly traffic

Optional later:

- This year traffic
- Active libraries

#### Row 3: Quotas and Billing

Left panel:

- Storage quota progress
- Traffic quota progress

Right panel:

- Billing cycle
- Plan
- Member quota
- Reset date for monthly traffic

#### Row 4: Usage Trends

Left panel:

- Storage growth trend

Right panel:

- Traffic trend with upload/download split

#### Row 5: Operational Detail

Left panel:

- Top users by traffic

Right panel:

- Top libraries by storage

#### Row 6: Risk and Lifecycle

- Deleted org warning banner when status is deleted
- Deactivated members summary
- Quota risk banner when near limits

## Visual Direction

### Layout

- Wider margins and cleaner vertical rhythm than the current Seahub-style pages
- KPI-first above-the-fold layout
- Card-based composition instead of definition-list-heavy pages

### Typography

- Use a more deliberate type system than the current default stack
- Strong numeric emphasis on KPI cards
- Smaller secondary labels with clearer contrast hierarchy

### Color

- Neutral control-plane base with distinct status colors
- Clear semantic states for healthy, warning, risk, deleted, and deactivated
- Avoid a generic white screen with unstructured borders

### Motion

- Use restrained page-enter and card-reveal transitions
- Avoid decorative motion in high-density data screens

## Tailwind Adoption Guidance

Tailwind CSS is appropriate for these new admin surfaces if introduced with guardrails.

Recommended rules:

- Use Tailwind for new dashboard templates and shared admin components
- Keep old pages functional until they are migrated
- Centralize colors, spacing, radii, and shadows as tokens
- Avoid large one-off utility blobs in page files
- Prefer reusable shell components that encapsulate utility usage

## KPI Source of Truth Backlog

These dashboard cards should not ship with placeholder data.

### Ready to Make Real

- Platform total storage
- Platform total files
- Active users by user status
- Org storage usage
- Org monthly upload/download/combined traffic
- Platform monthly traffic
- Platform this-year traffic

### Can Be Added with Small Backend Work

- Org this-year traffic
- Top org summaries surfaced directly in overview payloads

### Not Ready Yet

- Total devices
- Connected devices

Until device tracking exists, these should stay hidden or explicitly marked unavailable.

## Recommended Next Docs

- Tailwind migration note for legacy frontend
- KPI contract spec for `/admin/sysinfo` and org overview APIs
- Component inventory for admin dashboard primitives
- Admin statistics aggregation rules for 7-day, 30-day, and 1-year modes