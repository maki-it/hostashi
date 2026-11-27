// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
)

// Thresholds of ADR-0055 [E].
const (
	// crashLimit crash restarts within servermon.CrashWindow mean a unit
	// keeps crashing.
	crashLimit = 3
	// crashWindow: at most one "keeps crashing" email per unit in it.
	crashWindow = 6 * time.Hour
	// okPolls polls in a row with the unit active end an outage.
	okPolls = 2
	// agentPolls failed polls in a row mean the agent is unreachable.
	agentPolls = 3
)

// serviceNames are the names emails use; an unknown ID is used as it is.
var serviceNames = map[string]string{
	"nginx":     "nginx (web server)",
	"mariadb":   "MariaDB (databases)",
	"ssh":       "SSH",
	"fail2ban":  "fail2ban",
	"firewall":  "the firewall",
	"adminer":   "Adminer (database tool)",
	"freshclam": "the virus scan's signature updates (freshclam)",
	"clamd":     "the virus scan's scanner (clamd)",
	"clamonacc": "the real-time virus scan (clamonacc)",
	"agent":     "the Hostashi agent",
	"php":       "PHP",
	"valkey":    "the object cache (Valkey)",
	"node":      "the Node.js app",
	"app":       "the app",
}

// germanServiceNames are the names in German emails.
var germanServiceNames = map[string]string{
	"nginx":     "nginx (Webserver)",
	"mariadb":   "MariaDB (Datenbanken)",
	"ssh":       "SSH",
	"fail2ban":  "fail2ban",
	"firewall":  "die Firewall",
	"adminer":   "Adminer (Datenbank-Tool)",
	"freshclam": "die Signatur-Updates des Virenscans (freshclam)",
	"clamd":     "der Scanner des Virenscans (clamd)",
	"clamonacc": "der Echtzeit-Virenscan (clamonacc)",
	"agent":     "der Hostashi-Agent",
	"php":       "PHP",
	"valkey":    "der Objekt-Cache (Valkey)",
	"node":      "die Node.js-App",
	"app":       "die App",
}

// outage is one unit that got a "down" or "keeps crashing" email.
type outage struct {
	id, site string
	since    time.Time
	ok       int // polls in a row with the unit active
}

// ServiceAlerts turns the service monitor's polls into emails (ADR-0055):
// a unit that failed or keeps crashing, the all-clear when it runs again,
// and an agent that does not answer. It keeps its state in memory; after
// a panel restart the first poll starts afresh.
type ServiceAlerts struct {
	Notifier *Notifier
	Log      *slog.Logger

	mu         sync.Mutex
	open       map[string]*outage
	agentFails int
	agentSince time.Time // first failed poll of the current run
	agentDown  bool      // the "unreachable" email went out
}

var _ servermon.Alerter = (*ServiceAlerts)(nil)

// ServicesPolled handles one successful poll.
func (a *ServiceAlerts) ServicesPolled(ctx context.Context, p servermon.ServicePoll) {
	for _, ev := range a.poll(p) {
		a.send(ctx, ev)
	}
}

// AgentUnreachable handles one poll the agent did not answer.
func (a *ServiceAlerts) AgentUnreachable(ctx context.Context, at time.Time, err error) {
	a.mu.Lock()

	a.agentFails++
	if a.agentFails == 1 {
		a.agentSince = at
	}

	send := a.agentFails == agentPolls
	if send {
		a.agentDown = true
	}

	since := a.agentSince
	a.mu.Unlock()

	if !send {
		return
	}

	a.send(ctx, Event{
		Category: Services, Key: "agent-down:" + unix(since), Window: longWindow, Fragment: "services",
		Mail: mail.OpsEvent{Template: "agent-down", Subject: "The Hostashi agent does not answer", Lines: []string{
			"the panel has not reached the Hostashi agent since " + when(since) + ".",
			"Error: " + firstLine(err.Error()),
			"",
			"Without the agent the panel cannot change sites, run backups or renew certificates. The sites themselves keep running.",
			"Check it on the server with: systemctl status hostashi-agent",
		}, German: mail.OpsText{Subject: "Der Hostashi-Agent antwortet nicht", Lines: []string{
			"das Panel erreicht den Hostashi-Agent seit " + wann(since) + " nicht mehr.",
			"Fehler: " + firstLine(err.Error()),
			"",
			"Ohne den Agent kann das Panel keine Sites ändern, keine Backups ausführen und keine Zertifikate erneuern. Die Sites selbst laufen weiter.",
			"Prüf ihn auf dem Server mit: systemctl status hostashi-agent",
		}}},
	})
}

