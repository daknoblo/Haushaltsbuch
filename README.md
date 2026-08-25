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

> The user interface ships in German and English and follows the browser's
> `Accept-Language`. Code, comments and this documentation are in English.

## Screenshots

_Not captured yet._

---

## Features

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
    first six months, 49.99 € afterwards") needs no second booking.
- **Overview** per month: income, expenses and balance – in total and per
  person, broken down by category, cost nature and 50/30/20.
- **Dashboard** with a selectable period (month, two months, quarter, half year,
  year) and arrows that step by the length of that period. Every figure
  describes a *typical month* of the range, so the cards stay comparable no
  matter how long it is:
  - a **bar chart** of income against expenses, with the period picker centred
    above it,
  - a **view switch** between the whole household and a single person, which
    recomputes every card – your own view shows your half of the rent plus
    whatever only you carry,
  - a **settlement** that says who has to transfer how much to whom,
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
- **Automatic saving**: every input is persisted as soon as a field changes –
  there is no save button anywhere, and nothing you are typing in is ever
  replaced under your cursor.

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

All settings are provided through environment variables prefixed with `HB_`:

| Variable | Default | Description |
| --- | --- | --- |
| `HB_HTTP_ADDR` | `:8080` | Listen address |
| `HB_DATA_DIR` | `/appdata` | Directory holding `haushaltsbuch.db` |
| `HB_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `TZ` | `Europe/Berlin` | IANA time zone |

The database file is created as `<HB_DATA_DIR>/haushaltsbuch.db`. In the
container `/appdata` is declared as a volume. Invalid values for `HB_HTTP_ADDR`
or `HB_DATA_DIR` abort the start with a clear message; an invalid `TZ` is
reported as a warning and the application falls back to UTC.

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
