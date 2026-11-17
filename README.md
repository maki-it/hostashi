# Hostashi

Ein modernes, **wirklich quelloffenes** Hosting-Control-Panel für einzelne Linux-Server – als Alternative zu [CloudPanel](https://www.cloudpanel.io/).

> **Status:** Phase 0 – Planung. Es gibt noch keinen Code. Der Name ist gewählt (vorbehaltlich Markenrecherche), Lizenz und Stack sind Vorschläge und stehen in [`docs/07-decisions.md`](docs/07-decisions.md) zur Entscheidung.

## Kurzfassung

- Sites für **PHP, Node.js, Python, statisch und Reverse Proxy**, je Site eigener Linux-User und eigene Runtime.
- **Security by Design:** Web-Prozess ohne Root, kleiner Root-Agent mit fest definierten Operationen, keine Shell-Strings.
- **Deklarativ:** Zustand in einer DB, Konfigurationen werden generiert, geprüft, angewendet und bei Fehlern zurückgerollt.
- **Import aus CloudPanel**, damit Umsteiger nicht von vorn anfangen müssen.
- Ein Binary, geringer Ressourcenverbrauch, Ubuntu/Debian auf amd64 und arm64.

## Dokumentation

| # | Dokument | Inhalt |
|---|----------|--------|
| 1 | [Recherche CloudPanel](docs/01-recherche-cloudpanel.md) | Was CloudPanel kann, was die Repos enthalten, rechtliche Lage, Schwächen |
| 2 | [Vision & Scope](docs/02-vision-scope.md) | Zielgruppe, Positionierung, Differenzierung, Nicht-Ziele |
| 3 | [Feature-Matrix](docs/03-features.md) | Features mit Priorität und Phase, Parität zu CloudPanel plus Mehrwert |
| 4 | [Architektur](docs/04-architektur.md) | Komponenten, Rechtetrennung, Reconciler, Datenmodell, Installer |
| 5 | [Sicherheit](docs/05-sicherheit.md) | Bedrohungsmodell und verbindliche Sicherheitsanforderungen |
| 6 | [Roadmap](docs/06-roadmap.md) | Phasen, Meilensteine, Abnahmekriterien, Teststrategie, Risiken |
| 7 | [Entscheidungen](docs/07-decisions.md) | ADRs: offene Entscheidungen mit Empfehlung |

## Nächster Schritt

Die Fragen in Abschnitt „Offene Fragen“ von [`docs/07-decisions.md`](docs/07-decisions.md) beantworten (Lizenz, Stack, Team, Geschäftsmodell). Danach beginnt Phase 0 mit den Spikes aus der [Roadmap](docs/06-roadmap.md).
