// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mail"
)

const (
	// checkEvery is how often the watcher looks; events are deduplicated.
	checkEvery = 15 * time.Minute
	// recent: an older result is not reported, for example after the
	// panel was off for a while.
	recent = 48 * time.Hour
	// longWindow keeps one email per backup run or certificate threshold.
	longWindow = 30 * 24 * time.Hour
	// doctorEvery: the checks read the whole server, so not every tick.
	doctorEvery = time.Hour
	// week is the window of the PHP end-of-life reminder (ADR-0036).
	week = 7 * 24 * time.Hour
	// fullPercent is the disk use that ADR-0044 reports.
	fullPercent = 90
)

// ownCategory lists the checks that have a category of their own, so the
// daily digest leaves them out.
var ownCategory = map[string]bool{"site.php": true, "site.disk": true, "site.database": true, "system.disk": true, "site.tls": true, "hostashi.update": true}

// percent reads the "(NN %)" at the end of a disk check's summary.
var percent = regexp.MustCompile(`\((\d{1,9}) %\)$`)

// Expiry thresholds of ADR-0044, in days, largest first.
var expiryDays = []int{14, 3}

// Cert is one certificate the panel manages: a site's, or the panel's own
// with an empty Site.
type Cert struct {
	Site     string
	Name     string
	NotAfter time.Time // zero: none yet
	Error    string    // the last failed attempt, if any
	FailedAt time.Time
}

// Watcher turns the state of backups and certificates into events.
type Watcher struct {
	Notifier *Notifier
	Backups  func(ctx context.Context) (agentapi.BackupStatus, error) // nil: no backups
	// BackupInfo gives the schedule, to notice a missing run; nil: not
	// checked.
	BackupInfo func(ctx context.Context) (agentapi.BackupInfo, error)
	Certs      func(ctx context.Context) ([]Cert, error)
	// Doctor runs the server checks; nil: none. It runs at most hourly.
	Doctor func(ctx context.Context) (agentapi.DoctorReport, error)
	Now    func() time.Time
	Log    *slog.Logger

	lastDoctor time.Time
}

