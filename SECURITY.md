# Sicherheitsrichtlinie

## Was diese Anwendung ist

Haushaltsbuch ist ein selbst gehostetes Haushaltsbuch **ohne Anmeldung**. Es ist
dafür gebaut, im privaten Netz hinter einem Reverse Proxy zu laufen. Wer die
Seite erreicht, darf alles. Das ist eine bewusste Entscheidung, kein Versäumnis:
Die Datei mit den Zahlen liegt auf demselben Rechner, und eine Anmeldemaske, die
nur eine Anmeldemaske ist, schützt sie nicht.

Daraus folgt für den Betrieb:

- Die Seiten gehören **nicht ins offene Internet**. Wer sie von außen erreichbar
  machen will, setzt eine Zugangskontrolle davor (Cloudflare Access, ein
  VPN, Basic Auth im Proxy).
- `HB_API_TOKEN` sichert
  die Routen unter `/api/v1/`. Ist es nicht gesetzt, antwortet die API `503` —
  das ist der richtige Vorgabewert für eine Anwendung ohne Anmeldung.
- Ohne konfigurierte KI baut die Anwendung **keine ausgehenden Verbindungen**
  auf. Der optionale PDF-Import nutzt eine explizite Azure-App-Identität
  (`AZURE_CLIENT_SECRET` ist ein weiteres Geheimnis) für Anmeldung, Ermittlung
  der Deployments und Modellaufrufe. Geheimnisse gehören in die
  Laufzeitkonfiguration, nicht ins Repository oder in Backups.
- Beim manuell gestarteten Analysieren wird der lokal extrahierte Auszugstext
  einschließlich personenbezogener Finanzdaten an das gewählte Azure-Deployment
  gesendet; eine zusätzliche Bestätigungs-Checkbox gibt es nicht. PDFs werden
  nicht dauerhaft gespeichert; Text und Antworten nicht
  protokolliert. Azure-Verarbeitung, Kosten und Aufbewahrung richten sich nach
  der verwendeten Ressource. Wer die ungeschützten Seiten erreicht, kann auch
  diese Funktion nutzen und Kosten verursachen.
- Modellantworten sind untrusted Eingaben: Beträge, Daten, Währung und
  Referenzen werden validiert; ohne manuelle Freigabe zählen sie nicht als
  Ist-Zahlungen. Die Prüfung ersetzt keinen Vergleich mit dem Originalauszug.
  Es gibt weiterhin keine direkte Bankanbindung, Telemetrie oder externen
  Schriftarten.
- CSV und XLSX werden ausschließlich lokal eingelesen und benötigen keine
  KI-Verbindung. Zellinhalte sind untrusted Eingaben; Formeln, Makros,
  verschlüsselte Arbeitsmappen und alte XLS-Dateien werden nicht unterstützt.
  ZIP-, Tabellen- und Zellgrößen sind begrenzt. Ungültige Zeilen brechen den
  gesamten Import ab; externe Verknüpfungen werden nicht geladen.

## Eine Lücke melden

Melde einen Fund bitte **nicht** als öffentliches Issue, solange er ungeprüft
ist. Nutze stattdessen die private Meldung über GitHub:

**[Security Advisory melden](https://github.com/daknoblo/Haushaltsbuch/security/advisories/new)**

Hilfreich ist alles, was den Fund nachvollziehbar macht: die betroffene Version
(`/healthz` gibt sie aus), was du getan hast, was passiert ist und was du
erwartet hättest. Ein Ablauf, der den Fehler auslöst, ist mehr wert als eine
Einschätzung des Schweregrads.

Dies ist ein Freizeitprojekt einer einzelnen Person; eine Antwortzeit lässt sich
nicht zusichern. Was zugesichert wird: Jede Meldung wird gelesen und beantwortet,
und eine bestätigte Lücke wird behoben, bevor sie öffentlich beschrieben wird.

## Was in den Anwendungsbereich fällt

- Eine Möglichkeit, die Haushaltsgrenze zu überschreiten — also Daten eines
  anderen Haushalts zu lesen oder zu schreiben.
- Ein Weg, die API ohne gültiges Token anzusprechen.
- Cross-Site-Scripting, SQL-Injection, Path Traversal, Umgehung des
  Same-Origin-Schutzes oder der Content-Security-Policy.
- Ein Fehler, der die Datenbank beschädigt oder Daten verliert.

## Was nicht

- „Die Seiten sind ohne Anmeldung erreichbar." Siehe oben — das ist der Entwurf.
- Befunde, die eine bereits kompromittierte Maschine oder Zugriff auf das
  Datenverzeichnis voraussetzen.
- Fehlende Sicherheits-Header an einem Reverse Proxy, den dieses Projekt nicht
  ausliefert.

## Unterstützte Versionen

Es wird nur die jeweils neueste Fassung gepflegt. `:latest` folgt `main`, die
`v*.*.*`-Tags sind feste Stände. Ein Sicherheitsfehler wird im nächsten Tag
behoben, nicht in alten Ständen rückportiert.

| Version    | Unterstützt |
| ---------- | ----------- |
| neuester Tag | ja        |
| alles ältere | nein      |
