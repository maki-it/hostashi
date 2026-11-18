# 7. Entscheidungen (ADRs)

Alle Einträge sind **Vorschläge** (`Proposed`), bis du sie bestätigst. Format: Kontext, Optionen, Empfehlung, Konsequenzen. Bei Annahme wird der Status auf `Accepted` gesetzt und das Datum ergänzt. Spätere Änderungen bekommen eine neue ADR-Nummer statt Überschreiben.

| ADR | Thema | Status | Entscheidung nötig von dir? |
|-----|-------|--------|-----------------------------|
| 0001 | Clean-room und Umgang mit Referenz-Repos | Proposed | Nein, rechtliche Mindestregel |
| 0002 | Lizenz des Projekts | Accepted am 2026-11-18 ([adr/0002](adr/0002-license.md)), AGPL-3.0 + CC-BY-4.0 + DCO | Erledigt |
| 0003 | Sprache und Stack | Accepted am 2026-11-18 ([adr/0003](adr/0003-language-and-stack.md)) | **Ja** (Team-Know-how) |
| 0004 | Rechtetrennung Web/Agent | Proposed | Nein |
| 0005 | Webserver | Accepted am 2026-11-18 ([adr/0005](adr/0005-web-server.md)), nginx | Nein, S3 ist erledigt |
| 0006 | Zustandsspeicher und Reconciler | Proposed | Nein |
| 0007 | Isolationsmodell pro Site | Proposed | Nein |
| 0008 | Backups mit restic | Proposed | Nein |
| 0009 | Zielplattformen und Paketierung | Proposed | Teilweise |
| 0010 | Zielgruppe und Scope v1 | Proposed (Entwurf: [adr/0010](adr/0010-target-audience-and-v1-scope.md)) | **Ja** |
| 0011 | Name und Marke | Accepted (vorbehaltlich Markenrecherche) | Erledigt, Marke offen |
| 0012 | Sprache von Code und Doku | Proposed | Ja, kurz |
| 0013 | Geschäftsmodell und Governance | Proposed (Entwurf: [adr/0013](adr/0013-business-model-and-governance.md)) | **Ja** |
| 0014 | Name des CLI-Werkzeugs | Proposed | Ja, kurz |
| 0015 | Ressourcenlimits pro Site | Proposed (Entwurf: [adr/0015](adr/0015-per-site-resource-limits.md)) | **Ja** (Priorität Disk-Quota) |
| 0016 | Speicherort pro Site (Storage-Pools) | Proposed (Entwurf: [adr/0016](adr/0016-per-site-storage-location.md)) | **Ja** |
| 0017 | CloudPanel-Import als Migration, Presets | Accepted am 2026-11-18 ([adr/0017](adr/0017-cloudpanel-import-and-presets.md)) | Erledigt |
| 0018 | Öffentliche REST-API | Accepted am 2026-11-18 ([adr/0018](adr/0018-public-rest-api.md)) | Erledigt |
| 0019 | PHP-Paketquelle und mehrere PHP-Versionen | Accepted (Kim, 2026-11-18, [adr/0019](adr/0019-php-package-source-and-versions.md)) | **Ja** |
| 0020 | nginx-Paketquelle und Brotli | Accepted am 2026-11-18 ([adr/0020](adr/0020-nginx-package-source-and-brotli.md)) | Erledigt |
| 0021 | Brute-Force- und Einbruchsschutz | Accepted am 2026-11-18 ([adr/0021](adr/0021-brute-force-and-intrusion-protection.md)) | Erledigt |
| 0022 | Metriken, Logs und Tracing | Proposed (Entwurf: [adr/0022](adr/0022-metrics-and-tracing.md)) | Nur Annahme durch dich |

---

## ADR-0001 Clean-room und Umgang mit Referenz-Repos

**Kontext.** `cloudpanel-ce` enthält keinen Quellcode. `vhost-templates` und `docs` haben keine LICENSE-Datei und sind damit urheberrechtlich geschützt. `dploy` ist MIT, `clp-wp-varnish-cache` GPLv3.

