# 7. Entscheidungen (ADRs)

Alle Einträge sind **Vorschläge** (`Proposed`), bis du sie bestätigst. Format: Kontext, Optionen, Empfehlung, Konsequenzen. Bei Annahme wird der Status auf `Accepted` gesetzt und das Datum ergänzt. Spätere Änderungen bekommen eine neue ADR-Nummer statt Überschreiben.

| ADR | Thema | Status | Entscheidung nötig von dir? |
|-----|-------|--------|-----------------------------|
| 0001 | Clean-room und Umgang mit Referenz-Repos | Proposed | Nein, rechtliche Mindestregel |
| 0002 | Lizenz des Projekts | Proposed (Entwurf: [adr/0002](adr/0002-license.md)) | **Ja** |
| 0003 | Sprache und Stack | Accepted am 2026-11-18 ([adr/0003](adr/0003-language-and-stack.md)) | **Ja** (Team-Know-how) |
| 0004 | Rechtetrennung Web/Agent | Proposed | Nein |
| 0005 | Webserver (Nginx, Caddy-Option) | Proposed | Nein, Spike S3 entscheidet |
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

**Vollständiger Entwurf (Englisch, Status Proposed):** [adr/0002-license.md](adr/0002-license.md). Kurzfassung: AGPL-3.0-or-later mit DCO, wenn ADR-0013 wie empfohlen angenommen wird. Apache-2.0, wenn maximale Verbreitung wichtiger ist. Bei möglichem Open Core oder Dual-Licensing von Anfang an CLA. Keine Rechtsberatung.

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

**Vorschlag:** Nginx (nginx.org-Repo) hinter einem `WebServerDriver`-Interface. Grund: riesiges Wissen und Rewrite-Beispiele für WordPress, Laravel, Magento, Shopware, etc., FastCGI-Cache, ausgereiftes HTTP/3 (ab 1.25), Nutzer kennen die Syntax aus CloudPanel.

**Alternative Caddy:** automatisches HTTPS, JSON-Admin-API, speichersicher, einfachere Generierung. Nachteil: weniger bekannte Rewrite-Rezepte, anderer Cache-Ansatz. Spike S3 entscheidet mit Messung.

## ADR-0006 Zustandsspeicher und Reconciler

SQLite (WAL) als Quelle der Wahrheit, generierte Configs in versionierten Generationen, Validierung vor Umschaltung, Rollback bei Fehler, Drift-Erkennung. Siehe [Architektur 4.3](04-architektur.md).

**Offen:** Wenn Multi-Server früh kommt, ist PostgreSQL passender. Nach 1.0 neu bewerten, nicht vorher.

## ADR-0007 Isolationsmodell pro Site

Linux-User je Site, FPM-Pool über Unix-Socket, systemd-Sandbox, cgroup-Limits. Container (Podman/Docker) als **optionaler** Site-Typ später. Begründung: gleiche Isolationsstärke wie etablierte Panels, aber ohne Container-Runtime als Pflichtabhängigkeit und mit wenig RAM.

## ADR-0008 Backups mit restic

Restic als Engine (Verschlüsselung, Deduplizierung, viele Backends, auch über Rclone). Datenbanken über konsistente Dumps (MariaDB: `mariadb-dump --single-transaction` bzw. `mariabackup`, PostgreSQL: `pg_dump`). Restore-Test als Feature. Alternative Borg ist ebenfalls gut, hat aber weniger Backends.

## ADR-0009 Zielplattformen und Paketierung

Start: **Ubuntu 24.04 und Debian 13** (plus 22.04/Debian 12), **amd64 und arm64**. Ubuntu 26.04 nachziehen, wenn die Abhängigkeiten stabil sind. Keine EOL-Systeme (CloudPanel unterstützt noch Debian 11, das wir bewusst nicht übernehmen). Distribution per signiertem APT-Repo und `.deb`.

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

---

# Offene Fragen an dich

1. **Wofür ist das Projekt gedacht?** Eigener Bedarf (Hosting für eigene Kunden), öffentliches Open-Source-Projekt, kommerzielles Produkt oder eine Mischung? Das entscheidet Lizenz, Geschäftsmodell und Tempo.
2. **Wie groß ist das Team, und welche Sprachen beherrscht es?** Davon hängt ab, ob Go sinnvoll ist oder ein anderer Stack besser passt.
3. **Lizenz:** Apache-2.0, AGPL-3.0, GPL-3.0 oder Open Core?
4. **Name:** Erledigt, siehe ADR-0011. Offen sind nur Domains und Markenrecherche.
5. **Scope v1:** Einzelserver-Panel für Devs/Agenturen, oder von Anfang an Reseller/Multi-Server?
6. **Wo soll das Repo liegen?** GitHub (Sichtbarkeit, Beiträge) oder dein eigener Git-Server (`git.prod.maki-it.de`)? Dort kann ich es auf Wunsch anlegen. Ich habe das noch nicht getan.
7. **Integrationen:** Sind Hetzner (Cloud, Robot, Storage Box), INWX-DNS und Lexware/Lexoffice-Anbindung für dich Pflicht oder nur nice-to-have? Das beeinflusst die Reihenfolge der Provider-Integrationen.
8. **Budget:** Gibt es Budget für einen externen Pentest vor 1.0 und für Rechtsprüfung (Marke, Lizenz)?
