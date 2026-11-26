// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/updates"
)

func TestUpdate(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	repo := "https://get.hostashi.com/apt"
	fresh := now.Add(-5 * time.Hour)

	type want struct{ id, status, summary string }

	for _, c := range []struct {
		name string
		st   updates.Status
		err  error
		want []want
	}{
		{"not from the package", updates.Status{}, nil, nil},
		{"unreadable", updates.Status{}, errors.New("boom"), []want{{"hostashi.update", agentapi.DoctorWarn, "cannot read"}}},
		{"no source", updates.Status{Installed: "0.1.0"}, nil, []want{{"hostashi.apt", agentapi.DoctorWarn, "not configured"}}},
		{"no list", updates.Status{Installed: "0.1.0", Repository: repo}, nil, []want{{"hostashi.apt", agentapi.DoctorWarn, "no package list"}}},
		{
			"newest",
			updates.Status{Installed: "0.2.0", Available: "0.2.0", Repository: repo, ListedAt: fresh},
			nil,
			[]want{{"hostashi.apt", agentapi.DoctorOK, "fetched"}, {"hostashi.update", agentapi.DoctorOK, "0.2.0 is the newest"}},
		},
		{
			"update",
			updates.Status{Installed: "0.1.0", Available: "0.2.0", Update: true, Repository: repo, ListedAt: fresh},
			nil,
			[]want{{"hostashi.apt", agentapi.DoctorOK, "fetched"}, {"hostashi.update", agentapi.DoctorWarn, "hostashi 0.2.0 is available (installed: 0.1.0)"}},
		},
		{
			"update, automatic",
			updates.Status{Installed: "0.1.0", Available: "0.2.0", Update: true, Auto: true, Repository: repo, ListedAt: fresh},
			nil,
			[]want{{"hostashi.apt", agentapi.DoctorOK, "fetched"}, {"hostashi.update", agentapi.DoctorOK, "automatic updates install it"}},
		},
		{
			"stale list",
			updates.Status{Installed: "0.2.0", Available: "0.2.0", Repository: repo, ListedAt: now.Add(-5 * 24 * time.Hour)},
			nil,
			[]want{{"hostashi.apt", agentapi.DoctorWarn, "5 days ago"}, {"hostashi.update", agentapi.DoctorOK, "newest"}},
		},
	} {
		got := Update(c.st, c.err, now)
		if len(got) != len(c.want) {
			t.Errorf("%s: %d checks, want %d: %+v", c.name, len(got), len(c.want), got)

			continue
		}

		for i, w := range c.want {
			g := got[i]
			if g.ID != w.id || g.Status != w.status || !strings.Contains(g.Summary, w.summary) {
				t.Errorf("%s: check %d = %+v, want %+v", c.name, i, g, w)
			}

			if g.Status != agentapi.DoctorOK && g.Hint == "" {
				t.Errorf("%s: check %d has no hint", c.name, i)
			}
		}
	}
}

func TestRuntimes(t *testing.T) {
	repo := "https://get.hostashi.com/apt"

	for _, c := range []struct {
		name   string
		st     updates.Status
		status string
		text   string
	}{
		{"not from the package", updates.Status{}, "", ""},
		{"missing", updates.Status{Installed: "0.14.0", Repository: repo}, agentapi.DoctorWarn, "not installed"},
		{"not automatic", updates.Status{Installed: "0.14.0", Repository: repo, Runtimes: "20261127050000"}, agentapi.DoctorWarn, "does not take"},
		{"fine", updates.Status{Installed: "0.14.0", Repository: repo, Runtimes: "20261127050000", RuntimesAuto: true}, agentapi.DoctorOK, "20261127050000"},
	} {
		got := Runtimes(c.st)
		if c.status == "" {
			if len(got) != 0 {
				t.Errorf("%s: %+v", c.name, got)
			}

			continue
		}

		if len(got) != 1 || got[0].ID != "hostashi.runtimes" || got[0].Status != c.status || !strings.Contains(got[0].Summary, c.text) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}
