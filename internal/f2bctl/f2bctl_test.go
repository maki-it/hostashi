// SPDX-License-Identifier: AGPL-3.0-or-later

package f2bctl

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Captured from fail2ban 1.0.2 and the same format in 1.1.0 (Debian 13);
// the "Journal matches" line is what a systemd backend prints.
const statusOut = `Status for the jail: hostashi-site-auth
|- Filter
|  |- Currently failed:	3
|  |- Total failed:	13
|  ` + "`" + `- File list:	/var/log/nginx/hostashi/error.log
` + "`" + `- Actions
   |- Currently banned:	2
   |- Total banned:	4
   ` + "`" + `- Banned IP list:	203.0.113.7 2001:db8::1
`

const sshdStatusOut = `Status for the jail: sshd
|- Filter
|  |- Currently failed:	0
|  |- Total failed:	0
|  ` + "`" + `- Journal matches:	_SYSTEMD_UNIT=ssh.service + _COMM=sshd + _COMM=sshd-session
` + "`" + `- Actions
   |- Currently banned:	0
   |- Total banned:	0
   ` + "`" + `- Banned IP list:	
`

const bansOut = "2001:db8::1 \t2026-11-26 23:07:06 + 600 = 2026-11-26 23:17:06\n203.0.113.7 \t2026-11-26 23:07:05 + 3600 = 2026-11-27 00:07:05\n"

func TestParseStatus(t *testing.T) {
	j, err := ParseStatus("hostashi-site-auth", statusOut)
	if err != nil {
		t.Fatal(err)
	}

	if !j.Running || j.CurrentlyFailed != 3 || j.TotalFailed != 13 || j.CurrentlyBanned != 2 || j.TotalBanned != 4 {
		t.Errorf("got %+v", j)
	}

	if j, err := ParseStatus("sshd", sshdStatusOut); err != nil || j.TotalFailed != 0 || !j.Running {
		t.Errorf("sshd: %+v %v", j, err)
	}

	for _, bad := range []string{"", "Sorry", strings.Replace(statusOut, "Total failed:\t13", "Total failed:\tlots", 1), strings.Replace(statusOut, "Total banned:\t4", "Total banned:\t-1", 1)} {
		if _, err := ParseStatus("x", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseBans(t *testing.T) {
	loc := time.FixedZone("CET", 3600)

	bans, err := ParseBans(bansOut, loc)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 11, 26, 23, 7, 5, 0, loc).Unix()
	want := []agentapi.Fail2banBan{
		{Address: "203.0.113.7", Since: start, Until: start + 3600},
		{Address: "2001:db8::1", Since: start + 1, Until: start + 601},
	}

	if !slices.Equal(bans, want) {
		t.Errorf("got %+v, want %+v", bans, want)
	}

	if b, err := ParseBans("", loc); err != nil || len(b) != 0 || b == nil {
		t.Errorf("empty: %v %v", b, err)
	}

	forever, err := ParseBans("203.0.113.7 \t2026-11-26 23:07:05 + -1 = 9999-12-31 23:59:59\n", loc)
	if err != nil || len(forever) != 1 || forever[0].Until != 0 {
		t.Errorf("ban without end: %+v %v", forever, err)
	}

	for _, bad := range []string{
		"203.0.113.7", "<script> \t2026-11-26 23:07:05 + 600 = x", "203.0.113.7 \tyesterday + 600 = x",
		"203.0.113.7 \t2026-11-26 23:07:05 + ten = x", "203.0.113.7 \t2026-11-26 23:07:05",
	} {
		if _, err := ParseBans(bad, loc); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

type fake struct {
	calls [][]string
	out   map[string]string
	err   map[string]error
}

func (f *fake) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	k := strings.Join(args, " ")

	return f.out[k], f.err[k]
}

func TestStatus(t *testing.T) {
	f := &fake{
		out: map[string]string{
			"ping":                      "Server replied: pong\n",
			"status hostashi-site-auth": statusOut,
			"get hostashi-site-auth banip --with-time": bansOut,
			"status sshd":                    sshdStatusOut,
			"status recidive":                sshdStatusOut,
			"get sshd banip --with-time":     "",
			"get recidive banip --with-time": "\n",
		},
		err: map[string]error{"status hostashi-panel": errors.New("f2bctl: fail2ban-client status exited 255: Sorry but the jail 'hostashi-panel' does not exist")},
	}
	c := &Controller{Run: f.run, Location: time.UTC}

	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, j := range st.Jails {
		names = append(names, j.Name)
	}

	if !slices.Equal(names, agentapi.Fail2banJails) {
		t.Errorf("jails %v", names)
	}

	if st.Jails[1].Running || st.Jails[1].Bans == nil {
		t.Errorf("missing panel jail: %+v", st.Jails[1])
	}

	if !st.Jails[2].Running || len(st.Jails[2].Bans) != 2 {
		t.Errorf("site-auth: %+v", st.Jails[2])
	}

	for _, c := range f.calls {
		if c[0] != "ping" && !slices.Contains(agentapi.Fail2banJails, c[1]) {
			t.Errorf("call with a foreign jail: %v", c)
		}
	}
}

func TestStatusUnavailable(t *testing.T) {
	f := &fake{err: map[string]error{"ping": errors.New("exited 255")}}

	if _, err := (&Controller{Run: f.run}).Status(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

func TestUnban(t *testing.T) {
	f := &fake{
		out: map[string]string{
			"ping":                         "pong",
			"set sshd unbanip 203.0.113.7": "0\n",
			"set hostashi-site-auth unbanip 203.0.113.7": "1\n",
			"set recidive unbanip 203.0.113.7":           "1\n",
		},
		err: map[string]error{"set hostashi-panel unbanip 203.0.113.7": errors.New("Sorry but the jail 'hostashi-panel' does not exist")},
	}
	c := &Controller{Run: f.run}

	resp, err := c.Unban(context.Background(), agentapi.Fail2banUnbanRequest{Address: "203.0.113.7"})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(resp.Jails, []string{"hostashi-site-auth", "recidive"}) {
		t.Errorf("jails %v", resp.Jails)
	}

	f.calls = nil

	if _, err := c.Unban(context.Background(), agentapi.Fail2banUnbanRequest{Address: "--report-absent"}); !errors.Is(err, agentapi.ErrFail2banAddress) {
		t.Errorf("bad address: %v", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("client ran for a bad address: %v", f.calls)
	}

	f.out["set sshd unbanip 203.0.113.7"] = "OK"

	if _, err := c.Unban(context.Background(), agentapi.Fail2banUnbanRequest{Address: "203.0.113.7"}); err == nil {
		t.Error("odd answer accepted")
	}
}
