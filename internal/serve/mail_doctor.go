// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

// MailHistory is the part of the panel store the email check reads.
type MailHistory interface {
	FailedMailSince(ctx context.Context, since time.Time) ([]store.OutboxMail, error)
}

// mailCheck reports on outgoing email for the Health page and the
// overview (ADR-0044): nothing while email is off, a warning when messages
// failed in the last 24 hours. It does not connect to the relay; the test
// email on the Email page does that.
func mailCheck(relay MailRelay, hist MailHistory) PanelChecks {
	const id = "panel.mail"

	return func(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error) {
		c, err := relay.Load(ctx)
		if errors.Is(err, mail.ErrNotConfigured) {
			return nil, nil
		}

		if err != nil {
			return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorFail, id, "mail.settings.error", "panellog.hint", nil)}, fmt.Errorf("mail settings: %w", err)
		}

		relayAddr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))

		failed, err := hist.FailedMailSince(ctx, now.Add(-24*time.Hour))
		if err != nil {
			return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorFail, id, "mail.outbox.error", "panellog.hint", nil)}, fmt.Errorf("mail outbox: %w", err)
		}

		if len(failed) == 0 {
			return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorOK, id, "mail.ok", "", doctor.P{"relay": relayAddr})}, nil
		}

		key, params := "mail.failed", doctor.P{"count": strconv.Itoa(len(failed))}
		if len(failed) == 1 {
			key = "mail.failed.one"
		}

		if last := failed[0].LastError; last != "" {
			key, params["error"] = key+".last", last
		}

		return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorWarn, id, key, "mail.failed.hint", params)}, nil
	}
}

// allPanelChecks runs each set of panel checks in turn, also after one
// failed; nil for none.
func allPanelChecks(list []PanelChecks) PanelChecks {
	if len(list) == 0 {
		return nil
	}

	return func(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error) {
		var (
			out  []agentapi.DoctorCheck
			errs error
		)

		for _, f := range list {
			more, err := f(ctx, now)
			errs = errors.Join(errs, err)

			out = append(out, more...)
		}

		return out, errs
	}
}

// withDatabaseChecks adds the database size checks (ADR-0057) to the
// report the warning emails read, so a site whose databases near the
// threshold gets the same email as one whose disk fills up. A failed
// measurement leaves the agent's report as it is.
func withDatabaseChecks(run Doctor, db PanelChecks) Doctor {
	if run == nil {
		return nil
	}

	return func(ctx context.Context) (agentapi.DoctorReport, error) {
		rep, err := run(ctx)
		if err != nil {
			return rep, err
		}

		if more, err := db(ctx, time.Now()); err == nil {
			rep.Checks = append(rep.Checks, more...)
		}

		return rep, nil
	}
}
