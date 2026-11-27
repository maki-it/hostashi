// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"strconv"

	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

// deploySources say who started a run, in an email.
var deploySources = map[string]string{
	store.DeploySourcePanel:   "someone in the panel",
	store.DeploySourceAPI:     "an API token",
	store.DeploySourceWebhook: "a push (webhook)",
}

// DeployFailed is the email of ADR-0062 for a Git deploy of a site that
// failed. It goes to the accounts that may see the site and switched
// deploy emails on; each run is reported once.
func DeployFailed(site string, d store.SiteDeploy) Event {
	id := strconv.FormatInt(d.ID, 10)

	lines := []string{"deploy " + id + " of the site " + site + " failed.", ""}

	if d.Commit != "" {
		c := d.Commit
		if len(c) > 12 {
			c = c[:12]
		}

		if m := firstLine(d.Message); m != "" {
			c += " (" + m + ")"
		}

		lines = append(lines, "Commit: "+c)
	}

	if by, ok := deploySources[d.Source]; ok {
		lines = append(lines, "Started by: "+by)
	}

	if d.Error != "" {
		lines = append(lines, "Error: "+firstLine(d.Error))
	}

	lines = append(lines, "",
		"The site still serves the release that was live before. The log of the run is on the site's Git tab.")

	return Event{
		Category: Deploys, Site: site, Key: "deploy-failed:" + site + ":" + id, Fragment: "sites/" + site + "/git",
		Mail: mail.OpsEvent{Template: "deploy-failed", Subject: "Deploy of " + site + " failed", Lines: lines},
	}
}
