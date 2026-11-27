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

var germanDeploySources = map[string]string{
	store.DeploySourcePanel:   "jemandem im Panel",
	store.DeploySourceAPI:     "einem API-Token",
	store.DeploySourceWebhook: "einem Push (Webhook)",
}

// DeployFailed is the email of ADR-0062 for a Git deploy of a site that
// failed. It goes to the accounts that may see the site and switched
// deploy emails on; each run is reported once.
func DeployFailed(site string, d store.SiteDeploy) Event {
	id := strconv.FormatInt(d.ID, 10)

	lines := []string{"deploy " + id + " of the site " + site + " failed.", ""}
	de := []string{"das Deploy " + id + " der Site " + site + " ist fehlgeschlagen.", ""}

	if d.Commit != "" {
		c := d.Commit
		if len(c) > 12 {
			c = c[:12]
		}

		if m := firstLine(d.Message); m != "" {
			c += " (" + m + ")"
		}

		lines = append(lines, "Commit: "+c)
		de = append(de, "Commit: "+c)
	}

	if by, ok := deploySources[d.Source]; ok {
		lines = append(lines, "Started by: "+by)
		de = append(de, "Gestartet von: "+germanDeploySources[d.Source])
	}

	if d.Error != "" {
		lines = append(lines, "Error: "+firstLine(d.Error))
		de = append(de, "Fehler: "+firstLine(d.Error))
	}

	lines = append(lines, "",
		"The site still serves the release that was live before. The log of the run is on the site's Git tab.")
	de = append(de, "",
		"Die Site liefert weiter das Release aus, das vorher live war. Das Log des Laufs findest du im Tab Git der Site.")

	return Event{
		Category: Deploys, Site: site, Key: "deploy-failed:" + site + ":" + id, Fragment: "sites/" + site + "/git",
		Mail: mail.OpsEvent{
			Template: "deploy-failed", Subject: "Deploy of " + site + " failed", Lines: lines,
			German: mail.OpsText{Subject: "Deploy von " + site + " fehlgeschlagen", Lines: de},
		},
	}
}
