# Haushaltsbuch – HTTP API

The JSON API under `/api/v1` lets an external tool read the book and write
bookings into it: the figures come from somewhere — a salary slip, a bank
export, another tool — and this is the way in for anything that already knows a
number.

The HTML UI intentionally stays unauthenticated (internal use only, behind a
reverse proxy). **Only `/api/**` is protected**, by a bearer token.

> **Language note:** the application's user interface is German, and so are the
> `error` messages returned by this API. Everything else in this repository is
> English.

---

## Authentication

Every request carries a bearer token:

```
Authorization: Bearer <token>
```

| Environment variable | Scope | Allowed methods |
| --- | --- | --- |
| `HB_API_TOKEN` | read and write | `GET`, `POST`, `PUT`, `DELETE` |

The token is supplied exclusively through the environment, never stored in the
database and never logged.

### Behaviour

- Missing or invalid token → `401 Unauthorized`.
- Variable not set → the API is disabled, every request → `503`.

The API is mounted **outside** the same-origin guard the HTML routes carry.
That guard exists for cookies; this authenticates with a token, so it would only
turn away callers it was never meant to protect against. The panic recovery and
the rate limit still apply.

---

## Conventions

- Request and response bodies are JSON; responses carry
  `Content-Type: application/json; charset=utf-8` and `Cache-Control: no-store`.
- **Unknown fields are rejected** (`400`) rather than ignored, so a misspelling
  is reported instead of silently changing nothing.
- Request bodies are capped at 2 MiB.
- Money travels as **integer cents** in every response. A request may use either
  `amount` (Euro, may have decimals) or `amount_cents` (integer). If both are
  given, `amount_cents` wins.
- Dates are `YYYY-MM-DD`, months are `YYYY-MM`.
- Errors always have the same shape:

```json
{ "error": "kategorie nicht gefunden" }
```

### Status codes

| Code | When |
| --- | --- |
| `200 OK` | Successful read, update, delete, or an upsert that matched |
| `201 Created` | `POST /api/v1/bookings` created a new booking |
| `400 Bad Request` | Malformed JSON, unknown field, failed validation, unknown reference |
| `401 Unauthorized` | Missing or wrong token |
| `404 Not Found` | Household or booking does not exist |
| `500 Internal Server Error` | Unexpected failure; details go to the log, not the body |
| `503 Service Unavailable` | `HB_API_TOKEN` is not set |

### Selecting a household

Every route works on one household. `?household=<id>` names it; leaving it out
falls back to the **active** household, so a script that only ever runs against
one household can omit it entirely. On write routes the body field `household`
takes precedence over the query parameter.

### Referring to things by name

Categories, people and tags may be given **by name instead of by id**, so a
script reads like the book does. Names are matched case-insensitively and
trimmed. Use whichever is more convenient:

| By name | By id |
| --- | --- |
| `"category": "Miete"` | `"category_id": 3` |
| `"payer": "Ich"` | `"payer_id": 1` |
| `"shares": [{"name": "Ich"}]` | `"shares": [{"member": 1}]` |
| `"tags": ["Urlaub"]` | — |

---