// Run checks at start and then every 15 minutes until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()

	for {
		w.Check(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check looks once; failures are logged.
func (w *Watcher) Check(ctx context.Context) {
	if w.Backups != nil {
		if s, err := w.Backups(ctx); err != nil {
			w.Log.WarnContext(ctx, "notify: backup status", "err", err)
		} else {
			if ev, ok := w.backupEvent(s); ok {
				w.notify(ctx, ev)
			}

			w.checkOverdue(ctx, s)
		}
	}

	if w.Certs != nil {
		list, err := w.Certs(ctx)
		if err != nil {
			w.Log.WarnContext(ctx, "notify: certificates", "err", err)
		}

		for _, c := range list {
			for _, ev := range w.certEvents(c) {
				w.notify(ctx, ev)
			}
		}
	}

	if w.Doctor != nil && w.Now().Sub(w.lastDoctor) >= doctorEvery {
		rep, err := w.Doctor(ctx)
		if err != nil {
			w.Log.WarnContext(ctx, "notify: doctor", "err", err)

			return
		}

		w.lastDoctor = w.Now()

		for _, ev := range w.doctorEvents(rep) {
			w.notify(ctx, ev)
		}
	}
}

func (w *Watcher) notify(ctx context.Context, ev Event) {
	if _, err := w.Notifier.Notify(ctx, ev); err != nil {
		w.Log.WarnContext(ctx, "notify", "template", ev.Mail.Template, "err", err)
	}
}

// backupEvent reports a finished run that failed or saved only part.
func (w *Watcher) backupEvent(s agentapi.BackupStatus) (Event, bool) {
	if s.Running || s.Finished.IsZero() || w.Now().Sub(s.Finished) > recent {
		return Event{}, false
	}

	var failed, fehler []string

	for _, it := range s.Items {
		if it.Error == "" && !it.Partial {
			continue
		}

		what, was := it.Kind, it.Kind
		switch {
		case it.Database != "":
			what, was = "database "+it.Database, "Datenbank "+it.Database
		case it.Site != "":
			what, was = "files of "+it.Site, "Dateien von "+it.Site
		}

		if it.Partial && it.Error == "" {
			what += " (some files could not be read)"
			was += " (einige Dateien konnten nicht gelesen werden)"
		} else {
			what += ": " + firstLine(it.Error)
			was += ": " + firstLine(it.Error)
		}

		failed = append(failed, "- "+what)
		fehler = append(fehler, "- "+was)
	}

	if s.OK && len(failed) == 0 {
		return Event{}, false
	}

	lines := []string{"the backup that finished on " + when(s.Finished) + " did not save everything."}
	de := []string{"das Backup, das am " + wann(s.Finished) + " fertig wurde, hat nicht alles gesichert."}

	if s.Error != "" {
		lines = append(lines, "Error: "+firstLine(s.Error))
		de = append(de, "Fehler: "+firstLine(s.Error))
	}

	if len(failed) > 0 {
		const most = 20

		lines = append(lines, "", "What failed:")
		de = append(de, "", "Was fehlgeschlagen ist:")

		if len(failed) > most {
			failed = append(failed[:most], fmt.Sprintf("- and %d more", len(failed)-most))
			fehler = append(fehler[:most], fmt.Sprintf("- und %d weitere", len(fehler)-most))
		}

		lines = append(lines, failed...)
		de = append(de, fehler...)
	}

	return Event{
		Category: Backups,
		Key:      "backup-failed:" + strconv.FormatInt(s.Finished.Unix(), 10),
		Window:   longWindow,
		Fragment: "backups",
		Mail: mail.OpsEvent{
			Template: "backup-failed", Subject: "Backup failed", Lines: lines,
			German: mail.OpsText{Subject: "Backup fehlgeschlagen", Lines: de},
		},
	}, true
}

func (w *Watcher) checkOverdue(ctx context.Context, s agentapi.BackupStatus) {
	if w.BackupInfo == nil {
		return
	}

	info, err := w.BackupInfo(ctx)
	if err != nil {
		w.Log.WarnContext(ctx, "notify: backup settings", "err", err)

		return
	}

	if ev, ok := w.overdueEvent(info, s); ok {
		w.notify(ctx, ev)
	}
}

// overdueEvent reports a scheduled run that did not happen (ADR-0008, 8),
// once per last finished run.
func (w *Watcher) overdueEvent(info agentapi.BackupInfo, s agentapi.BackupStatus) (Event, bool) {
	if !agentapi.BackupOverdue(info.Settings, info.Ready, s, w.Now()) {
		return Event{}, false
	}

	every, jede := "every day", "jeden Tag"
	if h := info.Settings.IntervalHours; h > 0 && h < 24 {
		every, jede = "every "+strconv.Itoa(h)+" hours", "alle "+strconv.Itoa(h)+" Stunden"
		if h == 1 {
			every, jede = "every hour", "jede Stunde"
		}
	}

	lines := []string{
		"no backup has finished since " + when(s.Finished) + ", although backups are scheduled to run " + every + ".",
		"Check the timer with: systemctl status hostashi-backup.timer hostashi-backup.service",
	}
	de := []string{
		"seit " + wann(s.Finished) + " ist kein Backup fertig geworden, obwohl Backups " + jede + " laufen sollen.",
		"Prüf den Timer mit: systemctl status hostashi-backup.timer hostashi-backup.service",
	}

	return Event{
		Category: Backups,
		Key:      "backup-overdue:" + strconv.FormatInt(s.Finished.Unix(), 10),
		Window:   longWindow,
		Fragment: "backups",
		Mail: mail.OpsEvent{
			Template: "backup-overdue", Subject: "Backup missing", Lines: lines,
			German: mail.OpsText{Subject: "Backup fehlt", Lines: de},
		},
	}, true
}

// certEvents reports a failed renewal (at most once a day) and a
// certificate that expires within 14 or 3 days (once per threshold).
func (w *Watcher) certEvents(c Cert) []Event {
	now := w.Now()
	name, fragment := "the certificate of the site "+c.Name, "sites/"+c.Site
	name2 := "das Zertifikat der Site " + c.Name

	if c.Site == "" {
		name, fragment = "the panel's certificate for "+c.Name, "system"
		name2 = "das Zertifikat des Panels für " + c.Name
	}

	var out []Event

	if c.Error != "" && !c.FailedAt.IsZero() && now.Sub(c.FailedAt) <= recent {
		lines := []string{"Hostashi could not renew " + name + " on " + when(c.FailedAt) + ".", "Error: " + firstLine(c.Error)}
		de := []string{"Hostashi konnte " + name2 + " am " + wann(c.FailedAt) + " nicht erneuern.", "Fehler: " + firstLine(c.Error)}

		if !c.NotAfter.IsZero() {
			lines = append(lines, "The current certificate is valid until "+when(c.NotAfter)+". Hostashi tries again.")
			de = append(de, "Das aktuelle Zertifikat ist bis "+wann(c.NotAfter)+" gültig. Hostashi versucht es erneut.")
		}

		out = append(out, Event{
			Category: Certificates, Site: c.Site, Key: "cert-failed:" + c.Site + ":" + c.Name, Fragment: fragment,
			Mail: mail.OpsEvent{
				Template: "cert-renewal-failed", Subject: "Certificate renewal failed: " + c.Name, Lines: lines,
				German: mail.OpsText{Subject: "Zertifikat nicht erneuert: " + c.Name, Lines: de},
			},
		})
	}

	if c.NotAfter.IsZero() {
		return out
	}

	left := c.NotAfter.Sub(now)

	for i, days := range expiryDays {
		if left > time.Duration(days)*24*time.Hour || (i+1 < len(expiryDays) && left <= time.Duration(expiryDays[i+1])*24*time.Hour) {
			continue
		}

		subject := "Certificate expires soon: " + c.Name
		line := upper(name) + " expires on " + when(c.NotAfter) + "."
		betreff := "Zertifikat läuft bald ab: " + c.Name
		zeile := upper(name2) + " läuft am " + wann(c.NotAfter) + " ab."

		if left <= 0 {
			subject, line = "Certificate expired: "+c.Name, upper(name)+" expired on "+when(c.NotAfter)+"."
			betreff, zeile = "Zertifikat abgelaufen: "+c.Name, upper(name2)+" ist am "+wann(c.NotAfter)+" abgelaufen."
		}

		out = append(out, Event{
			Category: Certificates, Site: c.Site, Window: longWindow, Fragment: fragment,
			Key: "cert-expiry:" + c.Site + ":" + c.Name + ":" + strconv.Itoa(days) + ":" + strconv.FormatInt(c.NotAfter.Unix(), 10),
			Mail: mail.OpsEvent{Template: "cert-expiry", Subject: subject, Lines: []string{
				line, "Sites keep working until then; after that, browsers show a warning.",
			}, German: mail.OpsText{Subject: betreff, Lines: []string{
				zeile, "Bis dahin funktionieren die Sites, danach zeigen Browser eine Warnung.",
			}}},
		})
	}

	return out
}

// doctorEvents turns a report into one daily digest of errors for admins,
// a weekly reminder per site on an old PHP version, and a note per disk,
// site or site's databases above 90 percent.
func (w *Watcher) doctorEvents(rep agentapi.DoctorReport) []Event {
	var (
		out           []Event
		digest, liste []string
	)

	for _, c := range rep.Checks {
		sumDE, hintDE := germanCheck(c)

		switch {
		case c.ID == "site.php" && c.Status != agentapi.DoctorOK && c.Site != "":
			out = append(out, Event{
				Category: PHP, Site: c.Site, Key: "php-eol:" + c.Site, Window: week, Fragment: "sites/" + c.Site + "/php",
				Mail: mail.OpsEvent{Template: "php-eol", Subject: "Old PHP version on the site " + c.Site, Lines: []string{
					"The site " + c.Site + ": " + c.Summary + ".", c.Hint,
				}, German: mail.OpsText{Subject: "Alte PHP-Version auf der Site " + c.Site, Lines: []string{
					"die Site " + c.Site + ": " + sumDE + ".", hintDE,
				}}},
			})
		case c.ID == "site.disk" && c.Site != "" && used(c, false) >= fullPercent:
			what, _, _ := strings.Cut(c.Summary, ":")
			out = append(out, Event{
				Category: Disk, Site: c.Site, Key: "disk:" + c.Site + ":" + what, Fragment: "sites/" + c.Site + "/limits",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The site " + c.Site + " is almost full", Lines: []string{
					"The site " + c.Site + ", " + c.Summary + ".", c.Hint,
				}, German: mail.OpsText{Subject: "Die Site " + c.Site + " ist fast voll", Lines: []string{
					"die Site " + c.Site + ", " + sumDE + ".", hintDE,
				}}},
			})
		case c.ID == "site.database" && c.Site != "" && used(c, false) >= fullPercent:
			// A warning threshold only (ADR-0057): the databases keep growing.
			out = append(out, Event{
				Category: Disk, Site: c.Site, Key: "database:" + c.Site, Fragment: "sites/" + c.Site + "/limits",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The databases of the site " + c.Site + " passed 90 % of their warning threshold", Lines: []string{
					"The site " + c.Site + ", " + c.Summary + ".", c.Hint,
				}, German: mail.OpsText{Subject: "Die Datenbanken der Site " + c.Site + " haben 90 % ihrer Warnschwelle überschritten", Lines: []string{
					"die Site " + c.Site + ", " + sumDE + ".", hintDE,
				}}},
			})
		case c.ID == "system.disk" && used(c, true) >= fullPercent:
			path, _, _ := strings.Cut(c.Summary, ":")
			out = append(out, Event{
				Category: Disk, Key: "disk:server:" + path, Fragment: "health",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The server's disk is almost full", Lines: []string{
					"Free space on the server, " + c.Summary + ".", c.Hint,
				}, German: mail.OpsText{Subject: "Der Speicherplatz des Servers ist fast voll", Lines: []string{
					"Freier Speicherplatz auf dem Server, " + sumDE + ".", hintDE,
				}}},
			})
		case c.ID == "hostashi.update" && c.Status == agentapi.DoctorWarn:
			// One email per new version (ADR-0050): the summary names it.
			out = append(out, Event{
				Category: Doctor, Key: "hostashi-update:" + c.Summary, Window: 30 * 24 * time.Hour, Fragment: "health",
				Mail: mail.OpsEvent{Template: "hostashi-update", Subject: "A new Hostashi version is available", Lines: []string{
					c.Summary + ".", c.Hint,
				}, German: mail.OpsText{Subject: "Eine neue Hostashi-Version ist verfügbar", Lines: []string{
					sumDE + ".", hintDE,
				}}},
			})
		case c.Status == agentapi.DoctorFail && !ownCategory[c.ID]:
			line, zeile := "- "+c.Summary, "- "+germanCheckTitle(c.ID)+": "+sumDE
			if c.Site != "" {
				line, zeile = "- site "+c.Site+": "+c.Summary, "- Site "+c.Site+", "+germanCheckTitle(c.ID)+": "+sumDE
			}

			digest = append(digest, line)
			liste = append(liste, zeile)
		}
	}

	if len(digest) > 0 {
		const most = 30

		if len(digest) > most {
			digest = append(digest[:most], fmt.Sprintf("- and %d more", len(digest)-most))
			liste = append(liste[:most], fmt.Sprintf("- und %d weitere", len(liste)-most))
		}

		lines := append([]string{"the server checks found errors:", ""}, digest...)
		de := append([]string{"die Serverprüfungen haben Fehler gefunden:", ""}, liste...)
		out = append(out, Event{
			Category: Doctor, Key: "doctor-digest:" + w.Now().UTC().Format(time.DateOnly), Fragment: "health",
			Mail: mail.OpsEvent{
				Template: "doctor-digest", Subject: "Server checks found errors", Lines: lines,
				German: mail.OpsText{Subject: "Serverprüfungen haben Fehler gefunden", Lines: de},
			},
		})
	}

	return out
}

// used returns the used percent of a disk check, or -1 when the summary
// has none. The server's check gives the free percent.
func used(c agentapi.DoctorCheck, free bool) int {
	m := percent.FindStringSubmatch(c.Summary)
	if m == nil {
		return -1
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}

	if free {
		return 100 - n
	}

	return n
}

func when(t time.Time) string { return mail.FormatTime(t, mail.English) }

// wann is when for German emails.
func wann(t time.Time) string { return mail.FormatTime(t, mail.German) }

// upper starts s with a capital letter.
func upper(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")

	return s
}