**Entscheidung (Vorschlag).**

- CloudPanel wird als **Funktionsreferenz** genutzt, nicht als Textvorlage. Keine Vhost-Templates, Doku-Texte, Screenshots oder UI-Texte übernehmen.
- Eigene Templates und Texte schreiben. Wer sie schreibt, sollte nicht 1:1 aus den Referenz-Dateien abschreiben, die Struktur ergibt sich aus der Nginx-Dokumentation und den Anwendungen selbst.
- MIT-Code (`dploy`) darf mit Lizenzhinweis verwendet werden. GPLv3-Code nur, wenn ADR-0002 das erlaubt.
- Der Name „CloudPanel“ wird nur nominativ-beschreibend im Vergleich genutzt („Alternative zu CloudPanel“).
- Rechtsprüfung durch eine Anwältin oder einen Anwalt vor der Veröffentlichung empfohlen. Das hier ist keine Rechtsberatung.

**Konsequenz.** Etwas mehr Aufwand, dafür keine Altlasten.

## ADR-0002 Lizenz des Projekts

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0002-license.md](adr/0002-license.md). Entschieden: Code unter AGPL-3.0-or-later, Doku und Übersetzungen unter CC-BY-4.0, Beiträge mit DCO statt CLA, kein Open Core. Nächster Schritt: `LICENSE`, Doku-Lizenz und DCO-Hinweis ins Repo. Vor jeder Veröffentlichung bleibt die Eigenprüfung (Lizenzen der Abhängigkeiten, Name und Marke) Pflicht. Keine Rechtsberatung.

Ursprünglicher Vergleich:

| Option | Pro | Contra |
|--------|-----|--------|
| **Apache-2.0** | Maximale Verbreitung, Patentklausel, Firmen nutzen es ohne Sorge | Forks dürfen proprietär werden |
| **AGPL-3.0** | Schützt vor proprietären SaaS-Forks, GPL-kompatibel (Varnish-Plugin nutzbar) | Manche Firmen meiden AGPL, Beitragende zögern |
| **GPL-3.0** | Klassisch für Panels (HestiaCP), Copyleft | Schützt nicht vor SaaS-Nutzung ohne Quellfreigabe |
| **Open Core** (z. B. Apache + proprietäre Pro-Module) | Finanzierungsmodell | Braucht CLA, Community-Misstrauen, doppelte Pflege |

**Empfehlung:** Apache-2.0 für die Verbreitung, wenn das Ziel Community ist. AGPL-3.0, wenn der Schutz vor einem „Hoster klont und verkauft es“ wichtiger ist. Die Entscheidung hängt an ADR-0013. Vor der ersten Zeile veröffentlichtem Code festlegen, nachträgliche Änderung ist mit Beitragenden mühsam. Falls Dual-Licensing denkbar ist, **von Anfang an CLA statt DCO** verwenden.

## ADR-0003 Sprache und Stack

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0003-language-and-stack.md](adr/0003-language-and-stack.md). Kurzfassung: Go bestätigt; Spikes S1 und S4 haben nichts widerlegt. S2 und S7 stehen noch aus, ein Scheitern von S2 öffnet die ADR wieder, S7 darf das Frontend-Framework ändern.

Ursprünglicher Vorschlag:

**Vorschlag:** Go für Backend, Agent und CLI. SPA (Svelte oder React) eingebettet. SQLite. Begründung: ein statisches Binary, kleiner Footprint, einfache arm64-Builds, gute Standardbibliothek für Prozesse, Dateien und TLS, speichersicher gegenüber C/C++ und PHP-Fallstricken.

**Alternativen:** Rust (höhere Sicherheitsgarantien, langsamerer Start, kleinerer Kontributor-Pool), PHP/Symfony (wohl CloudPanels Basis, großer Kontributor-Pool in der Hosting-Szene, aber Laufzeit und Packaging schwerer), Python (viele Systembibliotheken, aber Laufzeitabhängigkeit).

