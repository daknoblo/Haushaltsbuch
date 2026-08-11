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
  hand-written CSS embedded into the binary – no Node build step required
- **PDF export:** pure Go via [maroto](https://maroto.tech)

> The user interface is in German; code, comments and this documentation are in
> English.

---

## Features

- **Multiple households**, exactly one of which is active at a time. Switch via
  the header dropdown; create, rename and delete them in the settings.
- **Expenses** grouped by section, with
  - a **weekly / monthly / yearly** rhythm (normalised to a monthly amount),
  - **one-off** dated expenses,
  - **category**, **fixed/variable** and **need/want/saving** (50/30/20),
  - a flexible **split** per expense: equal, percentage or fixed amounts –
    e.g. rent 50/50, insurance 100 % on one person.
- **Income** per person and month with an arbitrary number of lines (e.g. salary
  plus bonus) and a "copy from previous month" action.
- **Overview** per month: income, expenses and balance – in total and per
  person, broken down by section, category, cost nature and 50/30/20.
- **Statistics** for the last 12 months including averages and a trend chart.
- **PDF export** of the overview, the statistics and the expense list.
- **Automatic saving**: every input is persisted as soon as a field changes –
  there is no save button.

---

## Quick start

### With Docker

```sh
docker run -d --name haushaltsbuch \
  -p 8080:8080 \
  -v haushaltsbuch-data:/app/appdata \
  -e TZ=Europe/Berlin \
  ghcr.io/daknoblo/haushaltsbuch:stable
```

Then open <http://localhost:8080> in a browser.

### With Docker Compose

See [deploy/docker-compose.example.yml](deploy/docker-compose.example.yml):

```sh
cp deploy/docker-compose.example.yml docker-compose.yml
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

| Variable       | Default                    | Description                          |
| -------------- | -------------------------- | ------------------------------------ |
| `HB_ADDR`      | `:8080`                    | Listen address                       |
| `HB_DB_PATH`   | `appdata/haushaltsbuch.db` | Path to the SQLite database          |
| `HB_LOG_LEVEL` | `info`                     | `debug`, `info`, `warn`, `error`     |
| `TZ`           | (system)                   | IANA time zone, e.g. `Europe/Berlin` |

Inside the container `HB_DB_PATH` points at `/app/appdata/haushaltsbuch.db`; the
directory `/app/appdata` is declared as a volume. An invalid `TZ` value is
reported as a warning at startup and the application falls back to UTC.

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
- All SQL access uses parameterised queries; names, colours, dates and
  identifiers are validated before they reach the store.
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
make build     # compile a static binary into bin/
make test      # run the test suite with the race detector
make vet       # go vet
make generate  # regenerate the templ templates (*_templ.go)
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
internal/server/        HTTP routing, middleware, handlers, PDF export
internal/web/           templ templates, assets, view models, formatting
internal/version/       Build metadata injected via -ldflags
```


## License

Released under the [MIT License](LICENSE).
