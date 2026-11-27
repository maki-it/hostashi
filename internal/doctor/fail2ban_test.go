// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestFail2banJails(t *testing.T) {
	all := agentapi.Fail2banStatus{Jails: []agentapi.Fail2banJail{{Name: "sshd", Running: true}, {Name: "recidive", Running: true}}}
	if c := Fail2banJails(all, nil); c.Status != agentapi.DoctorOK {
		t.Errorf("all running: %+v", c)
	}

	one := agentapi.Fail2banStatus{Jails: []agentapi.Fail2banJail{{Name: "sshd", Running: true}, {Name: "hostashi-site-auth"}}}
	if c := Fail2banJails(one, nil); c.Status != agentapi.DoctorWarn || !strings.Contains(c.Summary, "hostashi-site-auth") {
		t.Errorf("one missing: %+v", c)
	}

	if c := Fail2banJails(agentapi.Fail2banStatus{}, errors.New("no answer")); c.Status != agentapi.DoctorWarn {
		t.Errorf("error: %+v", c)
	}
}
