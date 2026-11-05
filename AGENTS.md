# Haushaltsbuch – Agent Instructions

## Purpose

Haushaltsbuch is a self-hosted household budget book. It records recurring and
one-off bookings, normalizes them to a monthly figure, splits them between the
people of a household and derives a monthly overview, a trend chart, a
fixed-cost and savings dashboard, a settlement between the members, a Sankey
flow diagram and PDF exports. It is a planning tool: figures are maintained as plans, with reviewed PDF, CSV and XLSX statement imports
for actual payments. There is no direct bank connection.

## Shared conventions

Repository-wide conventions are defined in `daknoblo/REPO-STANDARD.md`. That
document wins over this file unless a deviation is listed below.

## UI design principles

Contextually unavailable actions stay visible, visually muted and functionally
disabled rather than disappearing. Keep their place in the layout, use native
`disabled` controls where possible, and explain the restriction when helpful.
Visual disabling never replaces server-side validation.

## Domain model

- **Household** – the top-level container. Exactly one is active at a time; the
  active id lives in the `app_state` table.
- **Member** – a person of a household who can carry booking shares. Has a color
  used throughout the UI, and a name that is mandatory because a nameless person
  cannot be told apart in a split.
- **Category** – **mandatory** on every booking and the only grouping there is;
  the former sections were dropped because a category already says what kind of
  cost something is. Carries a `classification` (`income`/`expense`) so an
  income cannot be filed under an expense category, plus a color and an icon key
  that drive the breakdowns, the lists and the Sankey diagram.
- **Tag** – free, cross-cutting label, N:M via `booking_tags`. Tags are the
  escape hatch; resist adding further enums once they exist.
- **Booking** – the single unit of planning, replacing the former split between
  expenses and incomes so that a figure is maintained in exactly one place. A
  booking has a `direction` (`income`/`expense`), a non-negative amount, a
  frequency (`once`/`weekly`/`monthly`/`quarterly`/`yearly`) with an interval, a
  `due_point` (start/mid/end of month) for recurring ones, an active range, a
  cost nature (fixed/variable), a budget class (need/want/saving) and a
  `payer_member_id` – who fronts the money, without which no settlement can be
  computed.
- **BookingSplit** – a member's share. `split_mode` decides how `value` is read:
  ignored for `equal`, a percentage for `percent`, cents for `fixed`.
- **Retired booking** – a price-change predecessor remains in historical
  reports but is excluded from annual carry-over, including at SQL level.
  `retired` is read-only for normal booking writes and is preserved by backups.
- **BookingOverride** – replaces a booking's amount for a date range, so an
  introductory price is expressible without a second booking. The last matching
  override wins.
- **BankAccount / BankImport / BankTransaction** – statement provenance and
  reviewed actual payments, kept separate from plans. Accepted allocations are
  integer-cent snapshots. Transfers between own accounts never count as income
  or expenses. Every upload is reconciled against current payments per account,
  including pending, accepted, ignored and transfer decisions. Matching is
  count-aware: an existing payment satisfies at most one incoming occurrence.
  Preserve `source_fingerprint` across review edits so reimports do not recreate
  corrected payments. Files and completed file cards are not retained; internal
  import rows carry only the provenance needed by the payments.
  Use `store.ReconcileBankImports` for uploads, not strict batch creation.
  Each member has exactly one default bank account and may have additional
  accounts. Member creation and default-account creation are atomic; upgrades
  and old backup restores reuse existing accounts without changing their IDs.
  Only unused non-default accounts can be deleted individually. Deleting an
  unused member removes their unused accounts; history still blocks deletion.
- **Reconciled month** – an explicit household-wide completeness assertion.
  Pending rows block closure; current/future months stay open. Closed months
  must be reopened before importing or reviewing additional payments.

Recurrence is **evaluated on the fly, never materialized into rows**. Imported
actual payments are independent statement rows, not generated occurrences.
`internal/calc` spreads a recurring amount evenly
across the months it covers, so a yearly premium contributes a twelfth every
month; a one-off counts only in the month it falls into.

Every report can be built for the whole household or for a single member. In
the member scope a booking contributes only that member's allocated share, which
is what "what does this cost me" means.

Money is stored as `int64` cents everywhere. Amounts are parsed and rendered in
German notation by `internal/web/format.go`.

All reporting paths use the same rounded monthly booking values and member
shares. Remaining cents are assigned by stable IDs; period averages distribute
their remainder at booking level before aggregation. `calc.Prepare` creates a
read-only, request-local evaluation for dashboard reuse, never database rows.
The year matrix tracks active months separately from amounts: genuine zeros
count towards its mean and median, inactive months do not.
Dashboard metrics use only months with an income entry for the selected scope;
explicit zero income counts. `MonthReport.IncomeRecorded` distinguishes missing
income from zero without altering the underlying monthly planning amounts.
Expense plans remain visible over the full period, while surplus charts and
matrix cells omit months without income entries. Settlement is independent of
income coverage.

Reports support `plan`, `actual` and `hybrid`. Browser reports default to hybrid;
the API report keeps its plan default for compatibility. Hybrid replaces plans
with actuals only in closed past months; open historical months are labelled
plan estimates. Current/future months remain plans, with actuals shown separately.
Closed actual months have known zero income even without an income row.
`calc.WithBankLedger` builds a read-only report view with negative synthetic
IDs for actuals; no synthetic booking is persisted. Source labels must survive
member filters, period navigation and PDF exports. The booking list remains
plan-only.

Every write is scoped to the active household in SQL — the household id is part
of the store method signature, and section and category references are resolved
through a sub-select so a forged id cannot cross household boundaries.