**Zu entscheiden von dir:** Womit arbeitet dein Team am sichersten? Ein gut beherrschter Stack schlägt den „idealen“. Wenn niemand Go kann, ist das ein echter Kostenpunkt.

## ADR-0004 Rechtetrennung Web/Agent

Siehe [Architektur 4.2](04-architektur.md). Zwei Prozesse, typisierte RPC über Unix-Socket, keine Shell, keine allgemeine Exec-Schnittstelle.

**Konsequenz:** Mehr Anfangsaufwand je Operation (Schema, Validator, Handler). Dafür deutlich kleinere Angriffsfläche. Boilerplate wird durch Codegen aus einem Schema reduziert (Spike S1).

## ADR-0005 Webserver

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0005-web-server.md](adr/0005-web-server.md). Kurzfassung: nginx (nginx.org stable) hinter dem `WebServerDriver`-Interface, Kim hat sich nach Spike S3 für nginx entschieden. Gründe: eingebauter FastCGI-Cache, kleinerer Footprint, größte Rezeptbasis. Caddy bleibt als späterer zweiter Treiber möglich, LiteSpeed wird für v1 nicht genommen (proprietär bzw. eigene Konfigurationswelt).

## ADR-0006 Zustandsspeicher und Reconciler

SQLite (WAL) als Quelle der Wahrheit, generierte Configs in versionierten Generationen, Validierung vor Umschaltung, Rollback bei Fehler, Drift-Erkennung. Siehe [Architektur 4.3](04-architektur.md).

**Offen:** Wenn Multi-Server früh kommt, ist PostgreSQL passender. Nach 1.0 neu bewerten, nicht vorher.

## ADR-0007 Isolationsmodell pro Site

Linux-User je Site, FPM-Pool über Unix-Socket, systemd-Sandbox, cgroup-Limits. Container (Podman/Docker) als **optionaler** Site-Typ später. Begründung: gleiche Isolationsstärke wie etablierte Panels, aber ohne Container-Runtime als Pflichtabhängigkeit und mit wenig RAM.

## ADR-0008 Backups mit restic

Restic als Engine (Verschlüsselung, Deduplizierung, viele Backends, auch über Rclone). Datenbanken über konsistente Dumps (MariaDB: `mariadb-dump --single-transaction` bzw. `mariabackup`, PostgreSQL: `pg_dump`). Restore-Test als Feature. Alternative Borg ist ebenfalls gut, hat aber weniger Backends.

## ADR-0009 Zielplattformen und Paketierung

Start: **Debian 13 und Ubuntu 26.04** (Ubuntu 24.04 entfällt für v1 wegen ADR-0020; früher: Ubuntu 24.04 und Debian 13, plus 22.04/Debian 12), **amd64 und arm64**. Ubuntu 26.04 nachziehen, wenn die Abhängigkeiten stabil sind. Keine EOL-Systeme (CloudPanel unterstützt noch Debian 11, das wir bewusst nicht übernehmen). Distribution per signiertem APT-Repo und `.deb`.

**Nur frische Server (Kim, 2026-11-17):** Hostashi wird immer auf einem frisch installierten Server eingerichtet, kein Einbau in gewachsene Systeme. Empfohlenes Image: **Debian 13**, zweite unterstützte Option: **Ubuntu 24.04 LTS**. Ubuntu 26.04 LTS wird nachgezogen, sobald die Abhängigkeiten und Spikes S5 und S8 darauf laufen. Die Ersteinrichtung soll möglichst einfach und automatisiert sein, zum Beispiel ein Einzeiler oder ein cloud-init-Skript beim Bestellen (Spike S5).

