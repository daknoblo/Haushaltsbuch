# Haushaltsbuch – Agent Instructions

## Purpose

Haushaltsbuch is a self-hosted household budget book. It records recurring and
one-off bookings, normalizes them to a monthly figure, splits them between the
people of a household and derives a monthly overview, a twelve-month trend, a
fixed-cost and savings dashboard, a Sankey flow diagram and PDF exports. It is a
planning tool: figures are entered by hand, there is no bank connection and no
import.

## Shared conventions

Repository-wide conventions are defined in `daknoblo/REPO-STANDARD.md`. That
document wins over this file unless a deviation is listed below.

## Domain model

- **Household** – the top-level container. Exactly one is active at a time; the
  active id lives in the `app_state` table.
- **Member** – a person of a household who can carry booking shares. Has a color
  used throughout the UI.
- **Section** – a display grouping for expense bookings (housing, insurance, …).
- **Category** – **mandatory** on every booking. Carries a `classification`
  (`income`/`expense`) so an income cannot be filed under an expense category,
  plus a color that drives the breakdowns and the Sankey diagram.
- **Tag** – free, cross-cutting label, N:M via `booking_tags`. Tags are the
  escape hatch; resist adding further enums once they exist.
- **Booking** – the single unit of planning, replacing the former split between
  expenses and incomes so that a figure is maintained in exactly one place. A
  booking has a `direction` (`income`/`expense`), a non-negative amount, a
  frequency (`once`/`weekly`/`monthly`/`quarterly`/`yearly`) with an interval, an
  active range, a cost nature (fixed/variable) and a budget class
  (need/want/saving).
- **BookingSplit** – a member's share. `split_mode` decides how `value` is read:
  ignored for `equal`, a percentage for `percent`, cents for `fixed`.

Recurrence is **evaluated on the fly, never materialized into rows**. A planning
tool has no actuals to reconcile against, so generated occurrences would buy
nothing and cost a scheduler. `internal/calc` spreads a recurring amount evenly
across the months it covers, so a yearly premium contributes a twelfth every
month; a one-off counts only in the month it falls into.

Money is stored as `int64` cents everywhere. Amounts are parsed and rendered in
German notation by `internal/web/format.go`.

Every write is scoped to the active household in SQL — the household id is part
of the store method signature, and section and category references are resolved
through a sub-select so a forged id cannot cross household boundaries.

## Repository-specific configuration

Beyond the mandatory base set (`HB_HTTP_ADDR`, `HB_DATA_DIR`, `HB_LOG_LEVEL`,
`TZ`) there are no environment variables. The application has no secrets and no
outbound connections.

## Non-goals

- **No bank connection** (FinTS/PSD2) and no CSV import. Certification effort and
  attack surface do not fit an app that runs without authentication.
- **No multi-currency and no double-entry bookkeeping.** Firefly III exists for
  that; this project stays a household budget book.
- **No authentication.** Operated on a private network behind a reverse proxy.
- **No actuals tracking yet.** The app compares planned figures, not individual
  transactions.

## Releases

Every push to `main` publishes a moving `:latest` image, a `v*.*.*` tag publishes
the pinned semver tags. Both run the identical build, so a release can never
differ from what `main` was already producing. `make release BUMP=major|minor|patch`
computes the next version and opens an editor for the tag annotation — that
annotation becomes the body of the GitHub release, so write it properly.

## Deviations from the standard

- **`go.mod` pins a patch version and carries a `toolchain` directive.** The
  standard requires `go 1.26` and no toolchain line. `github.com/johnfercher/maroto/v2`,
  which renders the PDF exports, declares `go 1.26.1`, so `go mod tidy` raises the
  directive and a plain `go 1.26` no longer builds. Without `toolchain go1.26.6`,
  `setup-go` would then install exactly 1.26.1, whose standard library carries
  known advisories, and the mandatory `govulncheck` step would fail. The
  directive is removed as soon as maroto relaxes its requirement.
- **`darkMode: "class"` with a light palette.** waim and CFTM are dark-only. This
  app keeps a theme toggle, so every component class carries a `dark:` variant.
- **Expense rows are collapsible.** The editor has more than ten fields per
  booking; rendering them all at once made the page unreadable. Rows therefore
  ship as a one-line summary inside a `<details>` element and the open state
  travels through the auto-save round trip in a hidden field.
- **Tailwind only scans the templates and `viewmodel.go`.** Tailwind matches bare
  words anywhere in a scanned file, so ordinary prose in a Go comment would emit
  a utility and break the "CSS is up to date" check on an unrelated edit.
- **The Sankey diagram is laid out in Go and emitted as plain SVG.** The CSP
  forbids `unsafe-eval`, and a charting library would be a large dependency for
  one diagram. `internal/calc/sankey.go` computes node positions and ribbon
  paths; a node's value is the larger of its inflow and outflow, never the sum.
- **`contextcheck` is disabled for `internal/web`.** templ components receive the
  context through `Component.Render`, which the linter cannot follow, so it
  reports every render call.
- **`misspell` is disabled for `internal/i18n/catalog_de.go`.** The German
  catalog contains German words by definition.
