# Haushaltsbuch

Eine kleine, eigenständige Anwendung zur Verwaltung von Einnahmen und Ausgaben
mehrerer Haushalte. Läuft als **ein einzelnes, statisches Binary** in einem
minimalen Docker-Container (distroless, non-root).

- **Backend:** Go (CGO-frei), SQLite über `modernc.org/sqlite`
- **Frontend:** server-gerendert mit [templ](https://templ.guide) + HTMX,
  eigenes, in das Binary eingebettetes CSS – kein Node/Build-Schritt nötig
- **PDF-Export:** rein in Go über [maroto](https://maroto.tech)

---

## Funktionen

- **Mehrere Haushalte** mit jederzeit genau einem aktiven Haushalt; Wechsel über
  ein Dropdown in der Kopfzeile, Anlegen/Umbenennen/Löschen in den Einstellungen.
- **Ausgaben** je Sektion gruppiert, mit
  - Rhythmus **wöchentlich / monatlich / jährlich** (auf Monat normalisiert),
  - **einmaligen** datierten Ausgaben,
  - **Kategorie**, **Fix/Variabel** und **Bedarf/Wunsch/Sparen** (50/30/20),
  - flexibler **Aufteilung** pro Ausgabe: gleichmäßig, prozentual oder feste
    Beträge – z. B. Miete 50/50, Versicherung 100 % auf eine Person.
- **Einnahmen** je Person und Monat, beliebig viele Zeilen (z. B. Gehalt +
  Sonderzahlung/Bonus), mit „aus Vormonat übernehmen".
- **Übersicht** je Monat: Einnahmen, Ausgaben, Saldo – gesamt und pro Person,
  aufgeschlüsselt nach Sektion, Kategorie, Kostenart und 50/30/20.
- **Statistiken** über die letzten 12 Monate mit Durchschnitten und Verlauf.
- **PDF-Export** von Übersicht, Statistiken und Ausgabenliste.
- **Automatisches Speichern**: alle Eingaben werden beim Verlassen/Ändern eines
  Feldes sofort gespeichert – ohne Speichern-Button.

---

## Schnellstart

### Mit Docker

```sh
docker run -d --name haushaltsbuch \
  -p 8080:8080 \
  -v haushaltsbuch-data:/app/appdata \
  -e TZ=Europe/Berlin \
  ghcr.io/daknoblo/haushaltsbuch:stable
```

Danach im Browser: <http://localhost:8080>

### Mit Docker Compose

Siehe [deploy/docker-compose.example.yml](deploy/docker-compose.example.yml):

```sh
cp deploy/docker-compose.example.yml docker-compose.yml
docker compose up -d
```

### Lokal (Entwicklung)

```sh
make run
# oder
go run ./cmd/haushaltsbuch
```

Standardmäßig lauscht die App auf `:8080` und legt die Datenbank unter
`appdata/haushaltsbuch.db` an.

---

## Konfiguration

Alle Einstellungen erfolgen über Umgebungsvariablen mit dem Präfix `HB_`:

| Variable       | Default                    | Beschreibung                         |
| -------------- | -------------------------- | ------------------------------------ |
| `HB_ADDR`      | `:8080`                    | Listen-Adresse                       |
| `HB_DB_PATH`   | `appdata/haushaltsbuch.db` | Pfad zur SQLite-Datenbank            |
| `HB_LOG_LEVEL` | `info`                     | `debug`, `info`, `warn`, `error`     |
| `TZ`           | (System)                   | IANA-Zeitzone, z. B. `Europe/Berlin` |

Im Container ist `HB_DB_PATH` auf `/app/appdata/haushaltsbuch.db` gesetzt; das
Verzeichnis `/app/appdata` ist als Volume angelegt.

---

## Sicherheit

> **Keine eingebaute Authentifizierung.** Die Anwendung ist für den Betrieb in
> einem vertrauenswürdigen Netzwerk bzw. hinter einem Reverse-Proxy/VPN gedacht
> und sollte **nicht direkt ins Internet** exponiert werden.

- Minimale Angriffsfläche: distroless-Basis, non-root (UID/GID 65532),
  statisches Binary, empfohlenes read-only Root-Filesystem (nur das Datenvolume
  ist beschreibbar).
- Alle SQL-Zugriffe sind parametrisiert.
- Das Container-Image wird in der CI mit Trivy auf CRITICAL/HIGH-Schwachstellen
  gescannt.

---

## Entwicklung

```sh
make help      # verfügbare Targets
make build     # statisches Binary nach bin/
make test      # Tests mit Race-Detector
make vet       # go vet
make generate  # templ-Templates neu generieren (*_templ.go)
make docker    # Container-Image bauen
```

Das Web-UI nutzt [templ](https://templ.guide). Nach Änderungen an `*.templ`
müssen die generierten Dateien mit `make generate` (bzw.
`go tool templ generate`) aktualisiert und **mitkommittet** werden – dadurch
baut das Projekt ohne die templ-Toolchain.

### Projektstruktur

```
cmd/haushaltsbuch/      Einstiegspunkt (Flags, Wiring, Graceful Shutdown)
internal/config/        Konfiguration aus HB_-Env-Variablen
internal/store/         SQLite-Zugriff + Migrationen
internal/calc/          Monatsberechnung (Normalisierung, Aufteilung)
internal/server/        HTTP-Routing, Handler, PDF-Export
internal/web/           templ-Templates, Assets, View-Models, Formatierung
internal/logbuf/        In-Memory-Log-Puffer
internal/version/       Build-Metadaten
```