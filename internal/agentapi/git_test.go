// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
	"time"
)

func TestParseGitURL(t *testing.T) {
	good := map[string]GitRemote{
		"https://github.com/acme/site.git":         {Scheme: "https", Host: "github.com", Path: "acme/site.git"},
		"https://git.example.com:8443/a/b":         {Scheme: "https", Host: "git.example.com", Port: 8443, Path: "a/b"},
		"ssh://git@github.com/acme/site.git":       {Scheme: "ssh", User: "git", Host: "github.com", Path: "acme/site.git"},
		"ssh://gitea.example.com:2222/acme/site":   {Scheme: "ssh", User: "git", Host: "gitea.example.com", Port: 2222, Path: "acme/site"},
		"git@github.com:acme/site.git":             {Scheme: "ssh", User: "git", Host: "github.com", Path: "acme/site.git"},
		"deploy@git.example.com:/srv/git/site.git": {Scheme: "ssh", User: "deploy", Host: "git.example.com", Path: "srv/git/site.git"},
		"github.com:acme/site":                     {Scheme: "ssh", User: "git", Host: "github.com", Path: "acme/site"},
		"https://192.168.1.10/acme/site.git":       {Scheme: "https", Host: "192.168.1.10", Path: "acme/site.git"},
		"ssh://git@[2001:db8::1]:22/acme/site":     {Scheme: "ssh", User: "git", Host: "2001:db8::1", Port: 22, Path: "acme/site"},
	}

	for in, want := range good {
		got, err := ParseGitURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)

			continue
		}

		if got != want {
			t.Errorf("%s: got %+v, want %+v", in, got, want)
		}
	}

	bad := []string{
		"",
		"/srv/git/site.git",
		"./site",
		"file:///srv/git/site.git",
		"git://github.com/acme/site.git",
		"http://github.com/acme/site.git",
		"ext::sh -c touch% /tmp/pwned",
		"https://user:secret@github.com/acme/site.git",
		"https://token@github.com/acme/site.git",
		"https://github.com/acme/site.git?x=1",
		"https://github.com/acme/site.git#main",
		"https://github.com/",
		"https://github.com",
		"https://github.com:0/a/b",
		"https://github.com:99999/a/b",
		"https://github.com:/a/b",
		"https://localhost/a/b",
		"https://git.localhost/a/b",
		"https://127.0.0.1/a/b",
		"https://[::1]/a/b",
		"https://169.254.169.254/a/b",
		"https://0.0.0.0/a/b",
		"ssh://-oProxyCommand=x/a",
		"-oProxyCommand=x:a/b",
		"git@github.com:-a/b",
		"git@github.com://a/b",
		"git@github.com:a/../b",
		"git@github.com:a//b",
		"./a:b",
		"a/b:c",
		"https://github.com/acme/site .git",
		"https://github.com/acme/s\x00ite",
		"https://github.com/acme/sité",
		"https://exa_mple.com/a/b",
		"https://" + strings.Repeat("a", MaxGitURL) + ".com/a/b",
	}

	for _, in := range bad {
		if got, err := ParseGitURL(in); err == nil {
			t.Errorf("%q accepted: %+v", in, got)
		}
	}
}

func TestValidateGitBranch(t *testing.T) {
	for _, b := range []string{"main", "release/1.2", "feature/new-ui", "v2", "a_b.c"} {
		if err := ValidateGitBranch(b); err != nil {
			t.Errorf("%s: %v", b, err)
		}
	}

	for _, b := range []string{"", "HEAD", "@", "-main", "/main", "main/", "main.", "a..b", "a//b", "a@{1}", ".hidden", "a/.b", "x.lock", "a b", "a:b", "a~1", "a^", "a?", "a*", "a[", "a\\b", strings.Repeat("a", MaxGitBranch+1)} {
		if err := ValidateGitBranch(b); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestValidateGitDeployDir(t *testing.T) {
	for _, d := range []string{"htdocs", "htdocs/example.com", "app", "htdocs/example.com/public"} {
		if err := ValidateGitDeployDir(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}

	for _, d := range []string{"", ".", "/htdocs", "../x", "htdocs/../x", "htdocs/", "htdocs//a", ".deploy", ".deploy/releases", ".ssh", "htdocs/.git", "a\\b", "a\nb", strings.Repeat("a", MaxGitPath+1)} {
		if err := ValidateGitDeployDir(d); err == nil {
			t.Errorf("%q accepted", d)
		}
	}
}

func TestValidateGitShared(t *testing.T) {
	if err := ValidateGitShared([]string{".env", "wp-content/uploads", "storage"}); err != nil {
		t.Fatal(err)
	}

	if err := ValidateGitShared(nil); err != nil {
		t.Fatal(err)
	}

	bad := map[string][]string{
		"absolute":   {"/etc"},
		"dotdot":     {"../x"},
		"git":        {".git"},
		"in git":     {".git/config"},
		"duplicate":  {"a", "a"},
		"nested":     {"storage", "storage/logs"},
		"nested rev": {"storage/logs", "storage"},
		"not clean":  {"a/./b"},
		"dot":        {"."},
		"too many":   make([]string, MaxGitSharedPaths+1),
	}

	for name, p := range bad {
		if err := ValidateGitShared(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidateGitCommandsAndLimits(t *testing.T) {
	if err := ValidateGitCommands([]string{"composer install --no-dev --no-interaction", "npm ci && npm run build"}); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string][]string{
		"empty":     {""},
		"newline":   {"a\nb"},
		"too many":  make([]string, MaxGitCommands+1),
		"too long":  {strings.Repeat("a", 5000)},
		"blank one": {"ls", "  "},
	} {
		if err := ValidateGitCommands(c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	for _, n := range []int{MinGitKeep, DefaultGitKeep, MaxGitKeep} {
		if err := ValidateGitKeep(n); err != nil {
			t.Errorf("keep %d: %v", n, err)
		}
	}

	for _, n := range []int{0, 1, MaxGitKeep + 1} {
		if err := ValidateGitKeep(n); err == nil {
			t.Errorf("keep %d accepted", n)
		}
	}

	for _, d := range []time.Duration{time.Minute, DefaultBuildTimeout, time.Hour} {
		if err := ValidateGitBuildTimeout(d); err != nil {
			t.Errorf("timeout %s: %v", d, err)
		}
	}

	for _, d := range []time.Duration{0, 30 * time.Second, 90 * time.Second, 61 * time.Minute} {
		if err := ValidateGitBuildTimeout(d); err == nil {
			t.Errorf("timeout %s accepted", d)
		}
	}
}

func TestValidateGitToken(t *testing.T) {
	if err := ValidateGitToken("ghp_" + strings.Repeat("x", 36)); err != nil {
		t.Fatal(err)
	}

	for _, tok := range []string{"", "a b", "a\nb", "ä", strings.Repeat("a", MaxGitToken+1)} {
		if err := ValidateGitToken(tok); err == nil {
			t.Errorf("%q accepted", tok)
		}
	}

	for _, u := range []string{"", "x-access-token", "oauth2", "deploy@example.com"} {
		if err := ValidateGitTokenUser(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}

	for _, u := range []string{"a b", "a:b", strings.Repeat("a", MaxGitTokenUser+1)} {
		if err := ValidateGitTokenUser(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}
