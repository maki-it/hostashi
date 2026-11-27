// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// firewallCmd runs "hostashi firewall" through the panel's API (ADR-0078):
// list fail2ban's blocked addresses, or unblock one in every Hostashi jail.
// Roles, scopes and the audit log apply as in the panel.
//
//	firewall bans [-jail JAIL] [-address TEXT] [-json]
//	firewall unban ADDRESS
func firewallCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) >= 1 && args[0] == "bans":
		fs := flag.NewFlagSet("firewall bans", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		jail := fs.String("jail", "", "")
		address := fs.String("address", "", "")
		asJSON := fs.Bool("json", false, "")

		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
			return errUsage
		}

		r, err := allBans(ctx, *jail, *address)
		if err != nil {
			return err
		}

		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")

			return enc.Encode(r)
		}

		printBans(out, r)

		return nil
	case len(args) == 2 && args[0] == "unban":
		return apiPrint(ctx, out, http.MethodDelete, "/firewall/bans/"+url.PathEscape(args[1]), nil)
	}

	return errUsage
}

type cliBans struct {
	Jails []struct {
		Name            string `json:"name"`
		Running         bool   `json:"running"`
		CurrentlyFailed int    `json:"currentlyFailed"`
		TotalFailed     int    `json:"totalFailed"`
		CurrentlyBanned int    `json:"currentlyBanned"`
		TotalBanned     int    `json:"totalBanned"`
	} `json:"jails"`
	Addresses []struct {
		Address string     `json:"address"`
		Jails   []string   `json:"jails"`
		Since   time.Time  `json:"since"`
		Until   *time.Time `json:"until"`
	} `json:"addresses"`
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

// banPages caps the pages one listing reads (100 addresses each).
const banPages = 1000

// allBans reads every page of the matching addresses.
func allBans(ctx context.Context, jail, address string) (cliBans, error) {
	var all cliBans

	for page := 1; page <= banPages; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}}
		if jail != "" {
			q.Set("jail", jail)
		}

		if address != "" {
			q.Set("address", address)
		}

		var r cliBans
		if err := apiDo(ctx, http.MethodGet, "/firewall/bans?"+q.Encode(), nil, &r); err != nil {
			return cliBans{}, err
		}

		if page == 1 {
			all.Jails, all.Total, all.Truncated = r.Jails, r.Total, r.Truncated
		}

		all.Addresses = append(all.Addresses, r.Addresses...)

		if len(r.Addresses) == 0 || len(all.Addresses) >= r.Total {
			break
		}
	}

	return all, nil
}

func printBans(out io.Writer, r cliBans) {
	for _, j := range r.Jails {
		if !j.Running {
			fmt.Fprintf(out, "jail %s is not running\n", j.Name)
		}
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tJAILS\tSINCE\tUNTIL")

	for _, b := range r.Addresses {
		until := "no end"
		if b.Until != nil {
			until = b.Until.Local().Format(time.DateTime)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Address, strings.Join(b.Jails, ","), b.Since.Local().Format(time.DateTime), until)
	}

	_ = w.Flush()

	if r.Truncated {
		fmt.Fprintln(out, "fail2ban blocks more addresses than the panel reads; some are missing.")
	}
}
