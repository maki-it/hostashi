// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateRelease(t *testing.T) {
	for _, r := range []string{"1", "42", "123456789012345678", InitialRelease} {
		if err := ValidateRelease(r); err != nil {
			t.Errorf("%s: %v", r, err)
		}
	}

	for _, r := range []string{"", "0", "01", "-1", "12345678901234567890", "0-initial/..", "../1", "1 ", "initial"} {
		if err := ValidateRelease(r); err == nil {
			t.Errorf("%q accepted", r)
		}
	}

	if ReleaseName(17) != "17" || ValidateRelease(ReleaseName(1<<62)) != nil {
		t.Fatal("release name")
	}
}

func TestDeployStartValidate(t *testing.T) {
	good := DeployStartRequest{
		Site: "blog", ID: 1, URL: "https://github.com/acme/site.git", Branch: "main", Auth: GitAuthNone,
		Commands: []string{"npm ci"}, Node: "22", TimeoutSeconds: 600,
	}

	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mod := range map[string]func(*DeployStartRequest){
		"id":           func(r *DeployStartRequest) { r.ID = 0 },
		"site":         func(r *DeployStartRequest) { r.Site = "../x" },
		"branch":       func(r *DeployStartRequest) { r.Branch = "-x" },
		"url":          func(r *DeployStartRequest) { r.URL = "file:///srv/x" },
		"auth":         func(r *DeployStartRequest) { r.Auth = "password" },
		"no token":     func(r *DeployStartRequest) { r.Auth = GitAuthToken },
		"no key":       func(r *DeployStartRequest) { r.Auth = GitAuthSSH },
		"key, https":   func(r *DeployStartRequest) { r.Auth, r.Key, r.HostKey = GitAuthSSH, "k", "ssh-ed25519 AAAA" },
		"no host key":  func(r *DeployStartRequest) { r.URL, r.Auth, r.Key = "git@github.com:acme/site.git", GitAuthSSH, "k" },
		"two lines":    func(r *DeployStartRequest) { r.Commands = []string{"a\nb"} },
		"node+python":  func(r *DeployStartRequest) { r.Python = "3.13" },
		"bad node":     func(r *DeployStartRequest) { r.Node = "22/../x" },
		"bad python":   func(r *DeployStartRequest) { r.Node, r.Python = "", "3" },
		"timeout":      func(r *DeployStartRequest) { r.TimeoutSeconds = 5 },
		"shared .git":  func(r *DeployStartRequest) { r.Shared = []string{".git/config"} },
		"huge key":     func(r *DeployStartRequest) { r.Auth, r.Key, r.HostKey = GitAuthSSH, strings.Repeat("k", 9000), "x" },
		"token, empty": func(r *DeployStartRequest) { r.Auth, r.Token = GitAuthToken, "" },
	} {
		r := good
		mod(&r)

		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	ssh := good
	ssh.URL, ssh.Auth, ssh.Key, ssh.HostKey = "git@github.com:acme/site.git", GitAuthSSH, "k", "ssh-ed25519 AAAA"

	if err := ssh.Validate(); err != nil {
		t.Fatalf("ssh: %v", err)
	}
}

func TestDeploySwitchPruneValidate(t *testing.T) {
	if err := (DeploySwitchRequest{Site: "blog", Release: "3", Dir: "htdocs"}).Validate(); err != nil {
		t.Fatal(err)
	}

	for _, r := range []DeploySwitchRequest{
		{Site: "blog", Release: "x", Dir: "htdocs"},
		{Site: "blog", Release: "3", Dir: "../htdocs"},
		{Site: "blog", Release: "3", Dir: "/srv"},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}

	if err := (DeployPruneRequest{Site: "blog", Dir: "htdocs", Keep: []string{"1", InitialRelease}}).Validate(); err != nil {
		t.Fatal(err)
	}

	if err := (DeployPruneRequest{Site: "blog", Dir: "htdocs", Keep: []string{"../x"}}).Validate(); err == nil {
		t.Fatal("bad keep accepted")
	}

	if err := (DeployPruneRequest{Site: "blog", Dir: "htdocs", Keep: make([]string, MaxGitKeep+2)}).Validate(); err == nil {
		t.Fatal("too many accepted")
	}

	if err := (DeployStatusRequest{Site: "blog"}).Validate(); err == nil {
		t.Fatal("status without id accepted")
	}
}
