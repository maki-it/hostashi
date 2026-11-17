# 4. Architektur (Vorschlag)

Alles hier ist ein Entwurf, der durch die Spikes in Phase 0 bestätigt oder verworfen wird.

## 4.1 Überblick

```text
 Browser / CLI / API-Clients / (später) Terraform
          │ HTTPS
          ▼
┌──────────────────────────────────────┐
│ hostashi serve        (User: hostashi) │  ohne Root, hört auf Panel-Port
│  • REST-API + eingebettete SPA        │
│  • AuthN/AuthZ, Audit, Job-Queue      │
│  • SQLite (Zustand, Audit, Jobs)      │
└──────────────┬───────────────────────┘
               │ Unix-Socket, SO_PEERCRED, typisierte RPC
               ▼
┌──────────────────────────────────────┐
│ hostashi agent        (User: root)     │  klein, kein Netzwerk-Listener
│  • feste Liste von Operationen        │
│  • Validierung, Reconciler            │
│  • schreibt nur in erlaubte Pfade     │
└───┬────────┬────────┬────────┬───────┘
    ▼        ▼        ▼        ▼
  Nginx   PHP-FPM   MariaDB   systemd / nftables / restic / ACME / useradd …
```

Ein Binary, drei Betriebsarten: `hostashi serve`, `hostashi agent`, `hostashi <cli-befehl>`. Die CLI spricht mit der API (Token) oder, als Root auf dem Server, direkt mit dem Agent.

## 4.2 Rechtetrennung (Kernentscheidung)

Panels dieser Klasse sind Angriffsziel Nummer eins: wer das Panel übernimmt, hat Root. Deshalb:

- Der Web-Prozess läuft **ohne Root** und ohne `sudo`. Er kann nichts außer dem Agent „bitten“.
- Der Agent hat **keinen Netzwerk-Listener**, nur einen Unix-Socket (`/run/hostashi/agent.sock`, Modus 0660, Gruppe `hostashi`). Peer-Credentials werden geprüft.
- Die RPC-Schnittstelle kennt **konkrete Operationen mit typisierten Parametern**, z. B. `SiteCreate{domain, type, phpVersion}`. Es gibt **kein** `Exec(cmd string)`.
- Prozesse werden ausschließlich mit **argv-Arrays** gestartet, nie über `sh -c`. Konfigurationen entstehen aus Go-Templates über typisierte Strukturen.
- Eingaben werden **positiv validiert** (Domain per Parser, User-Namen per fester Regel). Kein Blacklisting von Wörtern.
- Der Agent schreibt nur unter allowlist-Pfaden. Dateioperationen im Auftrag eines Site-Users laufen mit `openat2`/`RESOLVE_BENEATH` (oder `fchdir` in die Site-Wurzel plus Privilegien-Drop), damit Symlink-Tricks nicht herausführen.
- systemd-Hardening für beide Dienste (`NoNewPrivileges`, `ProtectSystem`, `CapabilityBoundingSet` auf das Nötige).

## 4.3 Deklarativer Zustand und Reconciler

1. Die DB hält den **Soll-Zustand** (Sites, Domains, DBs, Cron, Firewall, Backup-Policies …).
2. Der **Reconciler** im Agent berechnet daraus die Zielkonfiguration (Nginx-Vhosts, FPM-Pools, systemd-Units, Cron, nftables).
3. Er schreibt sie **atomar** in eine neue „Generation“ (`/var/lib/hostashi/generations/<n>/`), validiert (`nginx -t`, `php-fpm -t`, `nft -c`) und schaltet erst dann um und lädt neu.
4. Schlägt Validierung oder Health-Check fehl, wird die vorherige Generation wiederhergestellt und der Fehler im Job gemeldet.
5. **Drift-Erkennung:** Abweichungen von Hand geänderter, generierter Dateien werden im UI gezeigt (Übernehmen oder Überschreiben).
6. **Diff-Vorschau** vor dem Anwenden. Idempotent, dadurch lässt sich nach Absturz einfach neu aufsetzen.

**Eigene Anpassungen:** Pro Site gibt es Include-Dateien für benutzerdefinierte Nginx-Snippets (werden mitvalidiert). Wer den ganzen Vhost ersetzen will, markiert die Site als „unmanaged“. Das wird im UI klar gekennzeichnet.

## 4.4 Isolation pro Site

