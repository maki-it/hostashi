// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/updates"
)

// staleList is how old apt's list of the Hostashi repository may get: the
// daily apt-daily.timer refreshes it.
const staleList = 3 * 24 * time.Hour

// Update reports whether a newer hostashi package is available (ADR-0050).
// A server not set up from the package gets no checks.
func Update(st updates.Status, err error, now time.Time) []Check {
	if err != nil {
		return []Check{warn("hostashi.update", "cannot read the package state: "+describe(err), "Check /var/lib/dpkg/status and /var/lib/apt/lists.")}
	}

	if st.Installed == "" {
		return nil
	}

	if st.Repository == "" {
		return []Check{warn("hostashi.apt", "the Hostashi apt repository is not configured", "Run the install script again, or restore /etc/apt/sources.list.d/hostashi.sources.")}
	}

	if st.ListedAt.IsZero() {
		return []Check{warn("hostashi.apt", "apt has no package list of "+st.Repository, "Run: sudo apt update")}
	}

	var out []Check

	if age := now.Sub(st.ListedAt); age > staleList {
		out = append(out, warn("hostashi.apt",
			fmt.Sprintf("apt last fetched the package list of %s %d days ago", st.Repository, int(age.Hours()/24)),
			"Check that apt-daily.timer runs and that the server reaches "+st.Repository+": sudo apt update"))
	} else {
		out = append(out, ok("hostashi.apt", "apt fetched the package list of "+st.Repository+" on "+st.ListedAt.UTC().Format("2006-01-02 15:04 UTC")))
	}

	switch {
	case !st.Update:
		out = append(out, ok("hostashi.update", "hostashi "+st.Installed+" is the newest version"))
	case st.Auto:
		out = append(out, ok("hostashi.update", "hostashi "+st.Available+" is available (installed: "+st.Installed+"); the daily automatic updates install it"))
	default:
		c := warn("hostashi.update", "hostashi "+st.Available+" is available (installed: "+st.Installed+")",
			"Install it with: sudo apt update && sudo apt upgrade. The update keeps a copy of the panel database first.")
		c.Code, c.Params = "hostashi.update.available", map[string]string{"available": st.Available, "installed": st.Installed}
		out = append(out, c)
	}

	return out
}

// Runtimes reports whether the package hostashi-runtimes brings newer
// patch releases of the pinned Node.js and CPython lines (ADR-0077). A
// server not set up from the package gets no check.
func Runtimes(st updates.Status) []Check {
	if st.Installed == "" || st.Repository == "" {
		return nil
	}

	switch {
	case st.Runtimes == "":
		return []Check{warn("hostashi.runtimes", "the package hostashi-runtimes is not installed: newer patch releases of the pinned Node.js and Python lines do not arrive",
			"Install it now: sudo systemctl start hostashi-runtimes-install.service (the daily hostashi-runtimes-install.timer does the same; errors: journalctl -u hostashi-runtimes-install).")}
	case !st.RuntimesAuto:
		return []Check{warn("hostashi.runtimes", "unattended-upgrades does not take the suite runtimes: hostashi-runtimes "+st.Runtimes+" stays as it is",
			"Run the installer again: sudo /usr/share/hostashi/install.sh upgrade")}
	}

	return []Check{ok("hostashi.runtimes", "hostashi-runtimes "+st.Runtimes+" is installed and updated by unattended-upgrades")}
}
