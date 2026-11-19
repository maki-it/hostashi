// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import "fmt"

// Redact replaces free text that may hold credentials, such as an
// Authorization header in a custom snippet or a token in a cron command, with
// its length. A redacted report can be shared for review; the importer needs
// the full one.
func (rep *Report) Redact() {
	for i := range rep.Sites {
		s := &rep.Sites[i]

		for j := range s.Custom {
			s.Custom[j].Raw = hidden(len(s.Custom[j].Raw))
		}

		for j := range s.Cron {
			s.Cron[j].Command = hidden(len(s.Cron[j].Command))
		}
	}
}

func hidden(n int) string {
	return fmt.Sprintf("[%d bytes hidden]", n)
}
