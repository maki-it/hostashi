// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
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
