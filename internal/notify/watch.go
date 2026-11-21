// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"fmt"
	"log/slog"
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
)

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
	Now      func() time.Time
	Log      *slog.Logger
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

func when(t time.Time) string { return t.UTC().Format("2 January 2006, 15:04 UTC") }

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")

	return s
}
