# 1. Recherche: CloudPanel

Stand der Recherche: 17.11.2026. Grundlage sind die fünf von dir genannten Repos (flach geklont und gelesen). Die GitHub-Issues habe ich **nicht** ausgewertet, die API war in dieser Session nicht freigeschaltet. Das lässt sich nachholen.

## 1.1 Was die Repos wirklich enthalten

| Repo | Inhalt | Lizenz (geprüft) | Folge für uns |
|------|--------|------------------|---------------|
| `cloudpanel-ce` | Nur `README.md`, Bilder und `.github`. **Kein Quellcode.** | keine | Es gibt nichts zu forken. CloudPanel ist „kostenlos“, aber nicht Open Source im Sinne der OSI. |
| `vhost-templates` | Nginx-Vhost-Vorlagen (v1, v2, v2-http3, v2-varnish) für ca. 30 Anwendungen | **keine LICENSE-Datei** | Rechtlich: alle Rechte vorbehalten. Als Referenz lesen, **nicht kopieren**. |
| `docs` | Docusaurus-Doku (v1, v2), rund 2000 Markdown-Dateien | **keine LICENSE-Datei** | Gute Funktionsreferenz, Texte nicht übernehmen. |
| `dploy` | CLI für Zero-Downtime-Deployment (PHP/Symfony, Composer-Projekt) | **MIT** (Copyright Anton Medvedev, also vermutlich ein Deployer-Fork, bitte prüfen) | Wiederverwendbar oder als Vorbild. |
| `clp-wp-varnish-cache` | WordPress-Plugin für Varnish-Purge | **GPLv3** (laut `readme.txt`) | Nur nutzbar, wenn unser Projekt GPL-kompatibel lizenziert ist. |

Die Lizenzangaben stammen aus den Dateien in den Klonen. Per GitHub-API konnte ich sie nicht gegenprüfen.

**Konsequenz:** Wir bauen *clean-room*. CloudPanel dient als Funktionsreferenz (was kann es, wie fühlt sich der Ablauf an), aber Templates, Texte und Strukturen schreiben wir selbst. Der Name „CloudPanel“ darf nur beschreibend im Vergleich auftauchen, nicht im Produktnamen. Siehe ADR-0001.

## 1.2 Funktionsumfang von CloudPanel v2 (aus der Doku)

**Plattform**

- Ubuntu 22.04/24.04/26.04 und Debian 11/12/13, x86-64 und ARM64, mindestens 1 Core, 2 GB RAM, 10 GB Disk.
- Nur VMs, **keine Container** (LXC, LXD, OpenVZ).
- Stack: Nginx 1.30 (HTTP/3, PageSpeed), MySQL 8.0/8.4, MariaDB 10.6–12.3, PHP 7.1–8.5, Redis, ProFTPD, Node.js 12–24 (nvm), Python 3.10–3.14, Varnish 7.x.
- Installation per Skript oder Marketplace-Image bei AWS, DigitalOcean, Hetzner, GCE, Azure, Oracle, Vultr.

**Sites** (Frontend-Bereich)

- Typen: WordPress, PHP (mit Vhost-Template), Node.js, Static HTML, Python, Reverse Proxy.
- Pro Site ein SSH-Benutzer („Site User“) mit Home unter `/home/<user>`.
- Automatische www/non-www-Weiterleitung, HTTP→HTTPS.
- Vhost-Editor mit Syntaxprüfung und automatischem Revert bei Fehlern, HTTP/3 pro Site schaltbar.
- TLS: Let's Encrypt, Cloudflare-Origin-Zertifikat, Import eigener Zertifikate.
- Datenbanken: anlegen/löschen, DB-User, phpMyAdmin, Export/Import, Remote-Zugriff, mehrere DB-Server, Master-Credentials.
- Dateimanager, SSH/SFTP/FTP-Benutzer, Cron-Jobs, Logs.
- Security je Site: IP-Blocking, Bot-Blocking, Basic Auth, Cloudflare-IP-Handling.
- PageSpeed-Modul, Varnish Cache mit WordPress-Plugin, Redis.

**Admin-Bereich**

- Firewall auf UFW-Basis, Basic Auth vor dem Panel, Panel-Port 8443.
- Benutzer mit drei Rollen: Admin, Site Manager, User (auf Sites beschränkt). 2FA (TOTP).
- Events (Audit-Log), Instanz-Verwaltung (Reboot, Dienste neu starten, Zeitzone, ProFTPD), Custom Domain fürs Panel.
- Remote-Backups über Rclone: S3, Wasabi, DO Spaces, Dropbox, Google Drive, Hetzner Storage Box, SFTP, eigene Rclone-Config. Cloud-Features (z. B. Snapshots bei Hetzner).
- Mehrsprachige UI (u. a. Finnisch, Ungarisch, Slowakisch, Thai).

**CLI `clpctl`**

- Root: Sites anlegen/löschen (PHP, Node, Static, Python, Reverse Proxy), DBs, Let's Encrypt, Benutzer, Vhost-Templates, Basic Auth, Cloudflare-IPs.
- Site-User: Berechtigungen zurücksetzen, DB-Export/Import, Varnish-Purge.

**Nicht enthalten:** Mailserver (Doku verweist auf Mailcow und externe SMTP-Anbieter), DNS-Server, Multi-Server-Verwaltung, Reseller-Funktionen, PostgreSQL, Container-Apps.

## 1.3 Release-Rhythmus

v2.5.0 (11/2024), 2.5.1 (03/2025), 2.5.2 (08/2025), 2.5.3 (12/2025), 2.5.4 (07/2026): ungefähr alle 3–7 Monate. Der Inhalt ist überwiegend neue OS-/PHP-/DB-Versionen und Bugfixes. Es gibt kaum neue Funktionen. Das ist die Lücke, die wir besetzen können.

## 1.4 Beobachtete Schwächen

Aus Changelog und Vhost-Template abgeleitet, keine Bewertung von CloudPanel als Ganzes:

- **Eingabevalidierung und Dateioperationen:** Ein Ordner namens „0“ führte beim Löschen dazu, dass das Elternverzeichnis gelöscht wurde (#542). Cron lehnte Befehle ab, die das Wort „admin“ enthalten (#582). Das deutet auf Filter statt Typisierung hin.
- **Rechte:** Falsche Dateirechte bei neuen Sites (#558). Beim Löschen einer Site blieb die User-Crontab liegen (#758). Eine PHP-FPM-Socket-Fehlkonfiguration wurde extern als Sicherheitslücke gemeldet (2.5.2). Mehrere Releases enthalten extern gemeldete Security-Fixes.
- **PHP-FPM über TCP:** Das Generic-Template nutzt `fastcgi_pass 127.0.0.1:<port>` und setzt `PHP_VALUE` aus der Nginx-Konfiguration. Unix-Sockets mit Besitzer-Rechten und Pool-Konfiguration wären sauberer.
- **Geschlossener Kern:** Fehler und Sicherheitslücken lassen sich nur über den Hersteller beheben, die Community kann nicht mitprüfen.
- **Keine Container, kein Postgres, kein Multi-Server, kein Staging.**

## 1.5 Marktumfeld (aus dem Gedächtnis, nicht recherchiert)

Vergleichbare Projekte, die du dir ansehen solltest, bevor du Scope festlegst: HestiaCP, ISPConfig, Virtualmin, 1Panel, Coolify, Dokploy, CapRover sowie kommerziell Plesk, cPanel, RunCloud, SpinupWP, Ploi, Enhance. Eine echte Marktanalyse (Stars, Aktivität, Lizenz, Zielgruppe, Schwächen) ist ein Task in Phase 0.
