# Copilot-Instruktionen — Haushaltsbuch

Diese Datei beschreibt die verbindlichen Konventionen, Best Practices und
Sicherheitsvorgaben für das Projekt **Haushaltsbuch**. GitHub Copilot soll sich
bei allen Vorschlägen (Code, Dockerfile, GitHub Actions, Tests, Doku) an diese
Vorgaben halten. Die Vorgaben leiten sich aus den Schwesterprojekten
`waim` und `AutoFileMover` desselben Autors (`daknoblo`) ab und sollen für ein
konsistentes Setup über alle Repos hinweg sorgen.

> **Kontext:** Haushaltsbuch ist eine kleine, eigenständige Anwendung zur
> Erfassung und Auswertung von Einnahmen und Ausgaben (persönliche
> Finanzverwaltung). Sie läuft als einzelnes Binary in Docker.

---

## 1. Sprache, Runtime & Grundprinzipien

- **Sprache: Go** (aktuelle stabile Minor-Version, aktuell **Go 1.25**).
- **Module-Pfad:** `github.com/daknoblo/Haushaltsbuch`.
- **Ein einzelnes, statisches Binary** als Auslieferungsartefakt — keine
  externen Runtime-Abhängigkeiten.
- **Pure-Go / CGO-frei:** Immer `CGO_ENABLED=0` bauen. Für SQLite die
  reine-Go-Implementierung **`modernc.org/sqlite`** verwenden (kein
  `mattn/go-sqlite3`, kein C-Toolchain).
- **Standardbibliothek zuerst.** Abhängigkeiten nur einführen, wenn sie klaren
  Mehrwert bieten. Bevorzugte, bereits im Ökosystem genutzte Libs:
  `log/slog` (strukturiertes Logging), `net/http` (Server), `modernc.org/sqlite`.
- Für ein Server-gerendertes Web-UI (falls benötigt) das gleiche Muster wie in
  `waim` nutzen: **templ + HTMX + Tailwind CSS**. Generierte Dateien
  (`*_templ.go`, kompiliertes `app.css`) werden **committet**, damit das Projekt
  ohne die templ-/Tailwind-Toolchain baubar ist.

## 2. Projektstruktur

Standard-Go-Layout mit `cmd/` als Einstiegspunkt und `internal/` für die
Implementierung:

```
Haushaltsbuch/
├── cmd/haushaltsbuch/main.go     # Einstiegspunkt (dünn: Flags, Wiring, Start)
├── internal/
│   ├── config/                   # Konfiguration aus Env-Variablen
│   ├── store/                    # SQLite-Zugriff (+ store/migrations)
│   ├── server/                   # HTTP-Server, Routing, Handler
│   ├── web/                      # templ-Templates + assets (falls UI)
│   ├── logbuf/                   # In-Memory-Log-Puffer für die UI
│   └── version/                  # Build-Metadaten (siehe unten)
├── Dockerfile
├── docker-compose.yml            # bzw. deploy/docker-compose.example.yml
├── Makefile
├── go.mod / go.sum
├── .golangci.yml
├── .dockerignore
├── .gitignore
└── .github/workflows/            # ci.yml + release.yml
```

- `main.go` bleibt schlank: Argument-Parsing, Aufbau der Abhängigkeiten,
  Signal-Handling (`os/signal`, `SIGINT`/`SIGTERM`) und Graceful Shutdown.
- Nicht öffentlich wiederverwendbarer Code gehört unter `internal/`.

## 3. Build-Metadaten (`internal/version`)

Ein `internal/version`-Package hält die per `-ldflags` zur Build-Zeit
injizierten Werte (analog zu `waim`):

```go
var (
    Version = "dev"      // vYYYYMMDD-HHMM zur Build-Zeit
    Channel = "local"    // "stable", "dev" oder "local"
    Commit  = "unknown"  // Git-Commit-Hash
    Date    = "unknown"  // Build-Datum (RFC3339)
)
```

Injektion beim Build:

```
-X github.com/daknoblo/Haushaltsbuch/internal/version.Version=$(VERSION)
```

## 4. Makefile

Ein Makefile mit den üblichen Targets bereitstellen. Mindestens:

- `build` — statisches Binary bauen (`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ..."`).
- `run` — lokal starten.
- `test` — `go test ./...`.
- `vet` — `go vet ./...`.
- `tidy` — `go mod tidy`.
- `docker` — Image bauen (Build-Args für Version/Channel/Commit/Date durchreichen).
- `clean` — Build-Artefakte entfernen.
- Falls UI: `generate` (templ) und `css` (Tailwind), sowie `tools` zur
  Installation der CLIs.

Versionsformat: `VERSION ?= $(shell date -u +v%Y%m%d-%H%M)`.

## 5. Docker

**Mehrstufiges Dockerfile** nach folgendem Muster (angelehnt an `waim`):

