// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// hardeningSrc renders the site's hardening rules (ADR-0017 Part 4) into
// the server block that serves its files, before "location /" and the PHP
// location: nginx takes the first regular expression location that
// matches, so these come first, and a hidden PHP file such as
// /.git/x.php never reaches PHP. Every answer is 404, which does not tell
// whether the file exists. Every pattern is built in Go from paths that
// passed agentapi.WebHardening.Validate and regexp.QuoteMeta; it never
// holds a backslash followed by t, r or n, which nginx would turn into a
// control character inside quotes.
const hardeningSrc = `{{define "hardening"}}

    # Hidden files stay hidden, except /.well-known (RFC 8615).
    location ~ /\.(?!well-known/) {
        return 404;
    }
    {{- with .HD}}
    {{- with .ConfigRe}}

    # Dumps, backups, logs and editor copies are never served (ADR-0017).
    location ~* "{{.}}" {
        return 404;
    }
    {{- end}}
    {{- with .DenyDirsRe}}

    # Blocked folders (ADR-0017).
    location ~ "{{.}}" {
        return 404;
    }
    {{- end}}
    {{- with .NoPHPRe}}

    # PHP never runs in these folders (ADR-0017).
    location ~* "{{.}}" {
        return 404;
    }
    {{- end}}
    {{- range .DenyFiles}}

    location = /{{.}} {
        return 404;
    }
    {{- end}}
    {{- range .LimitFiles}}

    # At most {{$.HD.Rate}} requests per minute per address (ADR-0017).
    location = /{{.}} {
        limit_req zone={{$.HD.Zone}} burst={{$.HD.Burst}} nodelay;
        limit_req_status 429;
        {{- template "phpbody" $}}
    }
    {{- end}}
    {{- end}}
{{- end}}`

// hardeningView is what the hardening template renders for one site.
type hardeningView struct {
	ConfigRe, DenyDirsRe, NoPHPRe string
	DenyFiles, LimitFiles         []string
	// Zone is the site's rate limit zone, empty without LimitFiles.
	Zone        string
	Rate, Burst int
}

// configFileRe matches the names in agentapi.ConfigFileSuffixes.
var configFileRe = func() string {
	alts := make([]string, 0, len(agentapi.ConfigFileSuffixes))
	for _, s := range agentapi.ConfigFileSuffixes {
		alts = append(alts, regexp.QuoteMeta(s))
	}

	return "(?:" + strings.Join(alts, "|") + ")$"
}()

// quotedAlts joins paths, each quoted for a regular expression.
func quotedAlts(paths []string) string {
	alts := make([]string, 0, len(paths))
	for _, p := range paths {
		alts = append(alts, regexp.QuoteMeta(p))
	}

	return "(?:" + strings.Join(alts, "|") + ")"
}

// newHardeningView returns nil when the site has no rules beyond the
// fixed hidden-file rule. Rules that need PHP are left out without it.
func newHardeningView(s Site) *hardeningView {
	h := s.Hardening
	if h.IsZero() {
		return nil
	}

	v := &hardeningView{DenyFiles: h.DenyFiles}

	if h.ConfigFiles {
		v.ConfigRe = configFileRe
	}

	if len(h.DenyDirs) > 0 {
		// The folder itself too: without it, nginx would answer /folder
		// with a redirect to /folder/ and so reveal it.
		v.DenyDirsRe = "^/" + quotedAlts(h.DenyDirs) + "(?:/|$)"
	}

	if s.PHPSocket != "" {
		if len(h.NoPHPDirs) > 0 {
			v.NoPHPRe = "^/" + quotedAlts(h.NoPHPDirs) + `/.*\.(?:php|phtml|phar)$`
		}

		if len(h.LimitFiles) > 0 {
			v.LimitFiles = h.LimitFiles
			v.Zone = "hostashi_limit_" + hex.EncodeToString([]byte(s.User))
			v.Rate, v.Burst = agentapi.LimitRate, agentapi.LimitBurst
		}
	}

	return v
}