func (a *ServiceAlerts) poll(p servermon.ServicePoll) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.open == nil {
		a.open = map[string]*outage{}
	}

	var out []Event

	if a.agentDown {
		out = append(out, Event{
			Category: Services, Key: "agent-back:" + unix(a.agentSince), Window: longWindow, Fragment: "services",
			Mail: mail.OpsEvent{Template: "agent-back", Subject: "The Hostashi agent answers again", Lines: []string{
				"the panel reaches the Hostashi agent again since " + when(p.At) + ". It did not answer from " + when(a.agentSince) + ".",
			}, German: mail.OpsText{Subject: "Der Hostashi-Agent antwortet wieder", Lines: []string{
				"das Panel erreicht den Hostashi-Agent seit " + wann(p.At) + " wieder. Er hat ab " + wann(a.agentSince) + " nicht geantwortet.",
			}}},
		})
	}

	a.agentFails, a.agentDown = 0, false

	crashed := map[string]bool{}

	for _, e := range p.Events {
		k := servermon.Key(e.Service, e.Site)

		switch e.Kind {
		case store.ServiceEventFailed:
			if o, ok := a.open[k]; ok {
				o.ok = 0

				continue
			}

			a.open[k] = &outage{id: e.Service, site: e.Site, since: e.At}
			out = append(out, downEvent(e))
		case store.ServiceEventCrashRestart:
			crashed[k] = true

			if o, ok := a.open[k]; ok {
				o.ok = 0

				continue
			}

			if n := p.Crashes[k]; n >= crashLimit {
				a.open[k] = &outage{id: e.Service, site: e.Site, since: e.At}
				out = append(out, crashingEvent(e, n))
			}
		}
	}

	states := make(map[string]agentapi.ServiceState, len(p.States))
	for _, st := range p.States {
		states[servermon.Key(st.ID, st.Site)] = st
	}

	for k, o := range a.open {
		st, ok := states[k]
		if !ok || !st.Installed {
			delete(a.open, k) // the site or the service is gone

			continue
		}

		if st.Active != "active" || crashed[k] {
			o.ok = 0

			continue
		}

		o.ok++
		if o.ok >= okPolls {
			delete(a.open, k)

			out = append(out, backEvent(o, p.At))
		}
	}

	return out
}

func (a *ServiceAlerts) send(ctx context.Context, ev Event) {
	if _, err := a.Notifier.Notify(ctx, ev); err != nil && a.Log != nil {
		a.Log.WarnContext(ctx, "notify", "template", ev.Mail.Template, "err", err)
	}
}

