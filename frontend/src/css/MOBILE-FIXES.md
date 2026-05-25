# Mobile/Touch CSS audit — Phase 1 quick wins

Tracks deltas applied to the legacy CSS for mobile-friendliness. Phase 6+
will rebuild these subsystems on Tailwind v4 + responsive primitives.

## Hover-only interactions (`@media (hover: none)` overrides)

Touch devices don't reliably trigger `:hover`. Anywhere critical UI is
hidden by default and revealed on hover, we either:

1. Wrap the reveal in `:hover, :focus-within` (helps keyboard users), AND
2. Add a `@media (hover: none)` block that makes the reveal unconditional.

Files touched in Phase 1:

| File | Pattern fixed |
|---|---|
| `repo-seatable-integration-dialog.css` | `.account-operation-btn` (row action icons) |

## Deferred — to address in Phase 6 / Phase 10

These hover-revealed patterns live primarily inside React components (JS
state controls visibility, not CSS), and rebuilding them is the goal of
Phase 6's `<ResponsiveTable>` + `<Sheet>` primitives:

- `dirent-list-view` / `dirent-list-item` action menu (visible on hover via JS,
  reactstrap `table-hover` row styling).
- `files-activities` per-row actions.
- Various admin tables' per-row icon buttons.

When converting a list view to `<ResponsiveTable>`, ensure on mobile the
row action menu opens into a bottom sheet rather than relying on hover.

## Fixed-width containers fixed in Phase 1

| File | Selector | Old | New |
|---|---|---|---|
| `file-uploader.css` | `.uploader-list-view` | width: 35rem | full-width on <768px |
| `layout.css` | `.cur-view-detail` | width: 300px | full viewport on <768px |
| `shared-file-view.css` | `.shared-file-view-head` | width: 950px | max-width: 100% |
| `upload-link.css` | `#upload-link-panel` | width: 928px | 100% on <768px |
| `subscription.css` | `.subscription-container` | width: 480px | 100% on <768px |
| `search.css` | `.search-container.show` | width: 600px | fixed full-width on <768px |
| `manage-members-dialog.css` | `.add-members-select` | width: 385px | 100% on <768px |

## Touch targets (≥44px) at `(max-width: 767px)`

| File | Selector |
|---|---|
| `seahub.css` | `.side-nav-toggle` (hamburger) |
| `dirent-detail.css` | `.detail-header`, `.detail-control` |
| _More to come in job-007._ |

## Sidebar drawer bug

`side-panel.js` toggled the class `.left-zero` to show the drawer on mobile,
but no matching CSS rule existed — the drawer never visually opened. Fixed
in `layout.css` by adding the rule, plus a backdrop element (`.side-panel-backdrop`)
that taps to close.
