# 6. Roadmap

**Annahme für alle Zeitangaben:** 2 Entwickler in Vollzeit, einer davon mit Linux-Systemerfahrung, Frontend mindestens halbe Stelle. Mit einer Person verdoppelt sich die Dauer ungefähr, mit größerem Team wird sie wegen Abstimmung nicht proportional kürzer. Das sind **Schätzungen ohne Datengrundlage**, bitte nach Phase 0 neu kalibrieren.

## Phase 0: Fundament (3–4 Wochen)

Ziel: Entscheidungen treffen und die riskantesten Annahmen mit Wegwerf-Code prüfen.

**Entscheidungen:** Name und Markenprüfung, Lizenz, Stack, Zielgruppe, Hosting des Repos (siehe [ADRs](07-decisions.md)).

**Spikes** (jeweils 2–4 Tage, Ergebnis ist ein kurzer Bericht):

| Spike | Frage | Abbruchkriterium |
|-------|-------|------------------|
| S1 Agent-RPC | Reicht typisierte RPC über Unix-Socket mit Peer-Credentials, und bleibt der Code überschaubar? | Wenn mehr als 30 % Boilerplate pro Operation, Codegen einführen |
| S2 Reconciler | Generation-Umschaltung, `nginx -t`, Reload und Rollback robust? | Fehlerinjektion: kaputte Vhosts dürfen nie zu Downtime führen |
| S3 Nginx vs. Caddy | Welcher Treiber passt besser (Rewrites, HTTP/3, FastCGI-Cache, Templates)? | Entscheidung mit Messung |
| S4 Isolation | Per-Site-User, FPM-Socket, systemd-Sandbox, Escape-Test | Test aus Site A kann Site B nicht lesen |
| S5 Installer | Idempotenter Installer auf frischen Ubuntu 24.04 und Debian 13 (amd64 + arm64) | Läuft zweimal ohne Änderung |
| S6 CloudPanel-Adopt | Lässt sich eine echte CloudPanel-Instanz über das Dateisystem einlesen? | Wenn weniger als 80 % der Sites sauber übernommen werden, Importer zurückstellen |
| S7 UI-Prototyp | Svelte vs. React, Terminal und Live-Graphen, Bundle-Größe | |
| S8 Ressourcenlimits | Projekt-Quota auf frischem Debian 13 und Ubuntu 24.04 aktivierbar (idempotent, mit Reboot)? RAM je FPM-Master pro Site, Limits für Cron und Dienste ([ADR-0015](adr/0015-per-site-resource-limits.md)) | Wenn Quota nicht sicher aktivierbar ist, Alternativen (User-Quota, separates Volume) neu bewerten |

