// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "testing"

func TestVirusScanServicesHaveNoStop(t *testing.T) {
	for _, id := range []string{"freshclam", "clamd", "clamonacc"} {
		s, ok := LookupService(id)
		if !ok || !s.Optional || s.PerSite || s.Allows(ServiceStop) || !s.Allows(ServiceRestart) {
			t.Errorf("%s: %+v %v", id, s, ok)
		}

		if err := (ServiceActionRequest{ID: id, Action: ServiceStop}).Validate(); err == nil {
			t.Errorf("%s: stop accepted", id)
		}
	}
}
