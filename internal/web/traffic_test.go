// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
)

func TestTrafficLog(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}
	n.SiteLogDir = "/var/log/nginx/sites"

	f, err := n.Render(Desired{Sites: []Site{
		{User: "shop", Domains: []string{"shop.example.com"}, Root: "/srv/shop", Cert: "/c/shop.pem", Key: "/c/shop.key"},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// The HTTP and the HTTPS server block both count (ADR-0063).
	conf := string(f.Content["sites/shop.conf"])
	if got := strings.Count(conf, "access_log /var/log/nginx/sites/shop/traffic.log hostashi_traffic buffer=32k flush=5s;"); got != 2 {
		t.Errorf("traffic log in %d server blocks:\n%s", got, conf)
	}

	if m := string(f.Content["nginx.conf"]); !strings.Contains(m, "log_format hostashi_traffic '$msec $status $request_length $bytes_sent';") {
		t.Errorf("nginx.conf lacks the traffic format:\n%s", m)
	}
}
