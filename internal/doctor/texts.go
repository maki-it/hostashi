// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The texts of the checks in English and German (ADR-0071). A check names
// its summary and hint by key; a {{name}} in a text is the check's
// parameter. English is the Summary and Hint of the check; German is
// rendered from the same key and parameters where it is needed, for
// example in an email to an account that reads German.

// P holds the values of a text's placeholders. A value that is a phrase
// rather than a name or number, such as a list of findings, has its German
// form under the same name with "@de" appended.
type P = map[string]string

// text is one wording in both languages.
type text struct{ en, de string }

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

// render fills the placeholders of the text with key in the language.
// It reports false for an unknown key or a missing value.
func render(key string, german bool, p P) (string, bool) {
	t, ok := texts[key]
	if !ok {
		return "", false
	}

	tpl := t.en
	if german {
		tpl = t.de
	}

	missing := false
	out := placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		name := strings.Trim(m, "{}")

		if german {
			// A value that is a phrase brings its German form along.
			if v, ok := p[name+"@de"]; ok {
				return v
			}
		}

		v, ok := p[name]
		if !ok {
			missing = true

			return m
		}

		if german {
			return germanValue(name, v)
		}

		return v
	})

	return out, !missing
}

// germanValues translate parameter values that are words.
var germanValues = map[string]string{
	"disk space": "Speicherplatz", "files": "Dateien", "the database tool": "das Datenbank-Tool",
	"not found": "nicht gefunden", "permission denied": "Zugriff verweigert", "connection refused": "Verbindung abgelehnt",
}

// sizeParams hold a size or number with a decimal point.
var sizeParams = map[string]bool{"free": true, "total": true, "used": true, "limit": true}

func germanValue(name, v string) string {
	if de, ok := germanValues[v]; ok {
		return de
	}

	if sizeParams[name] {
		return strings.Replace(v, ".", ",", 1) // "9.2 GiB" -> "9,2 GiB"
	}

	return v
}

// Text returns the English text with key; an unknown key panics, as it
// is a programming error that the tests catch.
func Text(key string, p P) string {
	s, ok := render(key, false, p)
	if !ok {
		panic("doctor: text " + key + " is unknown or misses a value")
	}

	return s
}

// New builds a check from text keys; an empty hintKey means no hint.
func New(status, id, key, hintKey string, p P) Check {
	c := Check{ID: id, Status: status, Summary: Text(key, p), Code: key, Params: p}
	if hintKey != "" {
		c.Hint, c.HintCode = Text(hintKey, p), hintKey
	}

	return c
}

// NewSite is New for a check of one site.
func NewSite(status, id, site, key, hintKey string, p P) Check {
	c := New(status, id, key, hintKey, p)
	c.Site = site

	return c
}

func okT(id, key string, p P) Check { return New(agentapi.DoctorOK, id, key, "", p) }

func warnT(id, key, hintKey string, p P) Check { return New(agentapi.DoctorWarn, id, key, hintKey, p) }

func failT(id, key, hintKey string, p P) Check { return New(agentapi.DoctorFail, id, key, hintKey, p) }

// German returns the summary and hint of a check in German. A part
// without a German text, such as one from an older agent, stays English.
func German(c Check) (summary, hint string) {
	summary, hint = c.Summary, c.Hint

	if s, ok := render(c.Code, true, c.Params); ok && c.Code != "" {
		summary = s
	}

	if h, ok := render(c.HintCode, true, c.Params); ok && c.HintCode != "" {
		hint = h
	}

	return summary, hint
}

// germanTitles name the checks as the German Health page does.
var germanTitles = map[string]string{
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

// GermanTitle names a check in German; an unknown one by its ID.
func GermanTitle(id string) string {
	if t, ok := germanTitles[id]; ok {
		return t
	}

	return id
}