## Endpoint overview

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/households` | Households, with the active one marked |
| `GET` | `/api/v1/categories` | Categories of a household |
| `GET` | `/api/v1/members` | People of a household |
| `GET` | `/api/v1/tags` | Tags of a household |
| `GET` | `/api/v1/report` | Income, expenses, fixed costs, savings rate |
| `GET` | `/api/v1/bookings` | Bookings, optionally only those counting in a month |
| `POST` | `/api/v1/bookings` | Create a booking, or update one by `external_id` |
| `GET` | `/api/v1/bookings/{id}` | One booking |
| `PUT` | `/api/v1/bookings/{id}` | Change the fields named in the body |
| `DELETE` | `/api/v1/bookings/{id}` | Delete a booking |

Anywhere `{id}` is accepted, `ext:<external_id>` works too — see
[Idempotency](#idempotency).

---

## Reading

### `GET /api/v1/households`

```json
{
  "households": [
    { "id": 1, "name": "Mein Haushalt", "active": true }
  ]
}
```

### `GET /api/v1/categories`

A booking needs a category, and the category's `classification` has to match the
booking's `direction` — an income cannot be filed under an expense category.

```json
{
  "categories": [
    { "id": 1, "name": "Gehalt", "classification": "income",
      "color": "#10b981", "icon": "wallet" },
    { "id": 3, "name": "Miete", "classification": "expense",
      "color": "#6366f1", "icon": "home" }
  ]
}
```

### `GET /api/v1/members` and `GET /api/v1/tags`

```json
{ "members": [ { "id": 1, "name": "Ich", "color": "#2563eb" } ] }
```

Tags answer the same shape under the key `tags`.

### `GET /api/v1/bookings`

| Parameter | Meaning |
| --- | --- |
| `household` | Which household, default the active one |
| `month` | `YYYY-MM`. Only bookings **counting in that month** are returned |

Without `month` the whole book comes back. With one, a booking is included only
if it actually contributes that month, which is not the same as having been
created in it: a yearly premium counts every month, a one-off only in its own.

```json
{ "bookings": [ /* booking objects, see below */ ] }
```

### `GET /api/v1/report`

| Parameter | Meaning |
| --- | --- |
| `household` | Which household, default the active one |
| `month` | `YYYY-MM`, default the current month |
| `member` | Only this person's share; omit for the whole household |

```json
{
  "month": "2026-08",
  "member": 0,
  "income_cents": 300000,
  "expense_cents": 105000,
  "balance_cents": 195000,
  "fixed_cents": 105000,
  "variable_cents": 0,
  "unassigned_cents": 0,
  "savings_rate": 65.0,
  "by_budget_class": { "need": 105000 },
  "categories": [ { "id": 3, "label": "Miete", "cents": 105000 } ]
}
```

`member` is `0` for the household view. Recurring amounts are normalised to a
monthly equivalent, so a yearly premium contributes a twelfth every month.
`savings_rate` is a percentage of net income.

---

## Writing

### The booking object

Every field is optional on `PUT`; **what is left out keeps its value**, shares
and tags included. On `POST` only `name` and a category are required.

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `external_id` | string | `""` | The caller's own name for this booking, unique per household. See [Idempotency](#idempotency) |
| `household` | integer | active household | Which household the booking belongs to |
| `name` | string | — | **Required on create.** Trimmed, max 120 characters |
| `note` | string | `""` | Free text |
| `amount` | number | `0` | Euro, e.g. `981.50` |
| `amount_cents` | integer | `0` | Integer cents; wins over `amount` |
| `category` | string | — | **Required on create** (or `category_id`). Must match `direction` |
| `category_id` | integer | — | Alternative to `category` |
| `direction` | string | `expense` | `income` \| `expense` |
| `frequency` | string | `monthly` | `once` \| `weekly` \| `monthly` \| `quarterly` \| `yearly` |
| `interval` | integer | `1` | Every n-th period, 1–60. Ignored for `once` |
| `due_point` | string | `start` | `start` \| `mid` \| `end` of the month |
| `date` | string | — | `YYYY-MM-DD`. **Required for `frequency: once`**; clears the end date |
| `active_from` | string | `""` | `YYYY-MM-DD`, start of a recurring booking |
| `active_until` | string | `""` | `YYYY-MM-DD`, empty means open ended |
| `cost_nature` | string | `fix` | `fix` \| `variable`. Expenses only |
| `budget_class` | string | `need` | `need` \| `want` \| `saving` — the thirds of the 50/30/20 rule |
| `split_mode` | string | `equal` | `equal` \| `percent` \| `fixed` |
| `settle` | boolean | `true` | `false` keeps the booking out of the settlement |
| `payer` / `payer_id` | string / integer | none | Who fronts the money. `null` or `""` takes it back |
| `shares` | array | — | Who carries it, see below |
| `tags` | array of strings | — | Tag names; unknown ones are `400` |

#### `shares`

Who carries the booking. What `value` means depends on `split_mode` — the same
rule the dialog follows:

| `split_mode` | `value` |
| --- | --- |
| `equal` | ignored, everyone listed carries the same part |
| `percent` | a percentage, and the listed shares should add up to 100 |
| `fixed` | **cents** of the booking's own amount |

```json
"shares": [
  { "name": "Ich", "value": 60 },
  { "name": "Partner/in", "value": 40 }
]
```

A booking with **no shares carries nobody**. It still counts in the household
total but is reported as unassigned and appears in no person's view. Outside
`equal`, a share of `0` carries nothing either.

### `POST /api/v1/bookings` — create or upsert

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
        "cost_nature": "fix",
        "budget_class": "need",
        "payer": "Ich",
        "shares": [{ "name": "Ich" }, { "name": "Partner/in" }]
      }'
```