## Repository-specific configuration

Beyond the mandatory base set (`HB_HTTP_ADDR`, `HB_DATA_DIR`, `HB_LOG_LEVEL`,
`TZ`) there is one variable: `HB_API_TOKEN`. It is the bearer token of the HTTP
API under `/api/v1/`, and leaving it unset keeps every API route answering
`503` — which is the right default for an app that has no login.

Optional PDF import uses `AZURE_RESOURCE_ID`, `AZURE_TENANT_ID`,
`AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`. All unset disables AI; partial
configuration fails startup. `internal/bankimport` extracts PDF text locally,
discovers chat deployments via ARM and authenticates using an explicit
client-secret identity. It never uses ambient developer credentials.
The deployment is selected in Settings and saved globally in `app_state`, not
per household or per upload. Import requests use that saved selection, ignoring
any submitted deployment. Account administration also belongs in Settings;
Import selects the member's bank account and performs reading/review. CSV and
XLSX are parsed locally without AI, even when Azure is disabled. `internal/bankimport`
owns strict column mapping, cent-exact parsing and bounded spreadsheet decoding.
Invalid rows fail the whole import; never silently skip rows, guess ambiguous
columns or evaluate spreadsheet formulas. PDF-only configuration guards must
not block CSV/XLSX uploads.
Structured exports with multiple own accounts require explicit, distinct
destination mappings before persistence. Counterparty IBANs are not source
accounts. Mapping previews are bounded, household-scoped and in-memory only;
confirmation is atomic and serialized with data resets/restores.
Backup format 4 also preserves original payment fingerprints; formats 1–3 still
restore. Missing account defaults are backfilled
only after all original IDs have been restored.
Text is sent only when the user starts analysis; there is no separate consent
checkbox. Never log document text,
model output or credentials. AI output is an untrusted proposal and never
writes plans or accepted payments directly. Keep request, response, page and
text limits, destination validation and redirect refusal intact.

The API is mounted outside the same-origin guard on purpose: it authenticates
with a token rather than a cookie, so a forged cross-site request carries
nothing useful. It keeps the recover and the rate limit. `internal/api` holds
it, and the browser UI never calls it.

`docs/API.md` is the reference to hand to anything automating against this app:
every route, every field with its type and default, the enum values and the
status codes. Keep it in step with `internal/api` — a caller that cannot see the
code has nothing else to go on.

## Non-goals

- **No bank connection** (FinTS/PSD2). Certification effort and
  attack surface do not fit an app that runs without authentication.
- **No multi-currency and no double-entry bookkeeping.** Firefly III exists for
  that; this project stays a household budget book.
- **No authentication.** Operated on a private network behind a reverse proxy.
  The API token guards the machine-facing routes only; the pages stay open.
- **No automatic bank synchronization or autonomous posting.** Actual payments
  come from explicitly reviewed PDF, CSV or XLSX files; OCR, images and legacy
  XLS remain out of scope.

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
  directive and a plain `go 1.26` no longer builds. Without `toolchain go1.26.10`,
  `setup-go` would then install exactly 1.26.1, whose standard library carries
  known advisories, and the mandatory `govulncheck` step would fail. The
  directive is removed as soon as maroto relaxes its requirement. All Make
  targets export the declared toolchain so local checks match CI, even when a
  newer Go version is installed.
- **`darkMode: "class"` with a light palette.** waim and CFTM are dark-only. This
  app keeps a theme toggle, so every component class carries a `dark:` variant.
- **Bookings are edited in a `<dialog>` that opens on an already created draft.**
  There is no save button anywhere in the app, so the row has to exist before it
  can save itself. Every field posts with `hx-swap="none"` and the server answers
  `204` plus `HX-Trigger: hb:changed`; the input DOM is never replaced, which is
  what keeps a keystroke from being swallowed mid-request. The list listens for
  that event and re-fetches itself.
  Statement review is deliberately different: accepting actual payments,
  creating future plans and asserting month completeness require explicit actions.
- **Migrations run with `PRAGMA foreign_keys = OFF`.** Rebuilding a table means
  dropping it, and `DROP TABLE` with enforcement on runs an implicit `DELETE`
  that fires the children's `ON DELETE CASCADE` — migration 0004 would have wiped
  every split and tag. The pragma is a no-op inside a transaction, so it brackets
  the whole loop in `store.migrate`; `PRAGMA foreign_key_check` runs after each
  migration, inside its transaction and before commit, to take enforcement's
  place without making a failed migration permanent.
- **Tailwind only scans the templates and `viewmodel.go`.** Tailwind matches bare
  words anywhere in a scanned file, so ordinary prose in a Go comment would emit
  a utility and break the "CSS is up to date" check on an unrelated edit.
- **The Sankey diagram and the trend chart are laid out in Go and emitted as
  plain SVG.** The CSP forbids `unsafe-eval`, and a charting library would be a
  large dependency for two pictures. `internal/calc/sankey.go` computes node
  positions and ribbon paths; a node's value is the larger of its inflow and
  outflow, never the sum. `internal/calc/chart.go` does the same for the bars.
- **Category icons are inline SVG paths in `internal/web/icons.go`.** The CSP
  grants no external origin, and two dozen glyphs are not worth an icon font. An
  unset icon is guessed from the category name.
- **`contextcheck` is disabled for `internal/web`.** templ components receive the
  context through `Component.Render`, which the linter cannot follow, so it
  reports every render call.
- **`misspell` is disabled for `internal/i18n/catalog_de.go` and
  `internal/web/icons.go`.** The German catalog contains German words by
  definition, and so does the icon keyword table.