Begründung (Allgemeinwissen, ungeprüft): Debian ist konservativer, hat weniger Umbrüche zwischen Paketversionen und keine zusätzlichen Dienste wie snap oder Werbehinweise des Herstellers. Das passt zu einem Server, der lange unauffällig laufen soll. Ubuntu LTS ist ebenfalls stabil und bei Anbietern weit verbreitet. Gegen Ubuntu 26.04 als erste Wahl spricht nur, dass es neu ist. Mehrere PHP-Versionen brauchen auf beiden Systemen ein zusätzliches Paket-Repository (zu prüfen). Offen: Quota-Module auf den Kernels beider Systeme, wird in Spike S8 geprüft (siehe [ADR-0015](adr/0015-per-site-resource-limits.md)).

**Zu entscheiden:** Wie weit zurück sollen wir unterstützen? Jede zusätzliche Version vergrößert die Matrix. Mit der Regel „nur frische Server“ spricht vieles dafür, 22.04 und Debian 12 nicht aufzunehmen.

## ADR-0010 Zielgruppe und Scope v1

**Vollständiger Entwurf (Englisch, Status Proposed):** [adr/0010-target-audience-and-v1-scope.md](adr/0010-target-audience-and-v1-scope.md). Kurzfassung: Option A (Einzelserver für Entwickler und Agenturen) mit Leitplanken.

Ursprünglicher Vorschlag: Einzelserver für Entwickler und Agenturen. Reseller, Mail, DNS-Server und Multi-Server sind Nicht-Ziele für v1 (siehe [Vision](02-vision-scope.md)).

## ADR-0011 Name und Marke

**Entscheidung (17.11.2026):** Das Projekt heißt **Hostashi**, ein Kunstwort aus „Host“ und einem Sushi-Anklang.

**Verworfen:**

- *Hostwerk*: Der Name wird bereits häufig genutzt, auch als Marke und als `.io`- und `.de`-Domain.
- Weitere Kandidaten, die geprüft und nicht gewählt wurden: Maki-Namen, Werft- und Küstenbegriffe, Nautik-Begriffe (Skerry, Keelson …), Hostwarte, Hostwacht, Hostmill, Hostkeep, Bentowerk, Servishi u. a.

**Stand der Prüfung (17.11.2026):**

- Domains `hostashi.de`, `.dev`, `.com`, `.io` waren laut RDAP-Abfrage **nicht registriert**.
- Die Websuche (US, standard) fand keinen Treffer zum Namen, nur Ähnliches (Hisashi als Vorname, Hondashi als Würzmittel). Das ist kein Beweis, die Suche ist unscharf.

**Noch zu tun, bevor irgendetwas veröffentlicht wird:**

1. Domains sichern (`.de`, `.dev`, `.com`, `.io`), solange sie frei sind.
2. Markenrecherche bei DPMA, EUIPO und USPTO (Klassen 9 und 42), idealerweise mit anwaltlicher Prüfung.
3. GitHub-Organisation und Paketnamen (apt, npm, Go-Modul) prüfen, das war in der Recherche nicht erreichbar.
4. Klang und Bedeutung in den wichtigsten Zielsprachen prüfen.

**Stand 17.11.2026 (Angaben von Kim, nicht von Claude geprüft):**

- `hostashi.de` ist von Kim registriert. `.dev`, `.com` und `.io` bleiben vorerst unregistriert.
- Eigene Markenrecherche (Anleitung: `markenrecherche-anleitung.md` im Projektordner): keine Treffer. Das ist kein Beweis und ersetzt keine anwaltliche Prüfung.
- Das Repo bleibt in der GitHub-Organisation `maki-it`, es gibt keine eigene Organisation für Hostashi.

**Risiko:** Klingt der Name an „Hostinger“ oder andere „Host…“-Marken an, kann das im deutschen Markt zu Verwechslungsgefahr führen. Das ist ein Punkt für die Markenrecherche, nicht für diese Entscheidung.

## ADR-0012 Sprache von Code und Doku

Vorschlag: **Englisch** für Code, Kommentare, Issues, öffentliche Doku und UI-Standardsprache. Deutsch als vollständig gepflegte UI-Übersetzung. Diese Planungsdokumente sind deutsch, weil sie intern sind. Für die Veröffentlichung werden sie ins Englische übertragen.