func downEvent(e store.ServiceEvent) Event {
	name, where, fragment := describe(e.Service, e.Site)
	name2, where2 := describeGerman(e.Service, e.Site)
	lines := []string{
		name + where + " stopped on " + when(e.At) + " and systemd gave up restarting it.",
	}
	de := []string{
		name2 + where2 + " ist am " + wann(e.At) + " ausgefallen, und systemd startet den Dienst nicht mehr neu.",
	}

	if e.Detail != "" {
		lines = append(lines, "Result: "+e.Detail)
		de = append(de, "Ergebnis: "+e.Detail)
	}

	lines = append(lines, "", "Hostashi sends another email when it runs again. The services page shows its state and recent restarts.")
	de = append(de, "", "Hostashi schickt eine weitere E-Mail, sobald der Dienst wieder läuft. Die Seite Dienste zeigt seinen Zustand und die letzten Neustarts.")

	return Event{
		Category: Services, Site: e.Site, Window: longWindow, Fragment: fragment,
		Key: "service-down:" + servermon.Key(e.Service, e.Site) + ":" + unix(e.At),
		Mail: mail.OpsEvent{
			Template: "service-down", Subject: "Service down: " + subjectName(e.Service, e.Site), Lines: lines,
			German: mail.OpsText{Subject: "Dienst ausgefallen: " + germanSubjectName(e.Service, e.Site), Lines: de},
		},
	}
}

func crashingEvent(e store.ServiceEvent, n int) Event {
	name, where, fragment := describe(e.Service, e.Site)
	name2, where2 := describeGerman(e.Service, e.Site)
	minutes := strconv.Itoa(int(servermon.CrashWindow / time.Minute))

	return Event{
		Category: Services, Site: e.Site, Window: crashWindow, Fragment: fragment,
		Key: "service-crashing:" + servermon.Key(e.Service, e.Site),
		Mail: mail.OpsEvent{Template: "service-crashing", Subject: "Service keeps crashing: " + subjectName(e.Service, e.Site), Lines: []string{
			name + where + " crashed " + strconv.Itoa(n) + " times in the last " + minutes + " minutes. systemd restarted it each time.",
			"",
			"Hostashi sends another email when it runs steadily again.",
		}, German: mail.OpsText{Subject: "Dienst stürzt immer wieder ab: " + germanSubjectName(e.Service, e.Site), Lines: []string{
			name2 + where2 + " ist in den letzten " + minutes + " Minuten " + strconv.Itoa(n) + "-mal abgestürzt. systemd hat den Dienst jedes Mal neu gestartet.",
			"",
			"Hostashi schickt eine weitere E-Mail, sobald der Dienst wieder stabil läuft.",
		}}},
	}
}

func backEvent(o *outage, at time.Time) Event {
	name, where, fragment := describe(o.id, o.site)
	name2, where2 := describeGerman(o.id, o.site)

	return Event{
		Category: Services, Site: o.site, Window: longWindow, Fragment: fragment,
		Key: "service-back:" + servermon.Key(o.id, o.site) + ":" + unix(o.since),
		Mail: mail.OpsEvent{Template: "service-back", Subject: "Service running again: " + subjectName(o.id, o.site), Lines: []string{
			name + where + " runs again since " + when(at) + ". The problem started on " + when(o.since) + ".",
		}, German: mail.OpsText{Subject: "Dienst läuft wieder: " + germanSubjectName(o.id, o.site), Lines: []string{
			name2 + where2 + " läuft seit " + wann(at) + " wieder. Das Problem begann am " + wann(o.since) + ".",
		}}},
	}
}

// describe returns the service's name, which starts the sentence after
// "Hello <name>," in lower case like the other emails, " of the site x"
// for a per-site service, and the panel page to link.
func describe(id, site string) (name, where, fragment string) {
	name = serviceNames[id]
	if name == "" {
		name = id
	}

	if site == "" {
		return name, "", "services"
	}

	return name, " of the site " + site, "sites/" + site
}

// describeGerman is describe for German emails, without the page.
func describeGerman(id, site string) (name, where string) {
	name = germanServiceNames[id]
	if name == "" {
		name = id
	}

	if site == "" {
		return name, ""
	}

	return name, " der Site " + site
}

func germanSubjectName(id, site string) string {
	name, _ := describeGerman(id, site)
	if site != "" {
		return name + " von " + site
	}

	return name
}

func subjectName(id, site string) string {
	name, _, _ := describe(id, site)
	if site != "" {
		return name + " of " + site
	}

	return name
}

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
