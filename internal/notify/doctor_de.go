// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// germanCheckTitles name the server checks in German emails, as the
// Health page does (web/src/locales/de/server.json, doctor.checks).
var germanCheckTitles = map[string]string{
	"system.os": "Betriebssystem", "system.disk": "Freier Speicher", "system.quota": "Speicherlimits",
	"system.proc": "Prozess-Privatsphäre", "system.pool": "Home-Verzeichnisse", "system.pool.volume": "Storage-Pools",
	"system.fstab": "Mount-Tabelle", "service.panel": "Panel", "panel.mail": "E-Mail", "panel.tls": "Panel-Zertifikat",
	"service.nginx": "Webserver", "service.mariadb": "MariaDB", "service.fail2ban": "Schutz vor Brute-Force",
	"service.fail2ban.jails": "Jails des Brute-Force-Schutzes", "system.firewall": "Firewall",
	"system.firewall.panel": "Firewall-Regeln aus dem Panel", "system.updates": "Sicherheitsupdates",
	"system.reboot": "Neustart", "system.php": "PHP nach Updates", "system.files": "Dateimanager",
	"system.ssh": "SSH-Anmeldung als Admin", "system.sysctl": "Kernel-Einstellungen",
	"service.valkey": "Objekt-Cache (Valkey)", "site.home": "Home-Verzeichnis", "site.disk": "Speicherbelegung",
	"site.database": "Datenbankgröße", "site.limits": "Ressourcen-Limits", "site.tls": "Zertifikat",
	"site.php": "PHP-Version", "site.cache": "Objekt-Cache", "hostashi.update": "Hostashi-Version",
	"hostashi.apt": "Hostashi-Paketliste",
}

// germanChecks are the German summary and hint of a check's Code. A
// {{name}} is replaced by the check's parameter.
var germanChecks = map[string][2]string{
	"system.disk.fail": {
		"{{path}}: {{free}} frei von {{total}} ({{percent}} %)",
		"Schaff jetzt Platz: Sites, Datenbanken und Backups schlagen fehl, wenn die Platte voll ist.",
	},
	"system.disk.warn": {
		"{{path}}: {{free}} frei von {{total}} ({{percent}} %)",
		"Die Platte füllt sich. Prüf die Belegung der Sites und alte Backups.",
	},
	"site.disk.fail": {
		"{{what}}: {{used}} von {{limit}} ({{percent}} %)",
		"Fast voll: Am Limit schlagen Schreibvorgänge fehl. Erhöhe das Limit oder schaff Platz.",
	},
	"site.disk.warn": {
		"{{what}}: {{used}} von {{limit}} ({{percent}} %)",
		"Füllt sich. Erhöhe das Limit oder schaff Platz.",
	},
	"site.database.full": {
		"Datenbanken: {{used}} von {{limit}} ({{percent}} %)",
		"Die Datenbanken wachsen über die Warnschwelle hinaus weiter und füllen die Platte des Servers. Räum alte Daten auf (Logs, Sessions, Revisionen) oder erhöhe die Schwelle.",
	},
	"site.php.unknown": {
		"Für PHP {{version}} sind keine Support-Daten bekannt",
		"Stell die Site auf eine unterstützte PHP-Version um.",
	},
	"site.php.ended": {
		"PHP {{version}} bekommt seit {{until}} keine Sicherheitsupdates mehr",
		"Stell die Site im Panel auf eine unterstützte PHP-Version um. Hostashi lässt sie weiterlaufen und stellt nie selbst um.",
	},
	"site.php.soon": {
		"PHP {{version}} bekommt nur noch bis {{until}} Sicherheitsupdates ({{days}} Tage)",
		"Plane den Umstieg auf eine neuere PHP-Version.",
	},
	"hostashi.update.available": {
		"Hostashi {{available}} ist verfügbar (installiert: {{installed}})",
		"Installiere es mit: sudo apt update && sudo apt upgrade. Das Update sichert vorher die Panel-Datenbank.",
	},
}

// germanParams translate parameter values that are words.
var germanParams = map[string]string{"disk space": "Speicherplatz", "files": "Dateien"}

// sizeParams hold a size with a decimal point.
var sizeParams = map[string]bool{"free": true, "total": true, "used": true, "limit": true}

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

// germanCheck returns the summary and hint of a check in German, or its
// English text when its code has no German wording or misses a value.
func germanCheck(c agentapi.DoctorCheck) (summary, hint string) {
	text, ok := germanChecks[c.Code]
	if !ok {
		return c.Summary, c.Hint
	}

	missing := false
	fill := func(s string) string {
		return placeholder.ReplaceAllStringFunc(s, func(m string) string {
			key := strings.Trim(m, "{}")

			v, ok := c.Params[key]
			if !ok {
				missing = true

				return m
			}

			if de, ok := germanParams[v]; ok {
				return de
			}

			if sizeParams[key] {
				return strings.Replace(v, ".", ",", 1) // "9.2 GiB" -> "9,2 GiB"
			}

			return v
		})
	}

	summary, hint = fill(text[0]), fill(text[1])
	if missing {
		return c.Summary, c.Hint
	}

	return summary, hint
}

// germanCheckTitle names a check in German; an unknown one by its ID.
func germanCheckTitle(id string) string {
	if t, ok := germanCheckTitles[id]; ok {
		return t
	}

	return id
}
