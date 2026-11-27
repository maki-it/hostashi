// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"strings"
	"testing"
)

func TestFail2banUnbanValidate(t *testing.T) {
	for _, ok := range []string{"203.0.113.7", "2001:db8::1", "::1", "127.0.0.1"} {
		if err := (Fail2banUnbanRequest{Address: ok}).Validate(); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"", " 203.0.113.7", "203.0.113.7 ", "203.0.113.0/24", "2001:DB8::1", "2001:db8:0::1",
		"fe80::1%eth0", "::ffff:203.0.113.7", "203.0.113.7;id", "--report-absent", "example.com", "1.2.3",
	} {
		if err := (Fail2banUnbanRequest{Address: bad}).Validate(); !errors.Is(err, ErrFail2banAddress) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestFail2banStatusValidate(t *testing.T) {
	for _, ok := range []Fail2banStatusRequest{
		{}, {Jail: "recidive"}, {Address: "203.0.113."}, {Address: "2001:DB8::"}, {Address: "203.0.113.0/24"},
		{Address: "2001:db8::/32"}, {Offset: 100, Limit: Fail2banPageMax},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}

	for _, bad := range []Fail2banStatusRequest{
		{Jail: "apache"}, {Jail: "sshd;id"}, {Address: "203.0.113.7;id"}, {Address: "--report-absent"}, {Address: "example.com"},
		{Address: "203.0.113.0/"}, {Address: "203.0.113.0/40"}, {Address: "203.0.113.7 "}, {Address: strings.Repeat("1", Fail2banQueryMax+1)},
		{Offset: -1}, {Limit: -1}, {Limit: Fail2banPageMax + 1}, {Offset: fail2banOffsetMax + 1},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrFail2banFilter) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
}
