// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/tabwriter"
	"time"
)

// firewallCmd runs "hostashi firewall" through the panel's API (ADR-0078):
// list fail2ban's blocked addresses, or unblock one in every Hostashi jail.
// Roles, scopes and the audit log apply as in the panel.
//
//	firewall bans [-json]
//	firewall unban ADDRESS
func firewallCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "bans":
		var r cliBans
		if err := apiDo(ctx, http.MethodGet, "/firewall/bans", nil, &r); err != nil {
			return err
		}

		printBans(out, r)

		return nil
	case len(args) == 2 && args[0] == "bans" && args[1] == "-json":
		return apiPrint(ctx, out, http.MethodGet, "/firewall/bans", nil)
	case len(args) == 2 && args[0] == "unban":
		return apiPrint(ctx, out, http.MethodDelete, "/firewall/bans/"+url.PathEscape(args[1]), nil)
	}

	return errUsage
}

type cliBans struct {
	Jails []struct {
		Name        string `json:"name"`
		Running     bool   `json:"running"`
		TotalFailed int    `json:"totalFailed"`
		Bans        []struct {
			Address string     `json:"address"`
			Since   time.Time  `json:"since"`
			Until   *time.Time `json:"until"`
		} `json:"bans"`
	} `json:"jails"`
}

func printBans(out io.Writer, r cliBans) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "JAIL\tADDRESS\tSINCE\tUNTIL")

	for _, j := range r.Jails {
		if !j.Running {
			fmt.Fprintf(w, "%s\t(not running)\t-\t-\n", j.Name)

			continue
		}

		for _, b := range j.Bans {
			until := "no end"
			if b.Until != nil {
				until = b.Until.Local().Format(time.DateTime)
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", j.Name, b.Address, b.Since.Local().Format(time.DateTime), until)
		}
	}

	_ = w.Flush()
}
