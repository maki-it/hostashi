// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestNode(t *testing.T) {
	r22, _ := agentapi.NodeReleaseOf("22")
	r24, _ := agentapi.NodeReleaseOf("24")
	end22, _ := time.Parse(time.DateOnly, r22.End)

	if got := Node(nil, nil, end22); len(got) != 0 {
		t.Fatalf("nothing installed: %+v", got)
	}

	cur := []agentapi.NodeInstalled{{Major: "22", Version: r22.Version}, {Major: "24", Version: r24.Version}}

	// Long before the end: all fine, also with sites.
	got := Node(cur, map[string][]string{"22": {"shop"}}, end22.AddDate(-1, 0, 0))
	if len(got) != 1 || got[0].Status != agentapi.DoctorOK {
		t.Fatalf("current: %+v", got)
	}

	// 100 days before the end, with a site: warn; without one: fine.
	soon := end22.AddDate(0, 0, -100)

	got = Node(cur, map[string][]string{"22": {"blog", "shop"}}, soon)
	if len(got) != 1 || got[0].Status != agentapi.DoctorWarn || !strings.Contains(got[0].Summary, "blog, shop") {
		t.Fatalf("soon: %+v", got)
	}

	if got = Node(cur, nil, soon); got[0].Status != agentapi.DoctorOK {
		t.Fatalf("soon without sites: %+v", got)
	}

	// After the end.
	got = Node(cur, map[string][]string{"22": {"shop"}}, end22.AddDate(0, 0, 2))
	if len(got) != 1 || !strings.Contains(got[0].Summary, "no fixes since") {
		t.Fatalf("end: %+v", got)
	}

	// An update that did not finish.
	old := []agentapi.NodeInstalled{{Major: "24", Version: "24.0.0"}}

	got = Node(old, nil, end22.AddDate(-1, 0, 0))
	if len(got) != 1 || got[0].Status != agentapi.DoctorWarn || !strings.Contains(got[0].Summary, "did not finish") {
		t.Fatalf("outdated: %+v", got)
	}
}
