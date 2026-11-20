// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

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
	if !ok || !strings.HasPrefix(u.Home, "/home/") {
		return
	}

	seen := map[string]bool{}

	for _, name := range []string{"authorized_keys", "authorized_keys2"} {
		p := strings.TrimSuffix(u.Home, "/") + "/.ssh/" + name

		b, err := s.r.ReadUserFile(p, u.UID, maxSmall)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			site.Findings = append(site.Findings, Finding{SevReview, "ssh_keys_unreadable", err.Error(), p, 0})

			continue
		}

		keys, found := parseAuthorizedKeys(string(b), p, seen)
		site.SSHKeys = append(site.SSHKeys, keys...)
		site.Findings = append(site.Findings, found...)
		site.Sources = append(site.Sources, p)
	}

	if len(site.SSHKeys) > sshkeys.MaxKeys {
		site.Findings = append(site.Findings, Finding{SevReview, "ssh_keys_too_many", fmt.Sprintf("%d keys, only the first %d are carried over", len(site.SSHKeys), sshkeys.MaxKeys), "", 0})
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
