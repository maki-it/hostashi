// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify sends the operations emails of ADR-0044 to the accounts
// that switched them on and may see what they are about.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

// Category is one switch on the page Account, Notifications.
type Category string

// The operations categories of ADR-0044.
const (
	Backups      Category = "backups"
	Certificates Category = "certificates"
	Doctor       Category = "doctor"
	PHP          Category = "php"
	Disk         Category = "disk"
)

// Categories lists every category in the order the page shows them.
var Categories = []Category{Backups, Certificates, Doctor, PHP, Disk}

// titles name a category in the footer of an email.
var titles = map[Category]string{
	Backups: "backups", Certificates: "certificates", Doctor: "server checks", PHP: "PHP versions", Disk: "disk space",
}

// Known reports whether c is a category.
func Known(c Category) bool {
	_, ok := titles[c]

	return ok
}

// Visible reports whether an account with the role may get emails of the
// category at all (ADR-0035): backups and server checks are server
// settings, the others are about sites.
func Visible(role string, c Category) bool {
	switch c {
	case Backups, Doctor:
		return authz.Role(role) == authz.RoleAdmin
	case Certificates, PHP, Disk:
		return true
	default:
		return false
	}
}

// Enabled applies the default of the role to a switch the account never
// set: on for admins, off for everyone else.
func Enabled(role string, prefs map[string]bool, c Category) bool {
	if !Visible(role, c) {
		return false
	}

	if on, ok := prefs[string(c)]; ok {
		return on
	}

	return authz.Role(role) == authz.RoleAdmin
}

// Event is one problem to report.
type Event struct {
	Category Category
	// Site limits the recipients to accounts that may see it; empty means
	// a server-wide event, for admins only.
	Site string
	// Key identifies the event for deduplication, for example
	// "backup-failed:1700000000". The recipient is added to it.
	Key string
	// Window is how long the same key is not sent again; zero is 24 hours.
	Window time.Duration
	// Fragment is the panel page, for example "backups" or "sites/shop".
	Fragment string
	Mail     mail.OpsEvent
}

// Store is the part of the panel store the notifier reads.
type Store interface {
	NotificationRecipients(ctx context.Context) ([]store.NotifyRecipient, error)
	HasSiteGrant(ctx context.Context, userID int64, site string) (bool, error)
}

// Outbox queues the emails.
type Outbox interface {
	Enqueue(ctx context.Context, m mail.Message, e mail.Envelope) (bool, error)
}

// Notifier fans an event out to its recipients.
type Notifier struct {
	St     Store
	Outbox Outbox
	// Link builds a panel link from a fragment; empty when the panel has
	// no public URL. Host is the panel's host name for the footer.
	Link func(fragment string) string
	Host string
	Log  *slog.Logger
}

// Notify queues the event for every account that switched its category on
// and may see the site, and returns how many emails were queued. Without a
// relay it does nothing.
func (n *Notifier) Notify(ctx context.Context, ev Event) (int, error) {
	if !Known(ev.Category) || ev.Key == "" {
		return 0, errors.New("notify: event needs a category and a key")
	}

	list, err := n.St.NotificationRecipients(ctx)
	if err != nil {
		return 0, err
	}

	link := ""
	if n.Link != nil && ev.Fragment != "" {
		link = n.Link(ev.Fragment)
	}

	queued := 0

	for _, r := range list {
		ok, err := n.reaches(ctx, r, ev)
		if err != nil {
			return queued, err
		}

		if !ok {
			continue
		}

		msg, env := mail.OperationsMail(r.Email, r.Username, titles[ev.Category], ev.Mail, link, n.Host)
		env.DedupKey = ev.Key + ":" + strconv.FormatInt(r.UserID, 10)
		env.DedupWindow = ev.Window

		sent, err := n.Outbox.Enqueue(ctx, msg, env)
		if errors.Is(err, mail.ErrNotConfigured) {
			return queued, nil
		}

		if err != nil {
			n.Log.WarnContext(ctx, "queue notification", "template", ev.Mail.Template, "user", r.Username, "err", mail.Summary(err))

			continue
		}

		if sent {
			queued++
		}
	}

	return queued, nil
}

func (n *Notifier) reaches(ctx context.Context, r store.NotifyRecipient, ev Event) (bool, error) {
	if !Enabled(r.Role, r.Prefs, ev.Category) {
		return false, nil
	}

	switch authz.Role(r.Role) {
	case authz.RoleAdmin:
		return true, nil
	case authz.RoleManager:
		return ev.Site != "", nil
	case authz.RoleUser:
		if ev.Site == "" {
			return false, nil
		}

		return n.St.HasSiteGrant(ctx, r.UserID, ev.Site)
	default:
		return false, nil
	}
}