## ADR-0013 Geschäftsmodell und Governance

**Vollständiger Entwurf (Englisch, Status Proposed):** [adr/0013-business-model-and-governance.md](adr/0013-business-model-and-governance.md). Kurzfassung: Hybrid aus eigenem Einsatz, Community-Projekt mit Sponsoring und bezahlten Services, ohne Open Core in v1, DCO statt CLA.

Ursprüngliche Optionen:

Optionen: (a) reines Community-Projekt mit Spenden/Sponsoring, (b) kostenlos plus bezahlter Support und Hosting-Angebote, (c) Open Core mit Pro-Funktionen (z. B. Multi-Server, Reseller, SSO), (d) internes Werkzeug deiner Firma, das du später öffnest. Das bestimmt Lizenz (ADR-0002), Beitragsregeln (CLA/DCO), Roadmap-Priorität und Finanzierung des Pentests.

## ADR-0014 Name des CLI-Werkzeugs

**Vorschlag:** Binary, Paket und Dienste heißen **`hostashi`**. Kurzform als optionaler Alias (Symlink): **`ashi`**.

```bash
hostashi site add example.com --type php --php 8.4
ashi site add example.com --type php --php 8.4   # gleiches Programm
```

**Begründung:** Ein Name für alles (Binary, `.deb`, systemd-Units `hostashi.service` und `hostashi-agent.service`, Konfig unter `/etc/hostashi`) vermeidet Verwirrung. Der Alias spart Tipparbeit. Kein `…ctl`-Suffix wie bei `clpctl`, weil das Tool auch den Server (`hostashi serve`) und den Agent (`hostashi agent`) startet.

**Verworfen:** `hostctl` (meines Wissens existiert ein bekanntes Tool gleichen Namens für die hosts-Datei), `hashi` (Verwechslung mit HashiCorp), `hsi` (Xilinx-Werkzeug), `hostashictl` (zu lang).

**Offen:** Ob `ashi` oder `hostashi` auf Debian/Ubuntu schon als Paket oder Programm existiert, ist **nicht geprüft** (im Container waren keine Paketlisten verfügbar). Vor dem ersten Release gegen die Paketquellen von Debian und Ubuntu abgleichen.

## ADR-0015 Ressourcenlimits pro Site

**Vollständiger Entwurf (Englisch, Status Proposed):** [adr/0015-per-site-resource-limits.md](adr/0015-per-site-resource-limits.md). Kurzfassung: Speicherplatz je Site per Dateisystem-Quota (bevorzugt Projekt-Quota auf ext4/XFS), CPU/RAM/Prozesse über ein systemd-Slice je Site mit eigenem PHP-FPM-Master, `pm=ondemand` als Standard, Datenbank nur mit Verbindungslimit und Größenwarnung (MariaDB hat keine Größenquota), Nginx-Rate-Limits optional, Traffic-Kontingente später. Disk-Quota soll von P2 auf P1 hochgestuft werden. Alle Zahlen und Mechanismen sind unverifiziert, Spike S8 (Quota auf dem echten Server, RAM-Messung) klärt das.

## ADR-0016 Speicherort pro Site (Storage-Pools)

**Vollständiger Entwurf (Englisch, Status Proposed):** [adr/0016-per-site-storage-location.md](adr/0016-per-site-storage-location.md). Kurzfassung: Der Speicherort ist wählbar über benannte **Storage-Pools** (zum Beispiel ein zweites Volume), nie über einen freien Pfad aus der API. Der Agent prüft Mountpoint, Marker-Datei und Quota je Pool, damit ein nicht eingehängtes Volume nicht die Root-Platte füllt. Das Home-Verzeichnis der Site liegt direkt im Pool, ohne Symlinks. Hostashi formatiert oder partitioniert keine Platten. Umzug zwischen Pools ist ein späterer, eigener Vorgang. Alle Mechanismen sind unverifiziert, Spike S9 klärt das.

