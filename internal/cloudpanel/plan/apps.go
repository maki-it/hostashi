// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

// proxyPlan plans a reverse proxy site (ADR-0054): the old proxy_pass is
// the target, checked like one typed in the panel. A target the panel
// refuses, such as this server's loopback, skips the site.
func proxyPlan(ps *Site, s scan.Site, skip, review func(code, msg string)) {
	ps.Proxy = s.Upstream

	u, err := agentapi.ParseProxyTarget(s.Upstream, append(slices.Clone(s.Domains), s.RedirectFrom...))
	if err != nil {
		skip("proxy_target", "the reverse proxy target "+s.Upstream+" is refused: "+err.Error())

		return
	}

	if u.Scheme == "https" {
		review("proxy_tls_verify", "the target's certificate is checked now, the old nginx did not check it; turn the check off in the site's settings if the target has a self-signed certificate")
	}

	if n := len(SSHKeys(s)); n > 0 {
		review("ssh_keys_no_account", fmt.Sprintf("%d SSH keys of the site user stay behind: a reverse proxy site has no Linux account", n))
	}
}

// nodePlan plans a Node.js site (ADR-0054). The scan does not see the old
// Node.js version nor how the app was started, so the site gets the newest
// Node.js line installed here and its app stays stopped until a start
// command is set. It gets a port of Hostashi's range, not the old one.
func nodePlan(ps *Site, s scan.Site, t Target, review func(code, msg string)) {
	ps.Node = nodeLine(t.Node)

	if slices.Contains(t.Node, ps.Node) {
		review("node_version", "the old Node.js version is not scanned: the site gets Node.js "+ps.Node+"; check that the app runs on it")
	} else {
		review("node_missing", "no Node.js line is installed here: install Node.js "+ps.Node+" on the Node.js page before you create the site")
	}

	port := ""
	if u, err := url.Parse(s.Upstream); err == nil && u.Port() != "" {
		port = " instead of " + u.Port()
	}

	review("node_start", "the app stays stopped until its start command is set on the site's Web tab; it listens on the port in PORT"+port+
		", and the old process manager is not carried over")
}

// nodeLine is the Node.js line for an imported Node.js site: the newest of
// the installed lines that Hostashi still pins, or the newest pinned line
// if none is installed.
func nodeLine(installed []string) string {
	for i := len(agentapi.NodeReleases) - 1; i >= 0; i-- {
		if m := agentapi.NodeReleases[i].Major; slices.Contains(installed, m) {
			return m
		}
	}

	return agentapi.NodeReleases[len(agentapi.NodeReleases)-1].Major
}

// pythonPlan plans a Python site (ADR-0061). The scan does not see the old
// Python version nor how the app was started, so the site gets the newest
// Python line installed here (or the newest pinned one) and its app stays
// stopped until a start command is set. Hostashi makes the environment
// .venv; the packages are installed over SSH.
func pythonPlan(ps *Site, s scan.Site, t Target, review func(code, msg string)) {
	ps.Python = pythonLine(t.Python)

	if slices.Contains(t.Python, ps.Python) {
		review("python_version", "the old Python version is not scanned: the site gets Python "+ps.Python+"; check that the app runs on it")
	} else {
		review("python_missing", "no Python line is installed here: install Python "+ps.Python+" on the Python versions page before you create the site")
	}

	port := ""
	if u, err := url.Parse(s.Upstream); err == nil && u.Port() != "" {
		port = " instead of " + u.Port()
	}

	project := "the project's requirements"
	if s.Project != "" {
		project = s.Project
	}

	review("python_start", "the app stays stopped until its start command is set on the site's Web tab; it listens on the port in PORT"+port+
		", and the old process manager is not carried over. After the files are copied, install "+project+" into ~/.venv over SSH")
}

// pythonLine is the Python line for an imported Python site: the newest of
// the installed lines that Hostashi still pins, or the newest pinned line
// if none is installed.
func pythonLine(installed []string) string {
	for i := len(agentapi.PythonReleases) - 1; i >= 0; i-- {
		if l := agentapi.PythonReleases[i].Line; slices.Contains(installed, l) {
			return l
		}
	}

	return agentapi.PythonReleases[len(agentapi.PythonReleases)-1].Line
}
