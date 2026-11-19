// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import "time"

// SetNow replaces the clock in tests.
func (s *Service) SetNow(f func() time.Time) { s.now = f }
