# Haushaltsbuch

[![CI](https://github.com/daknoblo/Haushaltsbuch/actions/workflows/ci.yml/badge.svg)](https://github.com/daknoblo/Haushaltsbuch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/daknoblo/Haushaltsbuch)](https://github.com/daknoblo/Haushaltsbuch/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/daknoblo/Haushaltsbuch)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/ghcr.io-haushaltsbuch-blue?logo=docker)](https://github.com/daknoblo/Haushaltsbuch/pkgs/container/haushaltsbuch)

A small, self-contained application for managing the income and expenses of one
or more households. It ships as a **single static binary** in a minimal Docker
container (distroless, non-root).

- **Backend:** Go (CGO-free), SQLite via `modernc.org/sqlite`
- **Frontend:** server-rendered with [templ](https://templ.guide) + HTMX and
  Tailwind CSS, compiled into the binary – no Node build step required
- **PDF export:** pure Go via [maroto](https://maroto.tech)
- **Statement import:** CSV and Excel (`.xlsx`) read locally without AI;
  optional PDF text extraction with Azure OpenAI. All formats require explicit
  review before an actual payment counts.

> The user interface ships in German and English and follows the browser's
> `Accept-Language`. Code, comments and this documentation are in English.

## Screenshots

_Not captured yet._

---

## Features

- **Import & reconciliation** — PDF, CSV and XLSX statements, multiple own bank accounts, reviewed actual
  payments, duplicate warnings, internal-transfer exclusion and explicit month
  closure. Plans remain separate and continue to describe the future.

- **Multiple households**, exactly one of which is active at a time. Switch via
  the header dropdown; create, rename and delete them in the settings.
- **Bookings** — income and expenses are maintained in one place, entered through
  a single dialog, so a figure never has to be kept in sync in two lists:
  - a **one-off** dated booking or a **weekly / monthly / quarterly / yearly**
    rhythm with an interval ("every second month"), normalised to a monthly
    amount, plus where in the month it falls (start, middle, end),
  - a **mandatory category** that knows whether it belongs to income or
    expenses and carries a colour and a symbol, plus free **tags** for anything
    that cuts across categories,
  - **fixed/variable** and **need/want/saving** (50/30/20),
  - **who fronts the bill** and a flexible **split**: equal, percentage or fixed
    amounts – e.g. rent 50/50, life insurance 100 % on one person,
  - **differing amounts for a period**, so an introductory price ("10 € for the
    first six months, 49.99 € afterwards") needs no second booking,
  - **permanent price changes** that close the predecessor and exclude it from
    annual carry-over, preventing the old and new price from being added
    together,
  - left-aligned sorting, with full-text search and the **Hide old bookings**
    checkbox on the right. Search starts at two characters and covers names,
    categories, tags, involved people, notes, amounts, dates, recurrence and
    allocation values, including overrides. Multiple words can match different
    fields. Completion suggestions and a centered clear icon assist searching.
    The month checkbox is enabled by default; untick it to include past and
    future bookings. Filters affect only the list, not the monthly balance.
- **Overview** per month: income, expenses and balance – in total and per
  person, broken down by category, cost nature and 50/30/20.
- **Dashboard** with a selectable period (month, two months, quarter, half year,
  year) and arrows that step by the length of that period. It opens on the
  current month by default; explicit period links remain supported. The sticky
  period and person controls refresh the report in place without reloading the
  page, preserving the scroll position, expanded categories, open settlement
  ledgers and own-cost visibility. If the new report is shorter, scrolling is
  limited to its end.
  Failed requests leave the previous report visible and show an error.
  The budget cards
  describe a *typical month* of the range, so they stay comparable no matter
  how long it is:
  - a **bar chart** of income against expenses, with the period picker centred
    above it,
  - a **view switch** between the whole household and a single person, which
    recomputes every card – your own view shows your half of the rent plus
    whatever only you carry,
  - a **settlement** with the full booking amount, fronted amount and own
    share on each member's ledger. In year view, settlement and its cost
    breakdown sum January through the current month (all twelve months for
    past years; no months for future years). Other periods retain monthly
    averages. The selected basis determines whether a month uses normalized
    plans or actual payments. Previously made settlement transfers are not
    deducted as repayments.
    Shares are rounded without losing cents, including transfers below one
    euro. Incomplete or excessive splits are explicitly listed and block
    transfer suggestions until corrected,
  - an integrated **cost overview** within settlement, with shared and own cost
    tiles and a total per person, each showing its fixed-cost portion. All
    correctly allocated expenses count, including personal expenses, savings,
    manual settlement exclusions and bookings without a payer. Missing or
    invalid allocations are listed explicitly and excluded from these totals.
    **Show own costs** is off by default and adds sole-carried bookings to the
    list (for the selected person or the whole household). Allocation badges
    use solid, high-contrast green for alone, blue for equal shares,
    violet for percentages and amber for fixed amounts, with bold text labels
    and explicit light/dark palettes. Other status badges stay unchanged. The list footer
    follows the visible rows; tiles always show all costs. Reimbursement
    ledgers and transfers remain separate and unchanged. Cost figures use the
    settlement period, not the income-coverage filter of the budget metrics.
    The statistics PDF includes the complete cost summary and fixed portions,
  - **automatic settlement exclusion** when the payer carries the entire cost
    alone. The switch turns off with an explanation, including for existing
    bookings. Adding another carrier or changing the payer restores the
    previous preference; a deliberate manual exclusion stays off. Budget
    reports continue to include these costs. The settlement indicator sits next
    to the shared/sole badge; both are red for shared expenses without settlement,
  - **fixed costs** with their share of income and the largest items,
  - **savings rate** — deliberate savings plus surplus against net income — and
    the 50/30/20 split against its targets,
  - a **Sankey diagram** of the money flow, from every income source through the
    budget classes down to the single category,
  - spending **by category** and **by tag**.
- **PDF export** of the overview, the dashboard and the booking list, all
  reachable from the dashboard.
- **Custom ordering**: households and people can be moved up and down with the
  arrow buttons; bookings sort themselves by amount inside their category.
- **Automatic saving for plans and settings**: each field is persisted when it
  changes, and nothing you are typing in is ever
  replaced under your cursor. Failed saves are shown in the dialog and do not
  discard the input; correcting the invalid field clears the error after a
  successful save.
  Statement review is deliberately different: its explicit actions approve,
  correct, ignore or reopen a payment.

### Statement import and Plan / Actual / Hybrid

1. For **PDFs**, configure the optional Azure application identity (see below), then select
   the model once under **Settings → AI for imports**. The selection is
   saved automatically and applies to **all households**. CSV and XLSX work
   without an AI connection or selected model and never invoke Azure.
2. Manage household members under **Settings → People**. Every member
   automatically receives a default bank account, including existing members
   during upgrade. If a member already owns accounts, their oldest account is
   made the default rather than creating a duplicate. Under **Members' bank
   accounts**, rename labels, add accounts or choose another default.
   The member is the person fronting that bank account's payments; this is
   unrelated to the Azure identity. Renaming a member does not change account
   IDs or statement history.
3. In **Import & reconciliation**, select the **bank account shown on the
   statement** (labelled with its member), then drop or select PDF, CSV or XLSX
   files. Uploads start immediately and run sequentially, without a start button.
   Additional files join the running queue automatically. Only PDF analysis sends extracted text to Azure, without an
   additional confirmation checkbox. CSV/XLSX are parsed locally. Original files
   are not retained, and document text and model responses are not logged. PDFs
   use the saved model, never a model supplied by the upload form. Missing AI
   setup blocks only PDF analysis and links to Settings. Model discovery occurs
   in Settings and during PDF analysis, not when opening the import page.
   The bank account and PDF controls sit side by side on wider screens.
   The left destination selection applies to single-account files and PDFs.
   When a CSV/XLSX file contains multiple own accounts, an inline mapping card
   appears directly below the upload area before anything is persisted. Each
   source remains separate. Queue links scroll to that card without navigating
   away or opening another tab, even while further uploads are running.
4. The imported month and its booking rows appear immediately after reading a
   file or confirming account mapping. Further months with pending payments are
   shown as visible count links, rather than a collapsed list. If another row
   has unsaved edits, the application offers a link instead of discarding them.
   Compare the suggestions with the original statement. Correct dates, positive
   EUR amounts, income/expense direction, categories and allocations. A unique
   name match suggests a plan and highlights amount deviations, but never edits
   that plan. Allocations can use the account owner or proportionally copy a
   complete linked plan, including its tags. Accepted cent allocations are
   snapshots and do not change when a plan is edited later.
5. Accept each actual payment, ignore irrelevant/duplicate rows, or explicitly
   mark internal transfers to another own account. Similar opposite payments
   on different accounts are only hints, never automatic transfer decisions.
   Review both sides of transfers. Transfers do not count as income/expenses
   and do not track settlement debt repayments.
6. Once **all accounts** are complete, explicitly close the past month.
   Pending rows block closure. Reopen it before making corrections or importing
   another statement for that month. Current and future months stay open.

Settings group households, members and bank accounts into three equally sized
desktop tiles, leaving empty space below shorter content. They stack naturally
on smaller screens. Bank-account name fields use the same width for default,
additional and new accounts, with a shared reserved column for their actions.
**Configure the Azure connection**
always shows each of the four environment variables without an expandable
menu, on its own row: green **Set** or
red **Not set**. Only presence is displayed, never a variable's value; this is
not a credential-validity check. The status reflects the configuration loaded
at startup, so environment changes require a restart as before.

Statement transactions show booking date, payee, bank account, review state and
signed amount, plus category, cost nature, budget class, exact proposed member
shares, tags and any plan link directly in the row. Local suggestions use active
plans, consistent accepted history from the same bank account, known merchant
patterns and matching category/account names. Conflicting history and unknown
categories are not silently replaced by a catch-all category. Payment-processor
history also requires the same reference. Pending and ignored rows never train
the history-based suggestions; CSV/XLSX classification does not call AI.

An eligible row can be **accepted directly** without opening its details.
Possible duplicates, internal-transfer hints, missing categories or incomplete
allocations require inspection. Details remain available for corrections and
explicit duplicate overrides. Suggestions are request-local, not extra stored
bookings. Direct acceptance checks the displayed values and allocations against
a fresh database snapshot inside the write transaction; changed payment, plan
or allocation data must be reviewed again. There is no automatic approval,
duplicate override or retry. Existing accepted allocation snapshots remain
unchanged. Editing detail fields disables the row shortcut until those changes
are submitted through the detailed form.

State counts summarize the selected month. Duplicate and possible transfer
indicators remain visible when collapsed. Original source names remain available
in booking details as provenance, not as stored files. The generic AI warning
banner has been removed; manual review and month-completeness confirmation are
unchanged. The planned booking list also shows category names under booking
names, retaining its filters, allocation badges and monthly totals.

The upload queue shows each filename, actual transfer progress, server
processing, success, required account mapping or an explicit error. The bank
account and CSV/Excel options are captured for each selected file; controls are
locked during processing. Each CSV/XLSX file detects its own formats and columns,
and its expandable result shows the effective detection. Ambiguous formats or
headings stop that file without saving rows and open the correction options.
Correct the options and retry that file explicitly; its original file remains
available in browser memory. Failed files are never retried automatically.
Rate limits and network failures pause the remaining queue until explicitly
resumed, so a paid PDF analysis is never repeated automatically. An interrupted connection is
reported as an uncertain result: check the current bookings before retrying.

Upload and mapping cards use the same filename typography and spacing.
**Files being processed** contains only unfinished account mappings. Completed
uploads are not a permanent file archive: the browser releases the uploaded file
reference on completion, and only the current page shows a compact result
(`new` / `already present`). Reopening Import shows the bookings, not old file
cards. Removing a temporary result does not delete payments. Original file
contents are never retained by the server; minimal account/source provenance
remains attached to the stored payments.
Open or edited review forms are not replaced by background status refreshes.
Unstarted selections are browser-memory only; navigating away can discard them.
Without JavaScript, a fallback button still supports single-file uploads, while requests containing
multiple files are explicitly rejected rather than silently importing only one.

The overview shows plan and actual side by side. Overview, dashboard, charts,
matrix, member shares, settlement and their PDFs support three bases:

- **Plan:** unchanged monthly normalization of recurring bookings.
- **Actual:** accepted payments on their booking dates, excluding transfers.
  Open months are visibly incomplete, not presumed complete. A closed month
  without income is a known zero, not missing data.
- **Hybrid** (browser default): actual payments replace plans for closed past
  months. Open past months are marked **plan estimates**, while current/future
  months retain plans. Plans and actuals are never added for the same month.

An annual premium therefore counts entirely in its payment month in Actual,
but as one twelfth each month in Plan. Hybrid is a planning forecast, not a
bank-balance projection. The booking list and its PDF remain explicitly
plan-only. The API report preserves its previous `plan` default and offers an
explicit `basis` parameter; see [API reference](docs/API.md).

Accepting a payment never creates a plan implicitly. An accepted, unlinked
payment can explicitly create a new future plan with a chosen start and
frequency. Existing plans are edited through the normal booking editor.

#### Plausibility check

Open **Settings → Plausibility check**, select a calendar year, then run the
check explicitly. It reads a transaction-consistent snapshot of the active
household, without AI, corrections, generated booking rows or saved report
copies. Plan bookings, imported rows and accepted actual payments are counted
separately; keeping a plan and its linked payment is not itself a duplicate.

The check covers relationships, allocations, default accounts, closed-month
consistency and possible duplicates across the whole household, including
overlapping same-name recurring plans. Recurring date ranges are evaluated by
month, not prorated by day; consecutive prices must not share a month. For the selected
year it checks Plan, Actual and Hybrid for the household and every member:
monthly amounts against an independent rational-cent reference, period averages,
categories/tags, fixed/variable costs, budget classes, matrix totals/active-month
averages/medians, chart values, flow conservation and settlement. It also renders
the actual overview/dashboard headline components and checks their visible text,
including income-coverage-based period selection. Monetary normalization and
allocation use exact fractions; equal remainders go to stable IDs. Explicitly
allocated zero payments stay valid, and display formatting does not truncate
aggregates at the single-input limit.

Invalid source data prevents the calculation phase; incomplete plan allocations
and possible duplicates are review notices, not automatic corrections. Actual
allocations remain their original snapshots after plan changes. Missing income,
closed actual zero-income months, historical plan estimates, N:M tags and
elapsed-year settlement retain their distinct meanings. No discrepancies means
only that the checked values agree: it does not prove complete statements,
correct real-world inputs or visual PDF correctness. Run again after edits.
At most 100 findings are displayed; the summary counts all findings.

Every uploaded row is compared with the current ledger of its destination
account, regardless of filename, file digest or file format. Pending, accepted,
ignored and transfer decisions all count as existing records and are left
unchanged. Only unmatched occurrences are added for review. Repeated identical
rows are counted: one stored payment matches at most one incoming row, so extra
occurrences are not silently lost. Matching uses date, direction, integer cents
and normalized name/reference, plus an immutable original fingerprint that
keeps later corrections from reappearing on the next import. Matched records
whose original identity matches but whose current fields differ are counted as
preserved corrections in the result. Legacy records
without that fingerprint use their currently stored fields; original values
changed before this feature cannot be reconstructed.

Similar but different entries are flagged for inspection rather than overwriting
existing payments or enabling direct acceptance. An unchanged closed month can
be checked again without reopening it; adding a new payment to a closed month
still rejects the entire upload. PDF reuploads perform fresh analysis and may
incur another AI request, because file hashes no longer bypass row-level checks.
There are still no automatic retries.

Unused non-default accounts can
be removed in Settings; select another default before deleting a default
account. Accounts with import history or internal-transfer references remain
protected. Deleting a member also removes their unused accounts, but is refused
when their bank history or actual payment allocations still need the member.
Additional identical payments still require explicit duplicate confirmation
when accepted. Partial exports without stable bank transaction IDs cannot prove
whether otherwise identical occurrences represent the same real-world payment.
These checks do **not** prove statement completeness: AI extraction can omit or
misread rows. Always compare totals and rows with the original. When the AI
provides opening/closing balances, their difference must match the transactions.

Limits: EUR only, 10 MiB and 1,000 transactions per file. PDFs need selectable
text, with at most 100 pages and 200,000 bytes of extracted text. No OCR, images
or automatic bank access. Processing runs one at a time, is time-bounded and
rejects partial, truncated or malformed input without persisting a partial import.
Bank account names, default-account markers, import digests, review decisions,
allocations, month closures and the non-secret global model selection are
included in backup format 4, together with original payment fingerprints.
Formats 1–3 remain readable; missing accounts
and defaults are filled in without changing existing IDs. Older backups do not
contain a model selection, so select one in Settings after restoring them.
Resetting bookings also clears statements, actual payments and month closures,
but keeps the account setup and model selection. A full reset clears the model
selection and creates default accounts for the newly seeded members. Versions
before this change cannot read format 4 backups. Back up before upgrading;
migration 0010 adds fingerprint tracking without changing existing payment data.

### CSV and Excel formats

The collapsed **Correct automatic detection (if needed)** section permits
bank-specific adjustments. Normally, headings are found in the first 50 rows:

| Meaning | Examples of recognized headings |
| --- | --- |
| Booking date | `Buchungstag`, `Buchungsdatum`, `Datum`, `Booking date`, `Date` |
| Signed amount | `Betrag`, `Betrag (EUR)`, `Amount`, `Umsatz` |
| Separate debit / credit | `Soll` / `Haben`, `Debit` / `Credit`, `Belastung` / `Gutschrift` |
| Counterparty | `Name`, `Payee`, `Begünstigter/Zahlungspflichtiger`, `Empfänger/Auftraggeber` |
| Reference | `Verwendungszweck`, `Buchungstext`, `Reference`, `Description` |
| Direction (optional) | `Soll/Haben`, `S/H`, `Direction`; values such as `S`/`H`, `debit`/`credit` |
| Currency (optional) | `Währung`, `Currency`; only `EUR` or `€` is accepted |
| Own source account (optional) | `Kontonummer`, `Account number`, `Source account`, `Eigene IBAN` |
| Own account label (optional) | `Kontoname`, `Account name`, `Source account name` |

Headers are case-insensitive. Ambiguous headings require an explicit mapping,
not a guessed column. `Wertstellung` is never automatically substituted for the
booking date. Use the exact heading text to override a mapping or specify the
header row. Without a counterparty column, the reference supplies a short
display name while the full reference is retained. Multiple recognized reference
columns are joined in source order.

**Multiple accounts in one export:** own account numbers group the rows;
account names are used only when there is no own-account-number column.
A generic `IBAN` column is never treated as the source account because bank
exports commonly use it for the counterparty. Missing source identifiers are
errors rather than grounds for silently merging rows.

If multiple source accounts are detected, choose a distinct bank account of the
active household for each one directly in Import. The preview shows the source label, only the
last four characters of its number, and the number of transactions. No
account row relies on color alone: alternating tinted backgrounds and accent
borders separate the groups, while names and numbered destination labels remain
visible. Selection fields have stronger boundaries in both themes. No
destination is silently assigned. Unique exact name matches are preselected as
suggestions; the left selection can disambiguate a matching destination when the
source name itself is unique. Every mapping still requires confirmation. Add
missing bank accounts in Settings (opens in a new tab), then refresh the
destination list. Background updates retain existing form nodes and choices.
Confirmation and cancellation stay on the Import page; errors appear inline.
Without JavaScript, validation errors render the normal Import page with the
submitted choices. Confirmation compares every group and inserts only new rows
in one database transaction: a foreign/missing destination or a new payment in
a closed month rolls back every group. Existing decisions remain unchanged;
new payments remain pending manual review.

Mappings remain directly visible whenever Import is reopened and can be
explicitly discarded there. Legacy mapping URLs return to Import; missing or
expired previews show a recovery message instead of a separate dead-end page.
At most four are
held temporarily in memory, accessible for 15 minutes; expired entries are
removed on subsequent preview access. Restarting the process, restoring or
resetting data, or deleting a household invalidates outstanding mappings.
Unconfirmed mappings are not included in backups and original files are not
retained. Upload again if a preview expires. Each new multi-account upload
requires explicit mapping; source-account links are not silently remembered.

- **CSV:** automatic semicolon, comma or tab detection; quoted fields, embedded
  newlines, UTF-8 (with or without BOM), UTF-16 with BOM and Windows-1252.
  Separator and encoding can be set explicitly; an initial `sep=;`-style
  directive is supported. Header-row numbers start after that directive.
- **XLSX:** numeric cells are read directly, including Excel date serials and
  the 1900/1904 date systems. A unique sheet with recognizable statement headings
  is selected automatically, even if other sheets contain summaries. Multiple
  possible statement sheets require explicit selection; an invalid statement
  sheet is never silently ignored in favor of another one.
  One sheet is processed per upload. The same workbook may be uploaded again
  with another explicitly selected sheet; payment-level reconciliation prevents
  duplicate entries. Legacy `.xls`, encrypted workbooks, macros
  and formulas are not supported. Export formulas as values first.
- **Numbers:** German (`1.234,56`) or English (`1,234.56`) notation is inferred
  from all transaction rows, unless explicitly overridden. Numeric Excel amounts are independent of this
  setting. Cents are parsed exactly without floating-point rounding; fractional
  cents and conflicting debit/credit indicators are rejected.
- **Dates:** full-year ISO dates always work. Day/month/year or month/day/year
  is inferred from the complete file. If both yield different valid dates,
  explicit selection is required; future-date rejection is not used to guess
  the format. Two-digit years and mixed formats are not guessed.
- **Currency:** without a currency column, amounts are assumed to be EUR.
  If the column exists, every transaction must explicitly contain EUR.
- **Validation:** blank rows are ignored, but invalid transaction rows, totals
  and footers fail the entire file. Errors identify the affected row without
  reflecting cell contents. Nothing counts as an actual payment until reviewed.
  CSV rows may omit trailing columns only when none of those columns is mapped
  for import. This supports C24 exports whose header includes `Bargeldabhebung`
  but whose transaction rows omit that unused final field. Missing mapped
  columns and additional columns remain errors. Such C24 exports work with the
  default automatic separator, column and number/date-format detection.

Tables are limited to 64 columns, 50 preamble/header rows, 2 MiB of cell text,
and 16 KiB per cell. XLSX ZIP content is bounded to 32 MiB and 256 entries,
with worksheet row/cell limits enforced before loading. No spreadsheet formulas
are evaluated and no external links are fetched.

### Calculation rules

All reports, member views, charts and settlement use the same cent-rounded
monthly booking values. Indivisible cents are assigned deterministically by
booking or member ID, so the parts add up to the total. Incomplete or excessive
splits keep an explicit unassigned remainder rather than being silently
rescaled. Percent splits show the configured percentages; fixed splits are
labelled as fixed amounts, not as equal divisions.

Dashboard metrics use months with an income entry for the selected scope, plus
closed months on an actual basis (where missing income means a confirmed zero).
An explicitly recorded zero counts; a missing income entry is not treated as
zero. Income, costs, ratios and breakdowns use the same included months, listed
beside the metrics. Without any income entries, metrics display as unavailable.
All dashboard sections remain visible. In that case, fixed costs, deliberate
savings, categories and tags show the selected period's monthly expenses using
the selected basis; income ratios and surplus remain unavailable.
The 50/30/20 and flow sections explain that income is missing instead of drawing
a fictitious deficit. Once income entries exist, these breakdowns continue to
use only the recorded-income months, just like the other metrics.
Percentages with zero income are undefined and display as a dash.
The full expense plan remains visible in charts and the year matrix, but no
surplus is drawn for incomplete months or across gaps. Settlement still uses
its own expense-based period, independent of whether income has been entered.

Period averages divide by the number of included months. Their remaining cents
are distributed at booking level before categories and member figures are
summed. All bases share the same rounding engine. The year
matrix's **mean and median** describe the months a
row was active: a genuine zero month counts, while months outside the active
range do not. An active zero displays as `0 €`; inactive cells stay blank.
Its surplus row requires recorded income and leaves incomplete months blank.
The statistics PDF keeps the selected person, period and calculation basis.
The dashboard reuses a single request-local evaluation rather than repeatedly
calculating the same monthly allocations.

Backups read all tables in one transaction and preserve retired predecessors.
Older backups without that marker remain readable. Existing price changes
created before the marker was introduced cannot be identified reliably from
names or amounts; review those old entries before carrying them forward.
Migration foreign-key checks run before commit, so a failed check rolls back
the schema, data and migration version together.

---

## Quick start

### With Docker

```sh
docker run -d --name haushaltsbuch \
  -p 8080:8080 \
  -v haushaltsbuch-data:/app/appdata \
  -e TZ=Europe/Berlin \
  ghcr.io/daknoblo/haushaltsbuch:latest
```

Then open <http://localhost:8080> in a browser.

### With Docker Compose

See [docker-compose.example.yml](docker-compose.example.yml):

```sh
cp docker-compose.example.yml docker-compose.yml
docker compose up -d
```

### Locally (development)

```sh
make run
# or
go run ./cmd/haushaltsbuch
```

By default the application listens on `:8080` and creates its database at
`appdata/haushaltsbuch.db`.

---

## Configuration

Runtime settings use `HB_` variables, `TZ`, and optional standard Azure identity
variables:

| Variable | Default | Description |
| --- | --- | --- |
| `HB_HTTP_ADDR` | `:8080` | Listen address |
| `HB_DATA_DIR` | `/appdata` | Directory holding `haushaltsbuch.db` |
| `HB_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `HB_API_TOKEN` | *(unset)* | Bearer token for the HTTP API. Unset keeps the API off |
| `TZ` | `Europe/Berlin` | IANA time zone |
| `AZURE_RESOURCE_ID` | *(unset)* | Full ARM ID of the Cognitive Services / Azure OpenAI account |
| `AZURE_TENANT_ID` | *(unset)* | Entra tenant UUID |
| `AZURE_CLIENT_ID` | *(unset)* | Application/client UUID |
| `AZURE_CLIENT_SECRET` | *(unset)* | Application credential, supplied at deployment time |

Leave all four Azure variables unset to disable AI. Partial/invalid identity
configuration fails startup; credentials are not copied from another project
or stored in backups. Assign the application **Reader** on the configured
resource for discovery and **Cognitive Services OpenAI User** for inference
(or equivalent least-privilege custom roles). The identity must belong to the
intended tenant and have access to the chosen deployment.

The selected model name is stored in the application database, independently
of the active household. Changing households or restarting the application
keeps that selection. Azure credentials remain environment-only, not in the
database or backups. An unavailable saved model is shown as unavailable in
Settings; it is never silently replaced by another deployment.

This integration uses an explicit client-secret identity, ARM deployment
discovery and bearer-token inference. It targets Azure public cloud, pins
identity/discovery/inference hosts, refuses redirects, caches tokens through
the Azure SDK and refreshes the
deployment list every five minutes. It does not fall back to developer CLI
credentials. No Azure resource or role assignment is created automatically.

The database file is created as `<HB_DATA_DIR>/haushaltsbuch.db`. In the
container `/appdata` is declared as a volume. Invalid values for `HB_HTTP_ADDR`
or `HB_DATA_DIR` abort the start with a clear message; an invalid `TZ` is
reported as a warning and the application falls back to UTC.

---

## API

A JSON API under `/api/v1` lets a script keep the book up to date: read the
households, categories and people to resolve names, list bookings, ask for a
month's figures, and create, change or delete a booking.

Set `HB_API_TOKEN` to switch it on — without it every route answers `503`,
which is the right default for an app that has no login. Every request carries
`Authorization: Bearer <token>`.

```bash
curl -X POST http://localhost:8080/api/v1/bookings \
  -H "Authorization: Bearer $HB_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "external_id": "miete",
        "name": "Miete",
        "category": "Miete",
        "amount": 981.50,
        "frequency": "monthly",
        "active_from": "2026-01-01",
        "payer": "Ich",
        "shares": [{ "name": "Ich" }, { "name": "Partner/in" }]
      }'
```

`external_id` is the caller's own name for a booking. It turns `POST` into an
upsert and stands in for the numeric id as `ext:miete`, which is what keeps a
job that runs twice from filing everything twice.

**[docs/API.md](docs/API.md) is the full reference** — every route, every field
with its type and default, the enum values, the status codes and a worked
example. Point a tool or an agent at that file.

---

## Security

> **No built-in authentication.** The application is meant to run inside a
> trusted network or behind a reverse proxy/VPN and must **not** be exposed
> directly to the internet.

- Minimal attack surface: distroless base image, non-root (UID/GID 65532),
  static binary and a recommended read-only root filesystem (only the data
  volume is writable).
- Every response carries a strict `Content-Security-Policy` (`default-src
  'self'`, no `unsafe-eval`) plus `X-Content-Type-Options`, `X-Frame-Options`,
  `Referrer-Policy` and cross-origin isolation headers.
- State-changing requests (anything other than `GET`/`HEAD`/`OPTIONS`) are
  rejected with `403` when the browser reports them as cross-site
  (`Sec-Fetch-Site`, falling back to `Origin`). This protects the
  unauthenticated instance against CSRF.
- Request bodies are capped at 1 MiB and the HTTP server enforces read, write
  and idle timeouts.
- Pages are served with `Cache-Control: no-store` so that financial data is not
  written to shared or disk caches.
- All SQL access uses parameterised queries; names, colours, dates, amounts and
  identifiers are validated before they reach the store.
- Every write is scoped to the active household in SQL, so a forged identifier
  cannot read or modify another household's data.
- The container image is scanned for CRITICAL/HIGH vulnerabilities with Trivy in
  CI.
- Static analysis and supply-chain checks run in CI: CodeQL (push, pull request
  and weekly schedule), `go vet`, golangci-lint, `govulncheck` and a dependency
  review on every pull request. Dependabot keeps Go modules, GitHub Actions and
  the base image up to date.

---

## Development

```sh
make help      # list available targets
make tailwind  # download the standalone Tailwind CLI (run once)
make generate  # regenerate the templ templates and the Tailwind CSS
make check     # everything CI runs: fmt, vet, lint, test, build
make build     # compile a static binary into bin/
make docker    # build the container image
```

The web UI uses [templ](https://templ.guide). After changing a `*.templ` file
the generated files must be refreshed with `make generate` (i.e.
`go tool templ generate`) and **committed** – this keeps the project buildable
without the templ toolchain.

### Project layout

```
cmd/haushaltsbuch/      Entry point (flags, wiring, graceful shutdown)
internal/config/        Configuration from HB_ environment variables
internal/store/         SQLite access and migrations
internal/calc/          Monthly aggregation (normalisation, split allocation)
internal/i18n/          German and English text catalogs
internal/web/           HTTP handlers, middleware, routing, templ views, assets
internal/version/       Build metadata injected via -ldflags
```


## License

Released under the [MIT License](LICENSE).
