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
			return []agentapi.DoctorCheck{{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read the email settings", Hint: "Check the panel's log (journalctl -u hostashi-panel)."}}, fmt.Errorf("mail settings: %w", err)
		}

		relayAddr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))

		failed, err := hist.FailedMailSince(ctx, now.Add(-24*time.Hour))
		if err != nil {
			return []agentapi.DoctorCheck{{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read the outbox", Hint: "Check the panel's log (journalctl -u hostashi-panel)."}}, fmt.Errorf("mail outbox: %w", err)
		}

		if len(failed) == 0 {
			return []agentapi.DoctorCheck{{ID: id, Status: agentapi.DoctorOK, Summary: "relay " + relayAddr + ", no failed emails in the last 24 hours"}}, nil
		}

		summary := fmt.Sprintf("%d emails could not be sent in the last 24 hours", len(failed))
		if len(failed) == 1 {
			summary = "1 email could not be sent in the last 24 hours"
		}

		if last := failed[0].LastError; last != "" {
			summary += " (last: " + last + ")"
		}

		return []agentapi.DoctorCheck{{
			ID: id, Status: agentapi.DoctorWarn, Summary: summary,
			Hint: "Check the settings on the Email page and send a test email. Links in invitations can still be copied on the Users page.",
		}}, nil
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