## ADR-0017 CloudPanel-Import als Migration, Presets

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0017-cloudpanel-import-and-presets.md](adr/0017-cloudpanel-import-and-presets.md). Kurzfassung: Grundlage ist Spike S6 mit zwei Läufen auf `web2`, Ergebnis 0 % sauber und 90 % übernehmbar. Kim hat entschieden, dass eine Site als übernommen gilt, wenn kein Blocker vorliegt und jede eigene Regel einmal geprüft wird. Der Importer ist ein **Migrationswerkzeug**: Er liest den alten Server nur lesend, und der neue Server zieht die Daten. Die wiederkehrenden Regeln werden **nicht 1:1 übernommen**, sondern als eigene, typisierte Presets neu entworfen. Je Muster ist bewertet, ob es übernommen, verbessert oder weggelassen wird; zum Beispiel werden Security-Header neu gebaut und `if`-Weiterleitungen werden typisierte Redirects. Kims Vorgaben vom 2026-11-18: PageSpeed fällt weg, Varnish bleibt und kommt zusammen mit dem Importer (Release 1.0), Presets sind je Site-Typ aktiv (WordPress, TYPO3, generisches PHP, Static usw.). Entschieden (Kim, 2026-11-18): Der Start erfolgt auf einem frischen Server, der alte CloudPanel-Server wird nach der Migration abgeschaltet. Eigene Snippets werden nach einmaliger Prüfung unverändert übernommen (Kim, 2026-11-18). HTTP/3 und Kompression sind standardmäßig an, Kim bevorzugt Brotli; der nginx.org-Standardbuild hat aber kein Brotli, daher bis zu einem Spike (S11, Vorschlag) zunächst gzip. Kim hat die Standard-Matrix je Site-Typ bestätigt (2026-11-18). Spike S11 (2026-11-18): Für die nginx.org-Builds gibt es kein Brotli-Modul, die Distro-Module passen nur zu Debians und Ubuntus eigenem nginx. Empfehlung: gzip als Standard, Brotli später ([Bericht](../spike/s11-brotli/REPORT.md)). Kim entscheidet.

## ADR-0018 Öffentliche REST-API

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0018-public-rest-api.md](adr/0018-public-rest-api.md). Kurzfassung: REST mit OpenAPI 3.1 als Vertrag, **API-first**: Jede Funktion der UI läuft über einen dokumentierten Endpunkt, UI und CLI sind Clients derselben API. Pfad `/api/v1/`, ab 1.0 nur kompatible Änderungen. Ressourcen über feste IDs statt Domains. Schreibzugriffe ändern den Soll-Zustand, lange Vorgänge laufen als Job mit SSE. API-Tokens mit Ablaufdatum und Scopes (`resource:action`, optional auf einzelne Sites begrenzt); jede Anfrage prüft Scope, Rolle und Besitz. Die API läuft im Web-Prozess ohne Root und nutzt nur typisierte Agent-Operationen. Benannter Konflikt: eigene Nginx-Snippets sind Freitext und brauchen deshalb einen eigenen Hochrisiko-Scope. Beispiel im Entwurf: Wartungs- und Parkseiten per API schalten. Entschieden (Kim, 2026-11-18): Die API läuft unter der Adresse des Panels, mit optionaler IP-Freigabeliste für Token-Zugriffe. Entschieden (Kim, 2026-11-18): Tokens sind standardmäßig höchstens 365 Tage gültig, unbegrenzte Tokens sind aber einfach anlegbar. Benannter Konflikt: Ein Token ohne Ablauf ist ein dauerhaftes Geheimnis; abgemildert durch Hash-Speicherung, Scopes, IP-Freigabeliste, Audit-Log, Widerruf per Klick und Anzeige „zuletzt benutzt“.

---

# Offene Fragen an dich

