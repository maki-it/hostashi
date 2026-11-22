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
	Certs    func(ctx context.Context) ([]Cert, error)
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
		} else if ev, ok := w.backupEvent(s); ok {
			w.notify(ctx, ev)
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

	var failed []string

	for _, it := range s.Items {
		if it.Error == "" && !it.Partial {
			continue
		}

		what := it.Kind
		switch {
		case it.Database != "":
			what = "database " + it.Database
		case it.Site != "":
			what = "files of " + it.Site
		}

		if it.Partial && it.Error == "" {
			what += " (some files could not be read)"
		} else {
			what += ": " + firstLine(it.Error)
		}

		failed = append(failed, "- "+what)
	}

	if s.OK && len(failed) == 0 {
		return Event{}, false
	}

	lines := []string{"the backup that finished on " + when(s.Finished) + " did not save everything."}
	if s.Error != "" {
		lines = append(lines, "Error: "+firstLine(s.Error))
	}

	if len(failed) > 0 {
		const most = 20

		lines = append(lines, "", "What failed:")
		if len(failed) > most {
			failed = append(failed[:most], fmt.Sprintf("- and %d more", len(failed)-most))
		}

		lines = append(lines, failed...)
	}

	return Event{
		Category: Backups,
		Key:      "backup-failed:" + strconv.FormatInt(s.Finished.Unix(), 10),
		Window:   longWindow,
		Fragment: "backups",
		Mail:     mail.OpsEvent{Template: "backup-failed", Subject: "Backup failed", Lines: lines},
	}, true
}

// certEvents reports a failed renewal (at most once a day) and a
// certificate that expires within 14 or 3 days (once per threshold).
func (w *Watcher) certEvents(c Cert) []Event {
	now := w.Now()
	name, fragment := "the certificate of the site "+c.Name, "sites/"+c.Site

	if c.Site == "" {
		name, fragment = "the panel's certificate for "+c.Name, ""
	}

	var out []Event

	if c.Error != "" && !c.FailedAt.IsZero() && now.Sub(c.FailedAt) <= recent {
		lines := []string{"Hostashi could not renew " + name + " on " + when(c.FailedAt) + ".", "Error: " + firstLine(c.Error)}
		if !c.NotAfter.IsZero() {
			lines = append(lines, "The current certificate is valid until "+when(c.NotAfter)+". Hostashi tries again.")
		}

		out = append(out, Event{
			Category: Certificates, Site: c.Site, Key: "cert-failed:" + c.Site + ":" + c.Name, Fragment: fragment,
			Mail: mail.OpsEvent{Template: "cert-renewal-failed", Subject: "Certificate renewal failed: " + c.Name, Lines: lines},
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
		line := strings.ToUpper(name[:1]) + name[1:] + " expires on " + when(c.NotAfter) + "."

		if left <= 0 {
			subject, line = "Certificate expired: "+c.Name, strings.ToUpper(name[:1])+name[1:]+" expired on "+when(c.NotAfter)+"."
		}

		out = append(out, Event{
			Category: Certificates, Site: c.Site, Window: longWindow, Fragment: fragment,
			Key: "cert-expiry:" + c.Site + ":" + c.Name + ":" + strconv.Itoa(days) + ":" + strconv.FormatInt(c.NotAfter.Unix(), 10),
			Mail: mail.OpsEvent{Template: "cert-expiry", Subject: subject, Lines: []string{
				line, "Sites keep working until then; after that, browsers show a warning.",
			}},
		})
	}

	return out
}

// doctorEvents turns a report into one daily digest of errors for admins,
// a weekly reminder per site on an old PHP version, and a note per disk,
// site or site's databases above 90 percent.
func (w *Watcher) doctorEvents(rep agentapi.DoctorReport) []Event {
	var (
		out    []Event
		digest []string
	)

	for _, c := range rep.Checks {
		switch {
		case c.ID == "site.php" && c.Status != agentapi.DoctorOK && c.Site != "":
			out = append(out, Event{
				Category: PHP, Site: c.Site, Key: "php-eol:" + c.Site, Window: week, Fragment: "sites/" + c.Site + "/php",
				Mail: mail.OpsEvent{Template: "php-eol", Subject: "Old PHP version on the site " + c.Site, Lines: []string{
					"The site " + c.Site + ": " + c.Summary + ".", c.Hint,
				}},
			})
		case c.ID == "site.disk" && c.Site != "" && used(c, false) >= fullPercent:
			what, _, _ := strings.Cut(c.Summary, ":")
			out = append(out, Event{
				Category: Disk, Site: c.Site, Key: "disk:" + c.Site + ":" + what, Fragment: "sites/" + c.Site + "/limits",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The site " + c.Site + " is almost full", Lines: []string{
					"The site " + c.Site + ", " + c.Summary + ".", c.Hint,
				}},
			})
		case c.ID == "site.database" && c.Site != "" && used(c, false) >= fullPercent:
			// A warning threshold only (ADR-0057): the databases keep growing.
			out = append(out, Event{
				Category: Disk, Site: c.Site, Key: "database:" + c.Site, Fragment: "sites/" + c.Site + "/limits",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The databases of the site " + c.Site + " passed 90 % of their warning threshold", Lines: []string{
					"The site " + c.Site + ", " + c.Summary + ".", c.Hint,
				}},
			})
		case c.ID == "system.disk" && used(c, true) >= fullPercent:
			path, _, _ := strings.Cut(c.Summary, ":")
			out = append(out, Event{
				Category: Disk, Key: "disk:server:" + path, Fragment: "health",
				Mail: mail.OpsEvent{Template: "disk-full", Subject: "The server's disk is almost full", Lines: []string{
					"Free space on the server, " + c.Summary + ".", c.Hint,
				}},
			})
		case c.ID == "hostashi.update" && c.Status == agentapi.DoctorWarn:
			// One email per new version (ADR-0050): the summary names it.
			out = append(out, Event{
				Category: Doctor, Key: "hostashi-update:" + c.Summary, Window: 30 * 24 * time.Hour, Fragment: "health",
				Mail: mail.OpsEvent{Template: "hostashi-update", Subject: "A new Hostashi version is available", Lines: []string{
					c.Summary + ".", c.Hint,
				}},
			})
		case c.Status == agentapi.DoctorFail && !ownCategory[c.ID]:
			line := "- " + c.Summary
			if c.Site != "" {
				line = "- site " + c.Site + ": " + c.Summary
			}

			digest = append(digest, line)
		}
	}

	if len(digest) > 0 {
		const most = 30

		if len(digest) > most {
			digest = append(digest[:most], fmt.Sprintf("- and %d more", len(digest)-most))
		}

		lines := append([]string{"the server checks found errors:", ""}, digest...)
		out = append(out, Event{
			Category: Doctor, Key: "doctor-digest:" + w.Now().UTC().Format(time.DateOnly), Fragment: "health",
			Mail: mail.OpsEvent{Template: "doctor-digest", Subject: "Server checks found errors", Lines: lines},
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

func when(t time.Time) string { return t.UTC().Format("2 January 2006, 15:04 UTC") }

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")

	return s
}
