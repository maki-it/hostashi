// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "testing"

func TestDBCopyRequest(t *testing.T) {
	t.Parallel()

	for _, r := range []DBCopyRequest{
		{Source: "a", Target: "a"},
		{Source: "", Target: "b"},
		{Source: "a", Target: "b;drop"},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v accepted", r)
		}
	}

	if err := (DBCopyRequest{Source: "shop_wp", Target: "stage_wp"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
