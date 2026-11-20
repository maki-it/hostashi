// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/sshkeys"
)

const (
	// maxKeyLines bounds how many lines of one authorized_keys are read.
	maxKeyLines = 1000
	// maxComment is how much of a key's comment is kept.
	maxComment = 200
)

// sshKeys reads the site user's authorized_keys files, the OpenSSH default
// paths. Only the public keys are kept: options are dropped (the importer
// sets its own), keys Hostashi does not accept become findings.
func (s *scanner) sshKeys(site *Site) {
	u, ok := s.users[site.User]
	if !ok {
		return
	}

	s.userKeys(site, u, "", map[string]bool{})
	capKeys(site)
}

// extraUsers finds the additional SSH users CloudPanel creates for a site:
// their own passwd entry and home, but the site user's group as primary
// group. Their keys become keys of the site's one login (ADR-0032), marked
// with the user name. Users without keys (FTP or password logins) are not
// carried over.
func (s *scanner) extraUsers() {
	names := make([]string, 0, len(s.users))
	for n := range s.users {
		names = append(names, n)
	}

	sort.Strings(names)

	for i := range s.rep.Sites {
		site := &s.rep.Sites[i]

		owner, ok := s.users[site.User]
		if !ok || owner.GID < minSiteUID {
			continue
		}

		seen := map[string]bool{}
		for _, k := range site.SSHKeys {
			seen[k.Fingerprint] = true
		}

		for _, n := range names {
			u := s.users[n]
			if u.GID != owner.GID || u.UID == owner.UID || u.UID < minSiteUID || u.UID == 65534 || s.used[n] {
				continue
			}

			s.extra[n] = true
			site.SSHUsers = append(site.SSHUsers, n)

			before := len(site.SSHKeys)
			s.userKeys(site, u, n, seen)

			if len(site.SSHKeys) == before {
				site.Findings = append(site.Findings, Finding{SevInfo, "ssh_user_no_keys", "additional user " + n + " has no SSH key (password or FTP login); not carried over", "/etc/passwd", u.Line})
			}
		}

		capKeys(site)
	}
}

// userKeys adds the keys of one user's authorized_keys to the site. extra
// is the name of an additional user, empty for the site user.
func (s *scanner) userKeys(site *Site, u passwdEntry, extra string, seen map[string]bool) {
	if !strings.HasPrefix(u.Home, "/home/") {
		return
	}

	for _, name := range []string{"authorized_keys", "authorized_keys2"} {
		p := strings.TrimSuffix(u.Home, "/") + "/.ssh/" + name

		b, err := s.r.ReadUserFile(p, u.UID, maxSmall)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
			continue
		}

		if err != nil {
			site.Findings = append(site.Findings, Finding{SevReview, "ssh_keys_unreadable", err.Error(), p, 0})

			continue
		}

		keys, found := parseAuthorizedKeys(string(b), p, seen)
		for i := range keys {
			keys[i].User = extra
		}

		site.SSHKeys = append(site.SSHKeys, keys...)
		site.Findings = append(site.Findings, found...)
		site.Sources = append(site.Sources, p)
	}
}

func capKeys(site *Site) {
	if len(site.SSHKeys) > sshkeys.MaxKeys && !slices.ContainsFunc(site.Findings, func(f Finding) bool { return f.Code == "ssh_keys_too_many" && f.Source == "" }) {
		site.Findings = append(site.Findings, Finding{SevReview, "ssh_keys_too_many", fmt.Sprintf("%d keys, only the first %d are carried over", len(site.SSHKeys), sshkeys.MaxKeys), "", 0})
	}

	if len(site.SSHKeys) > sshkeys.MaxKeys {
		site.SSHKeys = site.SSHKeys[:sshkeys.MaxKeys]
	}
}

// parseAuthorizedKeys reads the keys of one file. Findings name the line
// and the reason, never key material.
func parseAuthorizedKeys(src, source string, seen map[string]bool) ([]SSHKey, []Finding) {
	var (
		keys []SSHKey
		out  []Finding
	)

	for i, raw := range strings.Split(src, "\n") {
		ln := i + 1
		if ln > maxKeyLines {
			out = append(out, Finding{SevReview, "ssh_keys_too_many", fmt.Sprintf("more than %d lines, the rest is not read", maxKeyLines), source, ln})

			break
		}

		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		pk, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			out = append(out, Finding{SevReview, "ssh_key_unparsed", "line is not a public key", source, ln})

			continue
		}

		public, fp, err := sshkeys.Parse(strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n"))
		if err != nil {
			out = append(out, Finding{SevReview, "ssh_key_rejected", strings.TrimPrefix(err.Error(), "sshkeys: invalid: "), source, ln})

			continue
		}

		if len(opts) > 0 {
			out = append(out, Finding{SevReview, "ssh_key_options", "key options (for example from= or command=) are dropped; the key is carried over as SFTP only", source, ln})
		}

		if seen[fp] {
			continue
		}

		seen[fp] = true
		keys = append(keys, SSHKey{Public: public, Fingerprint: fp, Comment: trimComment(comment), Source: source, Line: ln})
	}

	return keys, out
}

func trimComment(c string) string {
	c = strings.TrimSpace(c)
	if !utf8.ValidString(c) {
		return ""
	}

	if len(c) > maxComment {
		c = c[:maxComment]
		for !utf8.ValidString(c) {
			c = c[:len(c)-1]
		}
	}

	return c
}
