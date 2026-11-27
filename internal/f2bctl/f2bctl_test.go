// SPDX-License-Identifier: AGPL-3.0-or-later

package f2bctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
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

	st, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{})
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

	if st.Jails[1].Running {
		t.Errorf("missing panel jail: %+v", st.Jails[1])
	}

	if !st.Jails[2].Running || st.Total != 2 || len(st.Blocked) != 2 || st.Truncated {
		t.Errorf("site-auth: %+v %+v", st.Jails[2], st)
	}

	// The next pages and filters come from the same reading.
	n := len(f.calls)

	if st, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{Jail: "recidive"}); err != nil || st.Total != 0 {
		t.Errorf("recidive: %+v %v", st, err)
	}

	if len(f.calls) != n {
		t.Errorf("read fail2ban again within the cache time: %v", f.calls[n:])
	}

	for _, c := range f.calls {
		if c[0] != "ping" && !slices.Contains(agentapi.Fail2banJails, c[1]) {
			t.Errorf("call with a foreign jail: %v", c)
		}
	}
}

func TestStatusUnavailable(t *testing.T) {
	f := &fake{err: map[string]error{"ping": errors.New("exited 255")}}

	if _, err := (&Controller{Run: f.run}).Status(context.Background(), agentapi.Fail2banStatusRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v", err)
	}

	if len(f.calls) != 1 {
		t.Errorf("calls %v", f.calls)
	}

	f.calls = nil

	if _, err := (&Controller{Run: f.run}).Status(context.Background(), agentapi.Fail2banStatusRequest{Jail: "apache"}); !errors.Is(err, agentapi.ErrFail2banFilter) || len(f.calls) != 0 {
		t.Errorf("foreign jail: %v %v", err, f.calls)
	}
}

func TestStatusCacheAndTruncated(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fake{
		out: map[string]string{
			"ping": "pong",
			"status sshd": sshdStatusOut, "status hostashi-panel": sshdStatusOut, "status hostashi-site-auth": statusOut, "status recidive": sshdStatusOut,
			"get hostashi-site-auth banip --with-time": bansOut,
		},
		err: map[string]error{"get hostashi-site-auth banip --with-time": ErrTruncated},
	}
	c := &Controller{Run: f.run, Location: time.UTC, Now: func() time.Time { return now }}

	st, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{})
	if err != nil || !st.Truncated || st.Total != 2 {
		t.Fatalf("truncated: %+v %v", st, err)
	}

	calls := len(f.calls)
	now = now.Add(cacheFor - time.Second)

	if _, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{Offset: 1}); err != nil || len(f.calls) != calls {
		t.Errorf("cache not used: %v", err)
	}

	now = now.Add(2 * time.Second)

	if _, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{}); err != nil || len(f.calls) == calls {
		t.Errorf("cache too old but used: %v", err)
	}

	// An unblock drops the cached reading.
	calls = len(f.calls)
	f.out["set sshd unbanip 203.0.113.7"] = "0"
	f.out["set hostashi-panel unbanip 203.0.113.7"] = "0"
	f.out["set hostashi-site-auth unbanip 203.0.113.7"] = "1"
	f.out["set recidive unbanip 203.0.113.7"] = "0"

	if _, err := c.Unban(context.Background(), agentapi.Fail2banUnbanRequest{Address: "203.0.113.7"}); err != nil {
		t.Fatal(err)
	}

	calls = len(f.calls)

	if _, err := c.Status(context.Background(), agentapi.Fail2banStatusRequest{}); err != nil || len(f.calls) == calls {
		t.Errorf("cache kept after unban: %v", err)
	}
}