```dockerfile
# syntax=docker/dockerfile:1

# ---- Build stage ----
ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src

# Abhängigkeiten zuerst cachen.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG CHANNEL=local
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Version=${VERSION} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Channel=${CHANNEL} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Commit=${COMMIT} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Date=${DATE}" \
    -o /out/haushaltsbuch ./cmd/haushaltsbuch
RUN mkdir -p /out/appdata

# ---- Runtime stage ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /out/haushaltsbuch /app/haushaltsbuch
COPY --from=builder --chown=65532:65532 /out/appdata /app/appdata
ENV HB_ADDR=:8080
EXPOSE 8080
VOLUME ["/app/appdata"]
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/haushaltsbuch", "-healthcheck"]
ENTRYPOINT ["/app/haushaltsbuch"]
```

**Docker-Vorgaben (verbindlich):**

- **Build-Cache:** `go.mod`/`go.sum` vor dem restlichen Quellcode kopieren und
  `go mod download` separat ausführen.
- **Runtime-Basis:** `gcr.io/distroless/static:nonroot` (keine Shell, kein
  Paketmanager, minimale Angriffsfläche). Das setzt ein CGO-freies Binary voraus.
- **Non-root:** immer als `nonroot:nonroot` (UID/GID `65532`) laufen.
- **Healthcheck im Binary:** Da distroless kein `curl`/`wget`/Shell hat,
  implementiert das Binary ein `-healthcheck`-Flag, das lokal `/healthz`
  abfragt und einen passenden Exit-Code liefert.
- **Persistenz:** Datenverzeichnis auf `/app/appdata` (per `VOLUME`), das
  bereits mit korrekten Rechten für den Non-root-User vorangelegt wird.
- **Multi-Arch:** Images für `linux/amd64` und `linux/arm64` bauen.
- **Zeitzone:** `_ "time/tzdata"` importieren, damit `TZ` auch auf der
  distroless-Basis (ohne System-tzdata) funktioniert.

Eine `deploy/docker-compose.example.yml` (bzw. `docker-compose.yml`)
bereitstellen, die das veröffentlichte GHCR-Image referenziert, das Volume
mountet und die Env-Variablen dokumentiert.

## 6. GitHub Actions

Zwei Workflows unter `.github/workflows/`:

### `ci.yml` — Lint, Test & Build (bei push/PR auf `main` und `develop`)

- `permissions: contents: read`.
- `actions/checkout` und `actions/setup-go` (mit `cache: true`) in aktuellen,
  gepinnten Major-Versionen.
- Schritte in dieser Reihenfolge:
  1. `go vet ./...` (statische Analyse).
  2. `golangci/golangci-lint-action` (gepinnte Lint-Version).
  3. `go test -race ./...` (Tests mit Race-Detector).
  4. `CGO_ENABLED=0 go build ./...` (statischer Build).
- Falls UI: zusätzlich prüfen, dass generierter Code (`templ generate`) und
  kompiliertes CSS aktuell committet sind (`git diff --exit-code`).

### `release.yml` — Build, Push & Scan (bei push auf `main`/`develop` und Tags `v*`)

- `permissions: contents: read`, `packages: write`, `security-events: write`.
- Nach `ghcr.io` veröffentlichen (`IMAGE_NAME: ${{ github.repository }}`).
- `docker/setup-qemu-action` + `docker/setup-buildx-action` für Multi-Arch.
- `docker/login-action` mit `GITHUB_TOKEN`.
- `docker/metadata-action` für Tags/Labels; **Channel-Konvention**:
  - `main` → `stable`
  - `develop` → `dev`
  - Git-Tag `vX.Y.Z` → semver-Tags (`{{version}}`, `{{major}}.{{minor}}`)
- `docker/build-push-action` mit `platforms: linux/amd64,linux/arm64`,
  `provenance: true`, `sbom: true`, GHA-Cache (`cache-from/to: type=gha`) und
  Build-Args (`VERSION`, `CHANNEL`, `COMMIT`, `DATE`).
- **Vulnerability-Scan** mit `aquasecurity/trivy-action` gegen das gebaute
  Image (per Digest), `severity: CRITICAL,HIGH`, `ignore-unfixed: true`,
  Output als SARIF.
- SARIF via `github/codeql-action/upload-sarif` in den Security-Tab laden
  (`if: always()`).

**Allgemein:** Action-Versionen immer pinnen; keine ungepinnten `@master`/`@main`.

## 7. Linting & Formatierung

- **golangci-lint v2** über `.golangci.yml`:

```yaml
version: "2"
run:
  timeout: 5m
linters:
  default: standard
  enable:
    - misspell
  settings:
    errcheck:
      exclude-functions:
        - (io.Closer).Close
        - (io.ReadCloser).Close
        - (*database/sql.Rows).Close
        - (*database/sql.Stmt).Close
formatters:
  enable:
    - gofmt
```