1. **Wofür ist das Projekt gedacht?** Eigener Bedarf (Hosting für eigene Kunden), öffentliches Open-Source-Projekt, kommerzielles Produkt oder eine Mischung? Das entscheidet Lizenz, Geschäftsmodell und Tempo.
2. **Wie groß ist das Team, und welche Sprachen beherrscht es?** Davon hängt ab, ob Go sinnvoll ist oder ein anderer Stack besser passt.
3. **Lizenz:** Erledigt, siehe ADR-0002 (AGPL-3.0, CC-BY-4.0 für Doku, DCO).
4. **Name:** Erledigt, siehe ADR-0011. Offen sind nur Domains und Markenrecherche.
5. **Scope v1:** Einzelserver-Panel für Devs/Agenturen, oder von Anfang an Reseller/Multi-Server?
6. **Wo soll das Repo liegen?** GitHub (Sichtbarkeit, Beiträge) oder dein eigener Git-Server (`git.prod.maki-it.de`)? Dort kann ich es auf Wunsch anlegen. Ich habe das noch nicht getan.
7. **Integrationen:** Sind Hetzner (Cloud, Robot, Storage Box), INWX-DNS und Lexware/Lexoffice-Anbindung für dich Pflicht oder nur nice-to-have? Das beeinflusst die Reihenfolge der Provider-Integrationen.
8. **Budget:** Gibt es Budget für einen externen Pentest vor 1.0 und für Rechtsprüfung (Marke, Lizenz)?

## ADR-0019 PHP-Paketquelle und mehrere PHP-Versionen

**Vollständiger Text (Englisch, Status Accepted):** [adr/0019-php-package-source-and-versions.md](adr/0019-php-package-source-and-versions.md). Kurzfassung: Kein eigener PHP-Fork (Kims Entscheidung), sondern offizielle Pakete. Weil Distros nur eine Version liefern (Debian 13: 8.4, Ubuntu 24.04: 8.3), die Sites aber unterschiedliche Versionen brauchen, ist die Empfehlung: Standard ist die Distro-Version, weitere Versionen kommen auf ausdrücklichen Wunsch über das Repository von sury.org (geprüft: PHP 5.6 bis 8.5 für amd64 und arm64 auf trixie und noble, im Test liefen 7.4, 8.1 und 8.4 parallel). Benannte Konflikte: Drittquelle mit Root-Installation, und alte Versionen sind unsicher, daher EOL-Warnung und Freigabeliste statt freier Paketnamen. Entschieden (Kim, 2026-11-18, nach Spike S10 angepasst): Standard sind PHP 8.3 bis 8.5, Legacy (nur auf ausdrücklichen Wunsch) 8.2, 8.1 und 7.4, ältere Versionen werden nicht angeboten. Spike S10 (2026-11-18): Mit einem Pin bleibt die Standardversion bei der Distro, weitere Versionen kommen von sury.org (Debian 13 und Ubuntu 24.04 getestet); ohne Pin würde die Standardversion beim nächsten Update still ersetzt. Nach dem offiziellen Release-Plan endet der Support für 8.2 am 2026-12-31, daher wird 8.2 gleich als Legacy geführt.

## ADR-0020 nginx-Paketquelle und Brotli

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0020-nginx-package-source-and-brotli.md](adr/0020-nginx-package-source-and-brotli.md). Kurzfassung: Kim will Brotli und HTTP/3 als Standard. Das nginx.org-Repository hat kein Brotli-Modul, die Brotli-Pakete der Distributionen passen nur zum nginx der jeweiligen Distribution (Spike S11). Geprüft in den Paketlisten und den Build-Regeln: Debian 13 (nginx 1.26.3) und Ubuntu 26.04 (nginx 1.28.3) haben HTTP/3 eingebaut und passende Brotli-Module, Ubuntu 24.04 (nginx 1.24) hat kein HTTP/3. Entschieden (Kim, 2026-11-18): nginx aus der Distribution samt Brotli-Modulen auf Debian 13 und Ubuntu 26.04, Ubuntu 24.04 entfällt für v1, unterschiedliche nginx-Versionen sind in Ordnung. Das ersetzt die Paketquelle aus ADR-0005 (nginx.org). Spike S12 (2026-11-18) hat in frischen Containern echte Anfragen über HTTP/2 und HTTP/3 mit Brotli, gzip und vorkomprimierten `.br`-Dateien auf beiden Distributionen bestätigt. Der Installer-Lauf auf Ubuntu 26.04 steht noch aus.

