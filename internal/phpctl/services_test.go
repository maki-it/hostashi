// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestServicesRefuseOddUnitsAndActions(t *testing.T) {
	var s Services

	for _, u := range []string{
		"", "sshd.service", "systemd-journald.service", "nginx", "nginx.service --now", "-H host",
		"hostashi-valkey@.service", "hostashi-valkey@Blog.service", "hostashi-valkey@../x.service",
		"hostashi-php-8.4@.service", "hostashi-backup.service", "hostashi-files@blog.service",
	} {
		if err := s.Action(t.Context(), u, agentapi.ServiceRestart); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("unit %q: %v", u, err)
		}

		if _, err := s.Show(t.Context(), []string{"nginx.service", u}); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("show %q: %v", u, err)
		}
	}

	for _, a := range []string{"", "enable", "disable", "mask", "kill", "Start", "reload-or-restart"} {
		if err := s.Action(t.Context(), "nginx.service", a); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("action %q: %v", a, err)
		}
	}

	many := make([]string, MaxShowUnits+1)
	for i := range many {
		many[i] = "nginx.service"
	}

	if _, err := s.Show(t.Context(), many); err == nil {
		t.Error("too many units accepted")
	}

	if st, err := s.Show(t.Context(), nil); err != nil || len(st) != 0 {
		t.Errorf("empty show: %v %v", st, err)
	}
}

func TestServiceCatalogUnitsAllowed(t *testing.T) {
	for _, svc := range agentapi.Services {
		if svc.Unit != "" && !serviceUnitAllowed(svc.Unit) {
			t.Errorf("catalog unit %s refused", svc.Unit)
		}
	}

	for _, u := range []string{"hostashi-php-8.4@blog.service", "hostashi-valkey@shop.service"} {
		if !serviceUnitAllowed(u) {
			t.Errorf("%s refused", u)
		}
	}
}

func TestParseShow(t *testing.T) {
	out := `Id=nginx.service
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
Result=success
NRestarts=3
MainPID=812
MemoryCurrent=12582912
ActiveEnterTimestamp=@1763737445
ExecMainStartTimestamp=@1763737444

Id=fail2ban.service
LoadState=not-found
ActiveState=inactive
SubState=dead
UnitFileState=
Result=success
NRestarts=0
MainPID=0
MemoryCurrent=[not set]
ActiveEnterTimestamp=
ExecMainStartTimestamp=

Id=mariadb.service
LoadState=loaded
ActiveState=failed
SubState=failed
UnitFileState=enabled
Result=exit-code
NRestarts=x
MainPID=-1
MemoryCurrent=18446744073709551615
ActiveEnterTimestamp=n/a
ExecMainStartTimestamp=Fri 2026-11-21 15:04:04 CET
Evil=1
`

	st, err := ParseShow(out, []string{"nginx.service", "fail2ban.service", "mariadb.service"})
	if err != nil {
		t.Fatal(err)
	}

	want0 := agentapi.ServiceState{
		Unit: "nginx.service", Installed: true, Load: "loaded", Active: "active", Sub: "running", UnitFile: "enabled",
		Result: "success", NRestarts: 3, MainPID: 812, MemoryBytes: 12582912, MemoryKnown: true,
		ActiveSince: time.Unix(1763737445, 0).UTC(), StartedAt: time.Unix(1763737444, 0).UTC(),
	}

	if st[0] != want0 {
		t.Errorf("nginx: %+v", st[0])
	}

	if st[1] != (agentapi.ServiceState{Unit: "fail2ban.service"}) {
		t.Errorf("missing unit: %+v", st[1])
	}

	want2 := agentapi.ServiceState{Unit: "mariadb.service", Installed: true, Load: "loaded", Active: "failed", Sub: "failed", UnitFile: "enabled", Result: "exit-code"}
	if st[2] != want2 {
		t.Errorf("odd values: %+v", st[2])
	}

	// Odd values in enum fields are dropped.
	st, err = ParseShow("Id=nginx.service\nLoadState=loaded\nActiveState=hacked\nSubState=Run ning\nUnitFileState=weird\nResult=a=b\n", []string{"nginx.service"})
	if err != nil || st[0].Active != "" || st[0].Sub != "" || st[0].UnitFile != "" || st[0].Result != "" || !st[0].Installed {
		t.Errorf("%+v %v", st, err)
	}

	if _, err := ParseShow(out, []string{"nginx.service"}); err == nil {
		t.Error("block count mismatch accepted")
	}

	if _, err := ParseShow("Id=ssh.service\nLoadState=loaded\n", []string{"nginx.service"}); err == nil {
		t.Error("block of another unit accepted")
	}
}