- Code ist immer `gofmt`-formatiert.
- `go vet ./...` muss fehlerfrei sein.
- Fehler grundsätzlich behandeln (`errcheck`); bewusst ignorierte `Close()`-Aufrufe
  über die obige Ausnahmeliste, nicht durch `_ =` im Code verstreut.

## 8. Sicherheit (verbindlich)

- **Minimale Angriffsfläche:** distroless-Basis, non-root, statisches Binary.
- **Read-only Root-Filesystem** anstreben; nur das Datenvolume ist beschreibbar.
- **Secrets/API-Keys niemals im Klartext speichern.** Falls sensible Werte
  persistiert werden müssen, **verschlüsselt at-rest** ablegen
  (AES-256-GCM, Schlüssel aus einer Master-Key-Env-Variable abgeleitet) — wie in
  `waim`.
- **Keine Secrets committen** (keine Keys/Tokens/`.env` im Repo).
- **Keine eingebaute Authentifizierung** vorausgesetzt: Die App ist für den
  Betrieb in einem vertrauenswürdigen Netz bzw. hinter Reverse-Proxy/VPN gedacht
  und sollte nicht direkt ins Internet exponiert werden. Diese Annahme im README
  dokumentieren.
- **Container-Image scannen** (Trivy, siehe CI) und CRITICAL/HIGH-Findings ernst
  nehmen.
- **SQL:** ausschließlich parametrisierte Queries (Platzhalter), niemals
  String-Konkatenation von Nutzereingaben.
- **Eingaben validieren** (insb. Beträge, Datumsangaben, IDs) bevor sie in Store
  oder Ausgabe gelangen.

## 9. Konfiguration & Env-Variablen

- Konfiguration primär über **Env-Variablen mit Präfix `HB_`**
  (Haushaltsbuch), analog zu `WAIM_`/`AFM_` in den anderen Projekten.
- Etablierte Namensmuster:
  - `HB_ADDR` (Default `:8080`) — Listen-Adresse.
  - `HB_DB_PATH` — Pfad zur SQLite-Datenbank (im Container unter `/app/appdata`).
  - `HB_LOG_LEVEL` (z. B. `info`, `debug`) — Log-Level.
  - `TZ` — Zeitzone (IANA-Name).
  - `HB_MASTER_KEY` — nur falls Secrets verschlüsselt persistiert werden.
- Sensible Defaults; das Datenverzeichnis im Container ist fix `/app/appdata`.

## 10. Tests

- Tests mit dem Standard-`testing`-Package, ausführbar über `go test ./...`.
- In CI mit `-race` laufen lassen.
- Für Store-/Business-Logik gezielte Unit-Tests; SQLite-Tests können gegen eine
  temporäre Datei- oder In-Memory-DB laufen.

## 11. Git & Repo-Hygiene

- Branch-Konvention: `main` (stable) und `develop` (dev); Releases über
  `vX.Y.Z`-Tags.
- **`.gitignore`** deckt ab: Build-Output (`/bin/`, `/out/`, Binary), lokale
  Daten (`*.db`, `*.db-wal`, `*.db-shm`, `appdata/`), `.env*` (außer
  `.env.example`), Scan-Output (`*.sarif`, `results.json`, `gosec*`),
  OS-/Editor-Rauschen (`.DS_Store`, `.idea/`, `.vscode/`), `node_modules/`.
- **`.dockerignore`** schließt aus: `.git`, `.github`, `.devcontainer`, `*.md`,
  `docs/`, `deploy/`, lokale Daten/DB-Dateien, Build-Artefakte, `.DS_Store`.
- Generierte, aber für den Build nötige Dateien (`*_templ.go`, `app.css`)
  werden **committet**.

## 12. Devcontainer (optional, empfohlen)

Ein `.devcontainer/devcontainer.json` auf Basis des Go-Devcontainer-Images mit
`docker-in-docker`- und `github-cli`-Feature, den Extensions `golang.go`,
`ms-azuretools.vscode-docker`, `github.vscode-github-actions`, `formatOnSave`,
`golangci-lint` als Lint-Tool und `postCreateCommand: go mod download`.

---

## Zusammengefasst: Definition of Done für Änderungen

1. Baut CGO-frei (`CGO_ENABLED=0 go build ./...`).
2. `go vet ./...` und `golangci-lint` fehlerfrei, `gofmt`-formatiert.
3. `go test -race ./...` grün.
4. Docker-Image baut mehrstufig, läuft als non-root auf distroless mit
   funktionierendem `-healthcheck`.
5. Keine Secrets im Code/Repo; sensible Daten verschlüsselt at-rest.
6. CI- und Release-Workflow bleiben konsistent zu den Vorgaben oben.