## ADR-0021 Brute-Force- und Einbruchsschutz

**Vollständiger Text (Englisch, Status Accepted am 2026-11-18):** [adr/0021](adr/0021-brute-force-and-intrusion-protection.md). Kurzfassung: Das Panel drosselt Logins schon selbst (geprüft im Code), schützt aber weder SSH noch die Sites und blockt nichts auf Netzebene. Empfehlung: fail2ban aus der Distribution ist in v1 standardmäßig an, mit den Jails `sshd`, `hostashi-panel` und `recidive`, Sperren über nftables, Loopback und die Admin-Freigabeliste werden nie gesperrt. Jails pro Site (etwa WordPress-Login) gibt es, sie sind aber standardmäßig aus. CrowdSec kommt frühestens später als Opt-in, weil es standardmäßig Angreifer-IPs an einen externen Dienst meldet. Folgen: Das Panel schreibt fehlgeschlagene Logins als strukturierte JSON-Zeile mit IP ins Journal (bisher nur ins Audit-Log in SQLite, geprüft), nginx bekommt Logs pro Site in einem Ordner, den die Site nicht beschreiben kann (bisher ein globales Log, geprüft), und der spätere Firewall-Reconciler darf die nftables-Tabelle von fail2ban nicht löschen. Benannter Konflikt: `fail2ban-client` braucht eine zweite `os/exec`-Ausnahme mit festen Argumenten. Entschieden (Kim, 2026-11-18): fail2ban ist standardmäßig an, der Installer fragt nicht. Die Site-Logs liegen unter `/var/log/nginx/sites/<site>/` und gehören root (Kim, 2026-11-18). Der Agent steuert fail2ban über `fail2ban-client` als zweite `os/exec`-Ausnahme mit festen Argumenten (Kim, 2026-11-18), eine eigene Sperr-Engine in Hostashi ist verworfen. Keine offenen Fragen mehr.

## ADR-0022 Metriken, Logs und Tracing

**Vollständiger Text (Englisch, Status Proposed):** [adr/0022](adr/0022-metrics-and-tracing.md). Kurzfassung: Metriken im Prometheus-Format unter `/metrics` mit `client_golang` (aktuell v1.25.0, geprüft am 2026-11-18). OpenTelemetry für Metriken ist verworfen (mehr Abhängigkeiten, beim Abruf dasselbe Ergebnis). Der Endpunkt ist standardmäßig aus und nur auf zwei Wegen erreichbar: mit API-Token mit eigenem Scope `metrics:read` oder über einen eigenen Listener nur auf Loopback, nicht über eine „kommt von localhost“-Prüfung, weil ein lokaler Proxy die sonst aushebelt. Gemessen werden HTTP-Anfragen (nach Route, nicht nach Pfad), Agent-Operationen, Login-Fehler (ohne Benutzer und IP), Zertifikatsablauf, pro Site PHP-FPM und Speicherplatz. Label `site` ist der Systemname, Domains sind nirgends ein Label (Kardinalität und Aufklärung). Daten pro Site liefert der Agent über eine neue lesende Operation `metrics.snapshot`. Tracing kommt nicht in v1; stattdessen eine Request-ID, die das Panel erzeugt und an den Agent weitergibt. Geprüft: Der Agent lehnt unbekannte Felder im Protokoll ab, Panel und Agent müssen das neue Feld also gemeinsam bekommen. Entschieden (Kim, 2026-11-18): Der Endpunkt ist standardmäßig aus. Der Zugang ohne Token läuft über einen eigenen Listener nur auf Loopback (Kim, 2026-11-18). Das Label `site` ist der Systemname der Site (Kim, 2026-11-18). Keine offenen Fragen mehr.
