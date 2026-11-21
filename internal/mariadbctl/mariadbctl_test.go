// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadbctl

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDefaultsRejectsInjection(t *testing.T) {
	for _, r := range []Run{
		{User: "u1", Password: "pw\n[client]\nuser=root"},
		{User: "u1 # x", Password: "Secret-pass_12345"},
		{User: "u1", Password: "a'b-1234567890"},
		{User: "u1", Password: ""},
		{User: "", Password: "Secret-pass_12345"},
		{User: "u1", Password: "Secret-pass_12345", Socket: "/run/x;y"},
	} {
		if _, err := defaults(r); !errors.Is(err, ErrFailed) {
			t.Errorf("%+v accepted", r)
		}
	}

	got, err := defaults(Run{User: "hostashi_job_0123456789abcdef", Password: "Secret-pass_12345", Socket: "/run/mysqld/mysqld.sock"})
	if err != nil || string(got) != "[client]\nuser=hostashi_job_0123456789abcdef\npassword=Secret-pass_12345\nsocket=/run/mysqld/mysqld.sock\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRunRefusesRootAndBadNames(t *testing.T) {
	ctx := context.Background()

	if err := Import(ctx, Run{Database: "shop", UID: 0, GID: 0, User: "u1", Password: "Secret-pass_12345"}, strings.NewReader("")); !errors.Is(err, ErrFailed) {
		t.Fatalf("root: %v", err)
	}

	if err := Export(ctx, Run{Database: "--all-databases", UID: 1000, GID: 1000, User: "u1", Password: "Secret-pass_12345"}, nil); !errors.Is(err, ErrFailed) {
		t.Fatalf("option as name: %v", err)
	}
}

func TestDescribe(t *testing.T) {
	err := describe(errors.New("exit status 1"), []byte("ERROR 1044 (42000) at line 2: Access denied for user 'x' to database 'secret stuff'\n"))
	if err.Error() != "mariadbctl: failed: server error 1044 at line 2 of the dump" {
		t.Fatalf("%v", err)
	}

	if strings.Contains(describe(errors.New("x"), []byte("garbage with secret")).Error(), "secret") {
		t.Fatal("stderr leaked")
	}
}

func TestDefaultsRemote(t *testing.T) {
	base := Run{User: "hostashi_job_0123456789abcdef", Password: "Secret-pass_12345", Socket: "/run/mysqld/mysqld.sock"}

	for _, tc := range []struct {
		name string
		r    Remote
		want string
	}{
		{
			"verify",
			Remote{Host: "db.example.net", Port: 3306, TLS: "verify"},
			"protocol=tcp\nhost=db.example.net\nport=3306\nconnect-timeout=20\nssl\nssl-verify-server-cert\nssl-ca=/etc/ssl/certs/ca-certificates.crt\n",
		},
		{
			"verify-ca",
			Remote{Host: "10.0.0.5", Port: 3307, TLS: "verify-ca", CAFile: "/tmp/x/ca.pem"},
			"protocol=tcp\nhost=10.0.0.5\nport=3307\nconnect-timeout=20\nssl\nssl-verify-server-cert\nssl-ca=/tmp/x/ca.pem\n",
		},
		{"none", Remote{Host: "db1", Port: 3306, TLS: "none"}, "protocol=tcp\nhost=db1\nport=3306\nconnect-timeout=20\nskip-ssl\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.Remote = &tc.r

			got, err := defaults(r)
			if err != nil {
				t.Fatal(err)
			}

			want := "[client]\nuser=" + base.User + "\npassword=" + base.Password + "\n" + tc.want
			if string(got) != want {
				t.Fatalf("got\n%s\nwant\n%s", got, want)
			}
		})
	}

	for _, bad := range []Remote{
		{Host: "", Port: 3306, TLS: "none"},
		{Host: "db1", Port: 0, TLS: "none"},
		{Host: "db1", Port: 3306, TLS: "maybe"},
		{Host: "db1", Port: 3306, TLS: "verify-ca"},
		{Host: "db1\nsocket=/x", Port: 3306, TLS: "none"},
		{Host: "db1", Port: 3306, TLS: "verify-ca", CAFile: "/tmp/a b"},
	} {
		r := base
		r.Remote = &bad

		if _, err := defaults(r); !errors.Is(err, ErrFailed) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}