func TestPage(t *testing.T) {
	jails := []agentapi.Fail2banJail{{Name: "sshd", Running: true}}
	bans := map[string][]agentapi.Fail2banBan{
		"hostashi-site-auth": {
			{Address: "203.0.113.7", Since: 100, Until: 700},
			{Address: "203.0.113.70", Since: 300, Until: 900},
			{Address: "198.51.100.1", Since: 200, Until: 800},
		},
		"recidive": {
			{Address: "203.0.113.7", Since: 50},
			{Address: "2001:db8::1", Since: 400, Until: 1000},
		},
		"hostashi-panel": {{Address: "203.0.113.7", Since: 150, Until: 2000}},
	}

	addrs := func(st agentapi.Fail2banStatus) []string {
		out := []string{}
		for _, b := range st.Blocked {
			out = append(out, b.Address)
		}

		return out
	}

	all := Page(jails, bans, agentapi.Fail2banStatusRequest{})
	if want := []string{"2001:db8::1", "203.0.113.70", "198.51.100.1", "203.0.113.7"}; !slices.Equal(addrs(all), want) || all.Total != 4 {
		t.Fatalf("all: %v %d", addrs(all), all.Total)
	}

	// One row per address, every jail, the first start and no end when
	// one ban has none.
	b := all.Blocked[3]
	if !slices.Equal(b.Jails, []string{"hostashi-panel", "hostashi-site-auth", "recidive"}) || b.Since != 50 || b.Until != 0 {
		t.Errorf("merged: %+v", b)
	}

	for _, tc := range []struct {
		req  agentapi.Fail2banStatusRequest
		want []string
	}{
		{agentapi.Fail2banStatusRequest{Jail: "recidive"}, []string{"2001:db8::1", "203.0.113.7"}},
		{agentapi.Fail2banStatusRequest{Jail: "sshd"}, []string{}},
		{agentapi.Fail2banStatusRequest{Address: "203.0.113.7"}, []string{"203.0.113.70", "203.0.113.7"}},
		{agentapi.Fail2banStatusRequest{Address: "203.0.113.0/24"}, []string{"203.0.113.70", "203.0.113.7"}},
		{agentapi.Fail2banStatusRequest{Address: "203.0.113.7/32"}, []string{"203.0.113.7"}},
		{agentapi.Fail2banStatusRequest{Address: "2001:DB8::/32"}, []string{"2001:db8::1"}},
		{agentapi.Fail2banStatusRequest{Address: "2001:0db8:0::1"}, []string{"2001:db8::1"}},
		{agentapi.Fail2banStatusRequest{Address: "db8"}, []string{"2001:db8::1"}},
		{agentapi.Fail2banStatusRequest{Address: "198.51", Jail: "recidive"}, []string{}},
		{agentapi.Fail2banStatusRequest{Offset: 1, Limit: 2}, []string{"203.0.113.70", "198.51.100.1"}},
		{agentapi.Fail2banStatusRequest{Offset: 10}, []string{}},
	} {
		st := Page(jails, bans, tc.req)
		if got := addrs(st); !slices.Equal(got, tc.want) {
			t.Errorf("%+v: %v, want %v", tc.req, got, tc.want)
		}
	}

	if st := Page(jails, bans, agentapi.Fail2banStatusRequest{Offset: 1, Limit: 2}); st.Total != 4 {
		t.Errorf("total of a page: %d", st.Total)
	}

	if st := Page(nil, nil, agentapi.Fail2banStatusRequest{}); st.Jails == nil || st.Blocked == nil || st.Total != 0 {
		t.Errorf("empty: %+v", st)
	}
}

// One page with the longest addresses, blocked in every jail, fits in one
// agent frame, also when fail2ban blocks a hundred thousand addresses.
func TestPageFitsFrame(t *testing.T) {
	bans := map[string][]agentapi.Fail2banBan{}

	for i := range 100_000 {
		a := fmt.Sprintf("2001:db8:ffff:ffff:ffff:ffff:%04x:%04x", i>>16, i&0xffff)
		for _, j := range agentapi.Fail2banJails {
			bans[j] = append(bans[j], agentapi.Fail2banBan{Address: a, Since: 1_800_000_000 + int64(i), Until: 1_900_000_000})
		}
	}

	jails := make([]agentapi.Fail2banJail, 0, len(agentapi.Fail2banJails))
	for _, j := range agentapi.Fail2banJails {
		jails = append(jails, agentapi.Fail2banJail{Name: j, Running: true, CurrentlyFailed: 1 << 30, TotalFailed: 1 << 30, CurrentlyBanned: 1 << 30, TotalBanned: 1 << 30})
	}

	st := Page(jails, bans, agentapi.Fail2banStatusRequest{Limit: agentapi.Fail2banPageMax})
	st.Truncated = true

	body, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}

	// The agent's answer wraps the result in a few more bytes.
	if len(body) > agentrpc.MaxFrame/2 {
		t.Errorf("page is %d bytes, frame limit %d", len(body), agentrpc.MaxFrame)
	}
}

func TestLimitedWriter(t *testing.T) {
	var buf bytes.Buffer

	w := &limitedWriter{buf: &buf, max: 10}
	_, _ = w.Write([]byte("12345"))

	if w.cut {
		t.Error("cut below the limit")
	}

	_, _ = w.Write([]byte("678901"))

	if !w.cut || buf.Len() != 10 {
		t.Errorf("cut %v len %d", w.cut, buf.Len())
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
