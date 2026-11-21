// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "testing"

func TestDBRemoteJobRequest(t *testing.T) {
	ok := DBRemoteJobRequest{
		Kind: "export", Site: "shop", Database: "shop_db", Path: "backups/shop.sql.gz",
		Host: "db.example.net", Port: 3306, Addrs: []string{"10.0.0.5", "2001:db8::5"}, TLS: "verify",
		User: "hostashi_job_0123456789abcdef", Password: "Abcdefghijklmnopqrstuvwxyz012345",
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	ca := ok
	ca.TLS, ca.CAPEM = "verify-ca", "-----BEGIN CERTIFICATE-----"

	ip := ok
	ip.Host, ip.Addrs = "10.0.0.5", []string{"10.0.0.5"}

	for _, good := range []DBRemoteJobRequest{ca, ip} {
		if err := good.Validate(); err != nil {
			t.Errorf("%+v: %v", good, err)
		}
	}

	for name, mutate := range map[string]func(*DBRemoteJobRequest){
		"kind":          func(r *DBRemoteJobRequest) { r.Kind = "load" },
		"site":          func(r *DBRemoteJobRequest) { r.Site = "../x" },
		"database":      func(r *DBRemoteJobRequest) { r.Database = "mysql" },
		"path":          func(r *DBRemoteJobRequest) { r.Path = "../x.sql" },
		"host":          func(r *DBRemoteJobRequest) { r.Host = "DB.example.net" },
		"host newline":  func(r *DBRemoteJobRequest) { r.Host = "db\nsocket=/x" },
		"port":          func(r *DBRemoteJobRequest) { r.Port = 0 },
		"no addrs":      func(r *DBRemoteJobRequest) { r.Addrs = nil },
		"loopback":      func(r *DBRemoteJobRequest) { r.Addrs = []string{"127.0.0.1"} },
		"link-local":    func(r *DBRemoteJobRequest) { r.Addrs = []string{"fe80::1"} },
		"mapped":        func(r *DBRemoteJobRequest) { r.Addrs = []string{"::ffff:10.0.0.5"} },
		"not canonical": func(r *DBRemoteJobRequest) { r.Addrs = []string{"2001:0db8::5"} },
		"twice":         func(r *DBRemoteJobRequest) { r.Addrs = []string{"10.0.0.5", "10.0.0.5"} },
		"ip host other": func(r *DBRemoteJobRequest) { r.Host = "10.0.0.6" },
		"tls":           func(r *DBRemoteJobRequest) { r.TLS = "maybe" },
		"ca without":    func(r *DBRemoteJobRequest) { r.CAPEM = "x" },
		"ca missing":    func(r *DBRemoteJobRequest) { r.TLS = "verify-ca" },
		"user":          func(r *DBRemoteJobRequest) { r.User = "root" },
		"password":      func(r *DBRemoteJobRequest) { r.Password = "short" },
		"password char": func(r *DBRemoteJobRequest) { r.Password = "Abcdefghijklmnopqrstuvwxyz01234#" },
	} {
		r := ok
		r.Addrs = append([]string(nil), ok.Addrs...)
		mutate(&r)

		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
