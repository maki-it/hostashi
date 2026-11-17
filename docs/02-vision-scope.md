# 2. Vision & Scope

## 2.1 Vision

Ein Server soll in unter fünf Minuten produktiv sein und danach nicht zur Sicherheitsbaustelle werden. Das Panel nimmt Admins Routinearbeit ab (Sites, TLS, Datenbanken, Backups, Updates), ohne dass sie ihm blind vertrauen müssen. Der Code ist offen, die Rechte sind minimal, der Zustand ist nachvollziehbar.

## 2.2 Zielgruppe (Vorschlag)

**Primär (v1):** Entwickler, Agenturen und Admins, die auf **einem oder wenigen eigenen Servern** (VPS, Root-Server) mehrere Kundensites betreiben. Das ist CloudPanels Nische.

**Später:** Kleine Hoster mit Reseller-Bedarf (Pakete, Quotas, White-Label), Multi-Server-Betrieb.

**Nicht Zielgruppe v1:** Shared-Hosting im Stil von cPanel mit tausenden Accounts.

Das ist eine Annahme, die du bestätigen musst (ADR-0010).

## 2.3 Positionierung und Differenzierung

| Hebel | Konkret |
|-------|---------|
| **Wirklich Open Source** | Gesamter Code inkl. Installer, Templates und Doku unter OSI-Lizenz. Community kann Sicherheitslücken finden und beheben. |
| **Rechtetrennung** | Web-UI läuft ohne Root. Ein kleiner Agent führt nur typisierte, allowlist-basierte Operationen aus. |
| **Isolation pro Site** | Eigener Linux-User, eigener FPM-Pool über Unix-Socket, systemd-Sandboxing, cgroup-Limits. |
| **Deklarativ und nachvollziehbar** | Zustand in DB, generierte Configs, Diff-Vorschau, Rollback, Export als YAML. |
| **CloudPanel-Import** | Bestehende Server übernehmen („adopt“), ohne Neuaufbau. |
| **Backups, die man testen kann** | Restic (verschlüsselt, dedupliziert), Restore-Test, Benachrichtigung bei Fehlern. |
| **Moderne Auth** | Passkeys (WebAuthn), TOTP, OIDC-SSO, API-Tokens mit Scopes. |
| **API-first** | Alles, was die UI kann, kann die API. CLI und später Terraform-Provider nutzen sie. |
| **Mehr Runtimes** | PostgreSQL, Docker-Compose-Apps, optional LXC-Betrieb. |

Nicht jeder Hebel muss in v1. Priorisierung in [Feature-Matrix](03-features.md).

## 2.4 Nicht-Ziele (v1)

- Eigener **Mailserver** (Integration mit Mailcow oder Stalwart später, siehe Phase 4).
- Eigener **DNS-Server** (stattdessen Provider-Integrationen).
- **Windows**, BSD, RHEL-Familie.
- **Shared-Hosting-Massenbetrieb**, Billing, Domain-Registrierung.
- **Kubernetes**.

## 2.5 Leitprinzipien

1. **Sicherheit vor Features.** Ein Feature, das Root-Rechte im Web-Prozess braucht, wird anders gebaut oder gar nicht.
2. **Generiert statt gepatcht.** Wir editieren keine Konfigdateien per Regex. Wir rendern sie aus dem Zustand.
3. **Nie ohne Rückweg.** Jede Änderung ist validiert und zurückrollbar.
4. **Boring Tech.** Nginx, PHP-FPM, MariaDB, systemd, restic, ACME: bewährte Bausteine statt Eigenbau.
5. **Ein Binary, wenig RAM.** Idle-Ziel: unter 100 MB für das Panel inklusive Agent.
6. **Kein Lock-in.** Sites bleiben normale Linux-User mit normalen Dateien. Wer das Panel entfernt, behält einen laufenden Server.
7. **Doku und Tests sind Teil des Features.**
