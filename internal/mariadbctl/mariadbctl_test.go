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
