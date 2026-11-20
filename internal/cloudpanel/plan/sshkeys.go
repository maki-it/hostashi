// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/sshkeys"
)

// SSHKey is a scanned key as the importer adds it: SFTP only (ADR-0032).
type SSHKey struct {
	Label  string
	Public string
}

// SSHKeys returns the scanned keys of a site that Hostashi accepts, at most
// sshkeys.MaxKeys and each once. The label comes from the key's comment,
// reduced to the characters a label may have, or "imported key N".
func SSHKeys(s scan.Site) []SSHKey {
	out := []SSHKey{}
	seen := map[string]bool{}

	for _, k := range s.SSHKeys {
		if len(out) == sshkeys.MaxKeys {
			break
		}

		public, fp, err := sshkeys.Parse(k.Public)
		if err != nil || seen[fp] {
			continue
		}

		seen[fp] = true

		out = append(out, SSHKey{Label: keyLabel(k.Comment, len(out)+1), Public: public})
	}

	return out
}

// keyLabel keeps the letters, digits and ". _ - @ +" of a comment, spaces
// for everything else, squeezed and trimmed to sshkeys.MaxLabel.
func keyLabel(comment string, n int) string {
	b := make([]byte, 0, len(comment))
	space := false

	for i := range len(comment) {
		c := comment[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("._-@+", c) >= 0 {
			if space && len(b) > 0 {
				b = append(b, ' ')
			}

			b = append(b, c)

			space = false

			continue
		}

		space = true
	}

	l := string(b)
	if len(l) > sshkeys.MaxLabel {
		l = strings.TrimRight(l[:sshkeys.MaxLabel], " ")
	}

	if sshkeys.CheckLabel(l) != nil {
		return "imported key " + strconv.Itoa(n)
	}

	return l
}
