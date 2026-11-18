# 3. Feature-Matrix

**Priorität:** `P0` = ohne das kein Release, `P1` = für 1.0 nötig, `P2` = danach.
**Phase:** siehe [Roadmap](06-roadmap.md). A = Alpha (MVP), B = Beta, 1 = Release 1.0, 2+ = später.
**Quelle:** `CP` = Parität zu CloudPanel, `NEU` = Differenzierung.

## 3.1 Server und Plattform

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Installer (idempotent, signiert) und `.deb`-Paket | P0 | A | CP | Ubuntu 22.04/24.04, Debian 12/13, amd64 + arm64 |
| Ersteinrichtung per Einmal-Token (keine Default-Zugangsdaten) | P0 | A | NEU | Verhindert „erster Besucher wird Admin“ |
| Dienste verwalten (Start/Stop/Reload, Status) | P0 | A | CP | Nginx, PHP-FPM, DB, Redis. Abgestürzte Dienste startet systemd automatisch neu (`Restart=`), das Panel zeigt Neustarts an |
| Dashboard mit CPU/RAM/Disk/Netz | P0 | A | CP | Lokaler Ringpuffer, keine externe TSDB |
| Firewall (nftables/UFW) mit Vorgaben | P0 | A | CP | SSH-Lockout-Schutz (Rollback-Timer) |
| Zeitzone, Hostname, Reboot | P1 | B | CP | |
| Panel-Update im UI mit Preflight und Rollback | P1 | B | NEU | Hinweis im Panel, wenn ein Update verfügbar ist; optional automatische Updates (Kanal und Zeitfenster wählbar) |
| `hostashi doctor` (Selbstdiagnose) | P1 | B | NEU | |
| Betrieb auf Servern nur mit IPv6 | P1 | B | NEU | Panel, ACME, Backups und Paketquellen ohne IPv4 testen |
| UI auch auf dem Handy bedienbar | P1 | B | NEU | Responsives Layout als Anforderung an die SPA |
| Adopt-Modus (bestehenden Server übernehmen) | P1 | 1 | NEU | Basis für den Import |
| Betrieb in LXC/Incus-Containern | P2 | 2+ | NEU | CloudPanel kann das nicht |
| Multi-Server (Controller + Agents) | P2 | 2+ | NEU | Architektur vorbereiten, nicht bauen |

## 3.2 Sites und Runtimes

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Site-Typ Static | P0 | A | CP | |
| Site-Typ PHP (mehrere Versionen parallel, 7.4–8.5) | P0 | A | CP | FPM-Pool je Site, Unix-Socket |
| Site-Typ Node.js (systemd-Service, Versionen wählbar) | P0 | A | CP | |
| Site-Typ Reverse Proxy | P0 | A | CP | |
| Site-Typ Weiterleitung (alle Anfragen auf eine Ziel-URL) | P1 | B | NEU | Ohne Webroot und ohne Site-User |
| Site-Typ Python (WSGI/ASGI, venv) | P1 | B | CP | |
| WordPress-Schnellinstallation (WP-CLI) | P1 | B | CP | |
| Linux-User je Site, Home-Layout, Rechte-Reset | P0 | A | CP | |
| Domain einer Site ändern | P1 | B | NEU | Site intern über eine feste ID identifiziert, nie über die Domain (gilt ab Alpha). Vhost, Zertifikat und optional DB-Inhalte werden mitgezogen |
| Site deaktivieren, Wartungs- und Parkseiten | P1 | B | NEU | Eigene HTML-Seiten einmal im Panel hinterlegen und je Site per Schalter (UI, API, CLI) aktivieren, ohne Extra-Site und ohne Handarbeit im Vhost. Parkseiten auch für Domains ohne eigene Site. Optional Zugriff für erlaubte IPs während der Wartung |
| Aliase, Redirects, www/non-www, HTTP→HTTPS | P0 | A | CP | |
| Vhost-Templates (Laravel, Symfony, WP, Magento, Shopware, …) | P1 | B | CP | Eigene Templates, YAML-basiert, Vorschau |
| Vhost-Editor mit `nginx -t` und Rollback | P0 | A | CP | Eigene Snippets in Include-Dateien |
| Per-Site PHP-Einstellungen (memory_limit, …) | P0 | A | CP | Über Pool-Config, nicht über `PHP_VALUE` im Nginx. PHP-Version in der Site-Übersicht sichtbar; ungenutzte PHP-Versionen lassen sich abschalten |
| Ressourcenlimits je Site (CPU, RAM, Tasks, I/O) | P1 | B | NEU | systemd/cgroup v2 |
| Disk-Quota je Site | P1 | B | NEU | Projekt-Quota (ext4/XFS), von der Ersteinrichtung aktiviert, Details in [ADR-0015](adr/0015-per-site-resource-limits.md). Phase abhängig von Spike S8. Belegter Speicher je Site (Dateien und DB) wird im UI angezeigt, auch ohne Quota |
| HTTP/3 pro Site | P1 | B | CP | |
| Docker-Compose-Apps als Site-Typ | P2 | 2+ | NEU | Rootless Podman als Option prüfen |
| Staging/Klonen einer Site | P1 | 1 | NEU | Dateien + DB, Domain-Rewrite |
| Zero-Downtime-Deployments (Releases, Symlink, Rollback) | P1 | 1 | CP (dploy) | Eigene Umsetzung oder dploy-Anbindung (MIT) |
| Git-Deploy per Webhook | P2 | 2+ | NEU | |