`201` with the created booking, or `200` when an `external_id` matched an
existing one. The response is the booking as stored:

```json
{
  "id": 1,
  "external_id": "miete",
  "household": 1,
  "direction": "expense",
  "name": "Miete",
  "amount_cents": 98150,
  "monthly_cents": 98150,
  "category": "Miete",
  "category_id": 3,
  "frequency": "monthly",
  "interval": 1,
  "due_point": "start",
  "starts_on": "2026-01-01",
  "cost_nature": "fix",
  "budget_class": "need",
  "split_mode": "equal",
  "settle": true,
  "payer_id": 1,
  "shares": [ { "member": 1, "value": 0 }, { "member": 2, "value": 0 } ],
  "tags": [],
  "updated_at": "2026-08-27T10:12:02Z"
}
```

`monthly_cents` is what the booking contributes to a single month, overrides
and rhythm applied. `amount_cents` is the figure as entered.

### `PUT /api/v1/bookings/{id}` — update

Changes only the fields it names:

```bash
curl -X PUT http://localhost:8080/api/v1/bookings/ext:miete \
  -H "Authorization: Bearer $HB_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "amount": 1050 }'
```

Payer, shares and tags survive an update that does not mention them.

### `DELETE /api/v1/bookings/{id}`

```json
{ "deleted": 1 }
```

---

## Idempotency

A booking has no natural key — two insurances may share a name — so the caller
supplies one. `external_id` is any string, unique within a household:

- `POST` with an `external_id` that already exists **updates** that booking
  (`200`) instead of filing a second one (`201`).
- `GET`, `PUT` and `DELETE` accept `ext:<external_id>` in place of the numeric
  id, so a script never has to remember what the app assigned.

This is what keeps a job that runs twice from doubling the book. Bookings
entered by hand carry no `external_id` and can never be claimed by one.

---

## Example: filing a salary every month

```bash
#!/usr/bin/env bash
set -euo pipefail

HB=${HB_URL:-http://localhost:8080}
AUTH=(-H "Authorization: Bearer $HB_API_TOKEN" -H 'Content-Type: application/json')
MONTH=$(date +%Y-%m)

# Idempotent: re-running for the same month updates instead of duplicating.
curl -sS -X POST "$HB/api/v1/bookings" "${AUTH[@]}" -d "{
  \"external_id\": \"gehalt-$MONTH\",
  \"name\": \"Gehalt\",
  \"category\": \"Gehalt\",
  \"direction\": \"income\",
  \"amount\": 3701.00,
  \"frequency\": \"once\",
  \"date\": \"$(date +%Y-%m-28)\",
  \"payer\": \"Ich\",
  \"shares\": [{ \"name\": \"Ich\" }]
}"

# What does the month look like now?
curl -sS "$HB/api/v1/report?month=$MONTH" "${AUTH[@]}"
```

---

## What the API does not do

- **No households, categories, people or tags can be created.** They are set up
  in the UI; the API only resolves and reports them.
- **No amount overrides.** The "abweichende Beträge" of a recurring booking are
  UI-only for now.
- **No settlement or year matrix.** `GET /api/v1/report` answers a single
  month; the dashboard's settlement and the year table have no route yet.
- **No batch endpoint.** One booking per request; `external_id` makes a loop
  safe to repeat.