| Ebene | Maßnahme |
|-------|----------|
| Benutzer | Eigener Linux-User und Gruppe, Home `/home/<user>`, Web-Root darunter |
| PHP | Eigener FPM-Pool als Site-User, **Unix-Socket** mit Rechten `site-user:www-data 0660`, `open_basedir`, `disable_functions`-Defaults, Pool-eigene `php_admin_value` |
| Node/Python | systemd-Service als Site-User mit `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `ReadWritePaths` auf Site-Home |
| Ressourcen | cgroup v2 über systemd: `MemoryMax`, `CPUQuota`, `TasksMax`, `IOWeight` |
| Netzwerk | Optional: ausgehende Regeln je Site-UID über nftables (`meta skuid`) |
| Dateisystem | Quota je Site wo möglich (ext4/XFS-Projektquota) |

Runtimes liegen **systemweit und schreibgeschützt** unter `/opt/hostashi/runtimes/<name>/<version>` und werden per Symlink/Unit zugewiesen. Das ist reproduzierbarer als nvm pro User.

## 4.5 Webserver-Abstraktion

Ein Interface `WebServerDriver` (Render, Validate, Reload). Erste Implementierung: **Nginx** (offizielles nginx.org-Repo, HTTP/3). Caddy ist als Alternative zu evaluieren (JSON-API, automatisches HTTPS), siehe ADR-0005. Die Abstraktion soll nicht den kleinsten gemeinsamen Nenner erzwingen. Sie dient dazu, den Treiber später austauschen zu können.

## 4.6 Datenmodell (Auszug)

```text
Server ─┬─ User ── Role / SiteGrant
        ├─ Site ─┬─ Domain (primary, alias, redirect)
        │        ├─ Runtime {type, version, port|socket, limits}
        │        ├─ Certificate
        │        ├─ SiteUser (Linux) ── SshKey
        │        ├─ Database ── DbUser
        │        ├─ CronJob
        │        └─ BackupPolicy ── Snapshot
        ├─ FirewallRule
        ├─ Integration {provider, credentialRef}   (DNS, Storage, Cloud)
        ├─ ApiToken {scopes, expires}
        ├─ Job {status, log}
        └─ AuditEvent {actor, action, target, diff}
```

Geheimnisse (DB-Passwörter, API-Keys) liegen **verschlüsselt** in der DB (Schlüssel in `/etc/hostashi/`, Modus 0400, nur Agent/Serve-User), werden nie in Logs geschrieben und nur einmal angezeigt.

## 4.7 Speicher und Betrieb

- **SQLite (WAL)** für das Panel: kein zusätzlicher Dienst, einfache Backups (`VACUUM INTO`). Siehe ADR-0006.
- **Metriken:** Ringpuffer im Panel (z. B. 30 Tage, Auflösung abgestuft), kein Prometheus/Influx als Pflicht.
- **Jobs:** Persistente Warteschlange in SQLite, Fortschritt und Log je Job, Streaming per SSE/WebSocket.
- **Logs des Panels:** strukturiert (JSON) in journald.

## 4.8 Paketierung, Installation und Updates

- **Signierter APT-Repo** mit `.deb` für amd64/arm64 ist der Hauptweg. Zusätzlich ein Convenience-Skript, das nur das Repo einrichtet und `apt install hostashi` ausführt. Es verlangt keine Blind-Ausführung eines großen Skripts.
- Installer **idempotent** und mit Preflight (OS, RAM, Ports, bestehende Webserver). Er überschreibt nichts Fremdes, sondern bietet Adopt an.
- Releases: signiert (cosign), SBOM (CycloneDX/SPDX), reproduzierbare Builds als Ziel.
- Updates über APT oder UI. Vorher Snapshot der Panel-DB, nach dem Update Health-Check, bei Fehlschlag Rollback.
- Support-Matrix steht in der Doku und in `hostashi doctor`. Pro OS-Release gibt es einen CI-Lauf.

## 4.9 Technologiewahl (Vorschlag)

| Bereich | Vorschlag | Alternative |
|---------|-----------|-------------|
| Backend/Agent/CLI | **Go**: statisch gelinktes Binary, einfache Cross-Compile für arm64, gute Systemprogrammierung | Rust (sicherer, langsamere Entwicklung), PHP/Symfony (wie CloudPanel vermutlich, größerer Laufzeit-Fußabdruck) |
| Frontend | **SvelteKit oder React + Vite** als SPA, im Binary eingebettet, TypeScript | HTMX + templ (weniger JS, schwächer für Terminal/Live-Graphen) |
| API | REST + OpenAPI 3.1, Streaming per SSE | gRPC (nur intern zwischen serve und agent) |
| DB | SQLite | PostgreSQL (erst für Multi-Server) |
| ACME | lego als Bibliothek | certbot (Python-Abhängigkeit) |
| Backups | restic | rclone sync (keine Dedup/Verschlüsselung), borg |
| Tests | Go-Tests, Testcontainers/Incus-VMs, Playwright | |

Begründung und Konsequenzen: ADR-0003.