## 3.3 TLS und Domains

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Let's Encrypt (HTTP-01), Auto-Renew | P0 | A | CP | Eingebauter ACME-Client (z. B. lego). Neue Aliase und Subdomains bekommen automatisch ein Zertifikat |
| Wildcard über DNS-01 | P1 | B | NEU | Provider: INWX, Cloudflare, Hetzner DNS, Route53 als Erste |
| Eigene Zertifikate importieren | P0 | A | CP | |
| Cloudflare-Origin-Zertifikate und Real-IP-Handling | P1 | B | CP | |
| Panel unter eigener Domain mit gültigem Zertifikat | P0 | A | CP | |
| DNS-Record-Verwaltung über Provider-API | P2 | 2+ | NEU | |

## 3.4 Datenbanken und Cache

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| MariaDB/MySQL: DB und User anlegen/löschen | P0 | A | CP | Typisierte Namen, keine String-SQL |
| Import/Export (gzip) | P0 | A | CP | |
| Web-DB-Tool mit SSO (Adminer oder phpMyAdmin) | P1 | B | CP | Nur kurzlebige Tokens, nie Master-Credentials im Browser |
| Remote-Zugriff per IP-Allowlist | P1 | B | CP | |
| Mehrere DB-Server | P2 | 2+ | CP | |
| PostgreSQL | P1 | 1 | NEU | |
| Redis (pro Site, Auth, Unix-Socket) | P1 | B | CP | |

## 3.5 Zugriff, Dateien, Jobs

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| SSH-Keys und SFTP je Site-User (chroot optional) | P0 | A | CP | |
| Klassisches FTP | P2 | 2+ | CP | Standardmäßig aus, SFTP bevorzugt |
| Dateimanager im Browser | P1 | B | CP | Operationen über Agent mit Pfad-Confinement |
| Cron-Jobs je Site (Validator statt Verbotslisten) | P0 | A | CP | systemd-Timer als Option. Jobs einzeln pausieren und sofort ausführen |
| Logs ansehen/tailen (Access, Error, PHP, App) | P0 | A | CP | Logrotate je Site |
| Web-Terminal | P2 | 2+ | NEU | Nur mit starker Auth und Audit |

## 3.6 Sicherheit und Zugang zum Panel

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Benutzer + Rollen (Admin, Site-Manager, User je Site) | P0 | A | CP | RBAC in der API erzwungen, nicht nur im UI |
| TOTP-2FA | P0 | A | CP | |
| Passkeys (WebAuthn) | P0 | A | NEU | Neben TOTP von Anfang an, Kim 2026-11-18. Phishing-resistent. Wiederherstellung über Recovery-Codes, mehrere Passkeys je Benutzer |
| OIDC-SSO | P2 | 1 | NEU | |
| API-Tokens mit Scopes, Ablaufdatum | P0 | A | NEU | |
| Audit-Log (wer, was, wann, Diff) | P0 | A | CP | Unveränderlich (append-only), exportierbar |
| IP-/Bot-Blocking, Basic Auth je Site | P1 | B | CP | |
| fail2ban (SSH, Panel, recidive) | P0 | A | NEU | Standardmäßig an, ADR-0021 |
| CrowdSec als Opt-in | P2 | 2+ | NEU | Meldet IPs an externen Dienst, ADR-0021 |
| Rate-Limiting und Lockout am Panel-Login | P0 | A | NEU | |
| Malware-Scan (ClamAV/Maldet) | P2 | 2+ | NEU | |

