// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strconv"
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
		return []Check{warnT("hostashi.update", "update.error", "update.error.hint", P{"error": describe(err)})}
	}

	if st.Installed == "" {
		return nil
	}

	if st.Repository == "" {
		return []Check{warnT("hostashi.apt", "apt.norepo", "apt.norepo.hint", nil)}
	}

	if st.ListedAt.IsZero() {
		return []Check{warnT("hostashi.apt", "apt.nolist", "apt.nolist.hint", P{"repo": st.Repository})}
	}

	var out []Check

	if age := now.Sub(st.ListedAt); age > staleList {
		out = append(out, warnT("hostashi.apt", "apt.stale", "apt.stale.hint", P{"repo": st.Repository, "days": strconv.Itoa(int(age.Hours() / 24))}))
	} else {
		out = append(out, okT("hostashi.apt", "apt.ok", P{"repo": st.Repository, "time": st.ListedAt.UTC().Format("2006-01-02 15:04")}))
	}

	versions := P{"installed": st.Installed, "available": st.Available}

	switch {
	case !st.Update:
		out = append(out, okT("hostashi.update", "update.newest", versions))
	case st.Auto:
		out = append(out, okT("hostashi.update", "update.auto", versions))
	default:
		out = append(out, warnT("hostashi.update", "update.available", "update.available.hint", versions))
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
		return []Check{warnT("hostashi.runtimes", "runtimes.missing", "runtimes.missing.hint", nil)}
	case !st.RuntimesAuto:
		return []Check{warnT("hostashi.runtimes", "runtimes.manual", "runtimes.manual.hint", P{"version": st.Runtimes})}
	}

	return []Check{okT("hostashi.runtimes", "runtimes.ok", P{"version": st.Runtimes})}
}
