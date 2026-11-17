# 5. Sicherheit

Sicherheit ist das Hauptverkaufsargument und gleichzeitig das größte Risiko des Projekts. Dieses Dokument legt Bedrohungsmodell und verbindliche Anforderungen fest. Es wird vor Phase 1 mit einem externen Review abgeglichen.

## 5.1 Schutzgüter

1. Root-Zugriff auf den Server (Panel-Kompromittierung = Vollkompromittierung).
2. Kundendaten der Sites (Dateien, Datenbanken, Backups).
3. Zugangsdaten (Panel-Logins, DB-Passwörter, API-Keys für DNS, Storage, Cloud).
4. Verfügbarkeit der gehosteten Sites.

## 5.2 Angreifer und Bedrohungen

| Angreifer | Typische Wege | Gegenmaßnahme (Abschnitt) |
|-----------|---------------|---------------------------|
| Internet-Scanner, Bots | Pre-Auth-Lücken im Panel, Default-Zugangsdaten, Brute Force, frische Installationen abgreifen | 5.3 Panel-Exposition, 5.4 Auth |
| Angreifer mit Kunden-Site (z. B. gehackte WordPress-Site) | Ausbruch in andere Sites, Lesen fremder Dateien, Missbrauch des Panels | 5.5 Isolation |
| Böswilliger oder kompromittierter Panel-User mit wenig Rechten | Rechteausweitung über API-Lücken (IDOR), Pfad-Traversal, Command Injection | 5.6 Eingaben, 5.4 AuthZ |
| Supply-Chain (Pakete, Installer, Repos von Dritten) | Manipuliertes Skript/Paket, kompromittierte Upstream-Repos | 5.7 Lieferkette |
| Ausgespähte Backups oder Offsite-Speicher | Klartext-Backups, geleakte Keys | 5.8 Daten |

## 5.3 Panel-Exposition

- **Keine Default-Zugangsdaten.** Ersteinrichtung nur mit Einmal-Token, den der Installer in der Konsole ausgibt. Der Token läuft ab.
- Panel-Port standardmäßig **nicht 443**, mit eigenem TLS. Optional: Bindung nur an Unix-Socket oder localhost hinter Nginx, Zugriff per IP-Allowlist oder VPN.
- Installer schlägt vor, den Panel-Port per Firewall auf bekannte IPs zu beschränken. Basic-Auth als zusätzliche Schicht optional.
- Strikte HTTP-Header (CSP ohne `unsafe-inline`, HSTS, `X-Content-Type-Options`, `frame-ancestors 'none'`), CSRF-Schutz, `SameSite`-Cookies.

## 5.4 Authentifizierung und Autorisierung

- Passwörter mit **Argon2id**, Mindestlänge statt Zeichenregeln, Prüfung gegen bekannte Leaks optional offline.
- **TOTP** ab Phase A, **Passkeys** ab Phase B. 2FA für Admins erzwingbar.
- Login-Rate-Limit und Lockout, einheitliche Fehlermeldungen.
- **Autorisierung serverseitig je Ressource** (RBAC mit Site-Grants). Jeder API-Handler prüft Rolle *und* Besitz. Automatisierter Test: Für jede Route wird mit jeder Rolle geprüft, dass nur Erlaubtes durchgeht (IDOR-Matrix).
- API-Tokens: Scopes, Ablaufdatum, nur Hash gespeichert, einmal sichtbar.
- Audit-Log append-only, enthält Akteur, Quelle-IP, Aktion, Zielobjekt, Diff (ohne Geheimnisse).

## 5.5 Isolation zwischen Sites

Siehe [Architektur 4.4](04-architektur.md). Zusätzlich verbindlich:

- Kein PHP-FPM auf TCP-Ports. Unix-Sockets mit enger Rechtevergabe.
- Konfiguration nur über Pool-Dateien, **nie** `PHP_VALUE`/`PHP_ADMIN_VALUE` aus dem Nginx-Vhost.
- Ein Test pro Release versucht aus einer Test-Site heraus, Dateien, Sockets und Prozesse einer zweiten Site zu erreichen. Schlägt das an, ist der Release blockiert.

## 5.6 Eingaben, Dateien, Prozesse

- Alles, was in Dateinamen, Konfigs, SQL oder Kommandos landet, hat einen **Typ** (Domain, UnixUserName, DbIdentifier, AbsolutePathUnder(root)) mit eigenem Parser. Rohstrings gelangen nicht in den Agent.
- **Keine Shell.** Prozess-Start nur über argv. Cron-Befehle sind Nutzerinhalt, werden aber als Datei geschrieben, nicht interpoliert.
- SQL für Administration (CREATE DATABASE, GRANT) über Identifier-Quoting und Prepared Statements, wo möglich.
- Dateimanager und Rechte-Reset: Pfadauflösung relativ zur Site-Wurzel mit Symlink-Schutz (`RESOLVE_BENEATH`). Löschen nur nach Bestätigung der aufgelösten Zielpfade. Tests mit Sonderfällen (`0`, `..`, Symlinks, Leerzeichen, Unicode, sehr lange Namen).
- Fuzzing für alle Parser (Domains, Vhost-Snippets, Cron-Ausdrücke, Pfade).

## 5.7 Lieferkette

- Releases signiert (cosign), SBOM, Abhängigkeiten über Lockfiles, `govulncheck` und Dependabot/Renovate in CI.
- APT-Repo mit signiertem `Release`-Index. Der Signierschlüssel liegt offline oder in HSM/KMS.
- Externe Pakete (Nginx, PHP von Ondřej Surý bzw. sury.org, MariaDB, Node) sind Abhängigkeiten von Dritten. Wir pinnen Schlüssel-Fingerprints, dokumentieren die Quellen und prüfen sie in CI.
- Der Installer führt **kein unsigniertes Remote-Skript** als Root aus, außer dem dokumentierten Einzeiler, dessen Inhalt reproduzierbar gehasht veröffentlicht wird.

## 5.8 Daten und Geheimnisse

- Backups immer clientseitig verschlüsselt (restic). Schlüssel getrennt vom Backup-Ziel, Export-Anleitung vorhanden.
- Geheimnisse in der DB verschlüsselt, Schlüsseldatei mit Modus 0400. Rotation vorgesehen.
- DB-Passwörter von Sites sind pro Site/DB unterschiedlich. Master-Credentials sind nur dem Agent bekannt, nie dem Browser.
- Logs enthalten keine Geheimnisse (Redaction-Test in CI).

## 5.9 Prozess

- `SECURITY.md` mit Meldeweg (E-Mail, PGP/Signal) und Reaktionszeiten. Coordinated Disclosure, CVE-Vergabe, Security-Advisories.
- Statische Analyse (gosec, semgrep) und Secret-Scanning in CI.
- **Externer Pentest vor 1.0** (Budget einplanen, siehe Roadmap).
- Bug-Bounty prüfen, sobald Finanzierung steht.