**Weitere Aufgaben:** Marktanalyse (HestiaCP, ISPConfig, 1Panel, Coolify …), CloudPanel-Issues auswerten, Bedrohungsmodell mit externem Reviewer durchgehen, Repo, CI und Release-Pipeline aufsetzen, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`.

**Abnahme:** ADRs auf „Accepted“, Spike-Berichte liegen vor, CI baut Binary für amd64/arm64, Test-VM-Matrix steht.

## Phase 1: Alpha (8–10 Wochen)

Ziel: Auf einem frischen Server lässt sich eine PHP-Site mit HTTPS und Datenbank produktiv betreiben.

- Installer/`.deb`, Ersteinrichtung per Token, Login mit TOTP, Rollen, API-Tokens, Audit-Log
- Site-Typen Static, PHP, Node.js, Reverse Proxy; Linux-User, FPM-Pool, Aliase und Redirects
- Vhost-Editor mit Validierung und Rollback
- Let's Encrypt (HTTP-01), eigene Zertifikate, Panel unter eigener Domain
- MariaDB/MySQL-Verwaltung, Import/Export
- Cron, Logs, SSH-Keys/SFTP
- Firewall mit Lockout-Schutz, Dienste-Verwaltung, Dashboard
- CLI mit Basisbefehlen, OpenAPI-Spezifikation

**Abnahme:** 20 Test-Szenarien (End-to-End) grün auf allen Matrix-OS, Isolations-Test grün, Doku für Installation und erste Site. Nur für Tests und Freiwillige, nicht für Produktion.

## Phase 2: Beta (10–12 Wochen)

Ziel: Funktionsparität für die häufigsten CloudPanel-Anwendungsfälle.

- Backups mit restic (Ziele, Zeitplan, Retention, Restore in UI) und Benachrichtigungen
- Python-Sites, WordPress-Schnellinstallation, Vhost-Templates (Laravel, Symfony, WP, Magento, Shopware …)
- Passkeys, Rate-Limiting, IP-/Bot-Blocking, Basic Auth je Site
- DNS-01 mit ersten Providern (INWX, Cloudflare, Hetzner DNS), Wildcard-Zertifikate
- Web-DB-Tool mit SSO, Redis, Dateimanager
- FastCGI-Cache, HTTP/3, Ressourcenlimits und Disk-Quota je Site
- Panel-Update im UI, `hostashi doctor`

**Abnahme:** Mindestens 10 externe Testserver über 4 Wochen ohne Datenverlust, Backup-Restore-Test erfolgreich, Security-Review der API-Autorisierung.

## Phase 3: Release 1.0 (8–10 Wochen)

Ziel: Vertrauenswürdig genug für Produktivserver.

- Adopt-Modus und CloudPanel-Importer
- PostgreSQL, Staging/Klonen, Zero-Downtime-Deployments
- Zustand als YAML (Export/Apply), OIDC-SSO, Prometheus-Endpunkt, Fail2ban/CrowdSec
- **Externer Pentest** und Behebung, Restore-Tests automatisiert
- Doku vollständig (EN), UI auf EN/DE, Migrationsleitfäden
- Signierte Releases, SBOM, Support- und Update-Richtlinie

**Abnahme:** Keine offenen kritischen oder hohen Findings aus dem Pentest, Upgrade-Tests von Beta auf 1.0, Release-Checkliste vollständig.

## Phase 4 und später (nicht geplant, nur Optionen)

Multi-Server (Controller + Agents), Reseller und Pakete, Mail-Integration (Mailcow/Stalwart), Docker-Compose-Apps, Git-Deploy, Terraform-Provider, Plugin-System, LXC-Betrieb, Cloud-Provider-Integrationen (Hetzner Cloud und Robot, Snapshots, Firewalls), Web-Terminal, Malware-Scan.

## Teststrategie

| Ebene | Werkzeug | Inhalt |
|-------|----------|--------|
| Unit | Go-Tests, Golden Files | Generierte Configs, Parser, Validatoren |
| Fuzzing | `go test -fuzz` | Domains, Pfade, Cron, Vhost-Snippets |
| Integration | Incus/QEMU-VMs (kein Docker-Ersatz für systemd) | Reconciler gegen echte Nginx/FPM/MariaDB |
| End-to-End | Playwright | Kern-Flows im UI |
| Matrix | CI je OS × Architektur | Ubuntu 22.04/24.04/26.04, Debian 12/13 × amd64/arm64 |
| Sicherheit | Isolations-Test, IDOR-Matrix, gosec, semgrep, govulncheck | Pro Release verpflichtend |
| Upgrade | Alte Version installieren, Daten anlegen, aktualisieren, prüfen | Pro Release |

## Risiken

| Risiko | Wahrscheinlichkeit | Wirkung | Gegenmaßnahme |
|--------|--------------------|---------|---------------|
| Sicherheitslücke nach Veröffentlichung beschädigt Vertrauen dauerhaft | mittel | hoch | Rechtetrennung, externer Pentest vor 1.0, Disclosure-Prozess, „Alpha/Beta“ ehrlich kennzeichnen |
| Pflegeaufwand für OS-/PHP-/DB-Versionen frisst das Team auf | hoch | hoch | Matrix klein halten, Templates statt Code, Automatisierung, Support-Zeiträume festlegen |
| Abhängigkeit von Drittrepos (sury.org, nginx.org, MariaDB) | mittel | mittel | Schlüssel pinnen, Mirror-Option, Doku zur Fallback-Quelle |
| Rechtliche Probleme durch Übernahme von CloudPanel-Inhalten | niedrig bei Disziplin | hoch | Clean-room, Review-Regel „nichts aus Referenz-Repos kopieren“, ADR-0001 |
| Namens- oder Markenkonflikt | mittel | mittel | Marken- und Domainsuche in Phase 0 |
| Scope Creep (Mail, DNS, Billing) | hoch | hoch | Nicht-Ziele in [Vision](02-vision-scope.md), Änderungen nur per ADR |
| Zu wenig Adoption gegen etablierte Panels | mittel | hoch | Klare Differenzierung (Sicherheit, Import, Offenheit), Doku, Community-Arbeit |
| Einzelpersonen-Abhängigkeit (Bus-Faktor) | hoch | hoch | Zweite Maintainer-Person früh gewinnen, Entscheidungen dokumentieren |