## 3.7 Backups und Wiederherstellung

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Backup je Site (Dateien + DB) per restic | P0 | B | CP | Konsistente DB-Dumps, verschlüsselt, dedupliziert |
| Ziele: S3-kompatibel, SFTP, Hetzner Storage Box, Rclone-Backends | P0 | B | CP | |
| Zeitpläne, Retention, Benachrichtigung bei Fehlern | P0 | B | NEU | |
| Restore in UI (komplett, einzelne Datei, einzelne DB) | P0 | B | NEU | |
| Restore-Test (automatische Stichprobe) | P1 | 1 | NEU | |
| Restore auf einem neuen Server (Umzug) | P1 | 1 | NEU | Frischer Hostashi-Server liest vorhandenes Backup-Ziel, zeigt die Sites und stellt sie mit Zustand wieder her. Kein Multi-Server |
| Server-Snapshots über Cloud-API (Hetzner u. a.) | P2 | 2+ | CP | |

## 3.8 Monitoring und Benachrichtigung

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Dienste-Health und Disk-Warnung | P1 | B | NEU | |
| Benachrichtigungen: E-Mail (SMTP), Webhook, Telegram/Slack | P1 | B | NEU | |
| Prometheus-Endpunkt | P1 | 1 | NEU | Authentifiziert, standardmäßig aus |
| Uptime-Check je Site | P2 | 2+ | NEU | |
| Traffic je Site (MB/GB pro Tag/Woche/Monat) | P2 | 2+ | NEU | Aus den Access-Logs, keine Besucheranalyse |

## 3.9 Caching und Performance

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| Nginx FastCGI-Cache mit Purge | P1 | B | NEU | Einfacher als Varnish, deckt 80 % ab |
| Varnish-Integration | P1 | 1 | CP | Mit dem CloudPanel-Importer zusammen (ADR-0017, Kim 2026-11-18). Eigene VCL und Purge; WP-Plugin `clp-wp-varnish-cache` ist GPLv3, nur bei kompatibler Lizenz nutzbar |
| Brotli/Gzip, Cache-Header-Presets | P1 | B | CP | |

## 3.10 Schnittstellen und Automatisierung

| Feature | Prio | Phase | Quelle | Anmerkung |
|---------|------|-------|--------|-----------|
| REST-API (OpenAPI 3.1) | P0 | A | NEU | UI nutzt dieselbe API, keine Funktion nur in der UI, Details in [ADR-0018](adr/0018-public-rest-api.md) |
| CLI (`hostashi`) mit Parität zu `clpctl` | P0 | A | CP | |
| Zustand als YAML exportieren/anwenden | P1 | 1 | NEU | Basis für GitOps |
| CloudPanel-Importer | P1 | 1 | NEU | Siehe unten und ADR-0017 (Migration, Spike S6) |
| Presets je Site-Typ (WordPress, TYPO3, PHP, Static …) | P1 | 1 | NEU | Typisierte Härtungs- und Cache-Bausteine statt Vhost-Text, ADR-0017 |
| Terraform-/OpenTofu-Provider | P2 | 2+ | NEU | |
| Plugin-System | P2 | 2+ | NEU | Erst nach stabiler API |
| Mehrsprachige UI (Start: EN, DE) | P1 | 1 | CP | Weblate o. ä. |

## 3.11 CloudPanel-Importer (Idee)

Zwei Wege, beide sind zu prüfen, weil CloudPanel keinen dokumentierten Exportpfad hat und der Kern geschlossen ist:

1. **Adopt per Dateisystem:** Vorhandene Sites, Site-User, Nginx-Vhosts, PHP-FPM-Pools, Cron-Dateien und Zertifikate scannen und in unseren Zustand überführen. Funktioniert unabhängig vom CloudPanel-Interna.
2. **Zustand aus der CloudPanel-DB lesen:** Schneller, aber an unbekannte Interna gekoppelt. Wir würden eine lokale SQLite-Datei interpretieren, deren Schema nirgends dokumentiert ist. Das ist **nicht verifiziert**, ich habe in der Doku keinen Hinweis darauf gefunden.

Empfehlung: Weg 1 zuerst. Spike in Phase 0 mit einer echten CloudPanel-Testinstanz.
