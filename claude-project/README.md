# Claude-Projekt einrichten

Anlegen in claude.ai, ca. 3 Minuten:

1. In claude.ai links **Projects → New project**. Name: `Hostashi`. Beschreibung z. B. „Quelloffenes Hosting-Control-Panel, Alternative zu CloudPanel“.
2. **Set project instructions**: Inhalt von [`PROJEKTANWEISUNGEN.md`](PROJEKTANWEISUNGEN.md) einfügen.
3. **Add content → Upload files**: diese acht Dateien hochladen:
   - `../README.md`
   - `../docs/01-recherche-cloudpanel.md`
   - `../docs/02-vision-scope.md`
   - `../docs/03-features.md`
   - `../docs/04-architektur.md`
   - `../docs/05-sicherheit.md`
   - `../docs/06-roadmap.md`
   - `../docs/07-decisions.md`
4. In diesem Projekt einen neuen Chat starten, z. B. mit: „Hier sind meine Antworten auf die offenen Fragen aus 07-decisions: …“

## Pflege

Ein Projekt-Chat kann die Wissensdateien nicht selbst ändern. Der Stand bleibt nur erhalten, wenn du geänderte Dateien neu hochlädst. Die Anweisungen sorgen dafür, dass Claude am Ende jeder Sitzung die nötigen Änderungen nennt. Die Originale liegen weiter in diesem Git-Repo, das bleibt die Quelle der Wahrheit.

Die Menüführung in claude.ai ändert sich gelegentlich. Wenn die Bezeichnungen abweichen, such nach „Projects“ in der Seitenleiste.
