// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"text/tabwriter"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// serviceCmd runs "hostashi service" through the panel's API (ADR-0052,
// ADR-0058): list the catalog's units, or run one catalog action. Roles,
// scopes and the audit log apply as in the panel.
//
//	service list [-json]
//	service ID [SITE] ACTION
func serviceCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		var r struct {
			Services []agentapi.ServiceState `json:"services"`
		}

		if err := apiDo(ctx, http.MethodGet, "/services", nil, &r); err != nil {
			return err
		}

		printServices(out, r.Services)

		return nil
	case len(args) == 2 && args[0] == "list" && args[1] == "-json":
		return apiPrint(ctx, out, http.MethodGet, "/services", nil)
	case len(args) > 0 && args[0] == "list":
		return errUsage
	case len(args) == 2:
		// Naming the action on the command line is the confirmation the
		// panel asks for with a typed "<action> <id>".
		path := "/services/" + url.PathEscape(args[0]) + "/" + url.PathEscape(args[1])

		return apiPrint(ctx, out, http.MethodPost, path, map[string]string{"confirm": args[1] + " " + args[0]})
	case len(args) == 3:
		path := "/sites/" + url.PathEscape(args[1]) + "/services/" + url.PathEscape(args[0]) + "/" + url.PathEscape(args[2])

		return apiPrint(ctx, out, http.MethodPost, path, map[string]string{"confirm": args[2] + " " + args[0]})
	}

	return errUsage
}

func printServices(out io.Writer, states []agentapi.ServiceState) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSITE\tSTATE\tRESTARTS\tSINCE")

	for _, s := range states {
		state, restarts, since := "not installed", "-", "-"

		if s.Installed {
			state = s.Active
			if s.Sub != "" {
				state += " (" + s.Sub + ")"
			}

			restarts = strconv.FormatUint(uint64(s.NRestarts), 10)

			if !s.ActiveSince.IsZero() {
				since = s.ActiveSince.Local().Format("2006-01-02 15:04:05")
			}
		}

		site := s.Site
		if site == "" {
			site = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.ID, site, state, restarts, since)
	}

	_ = w.Flush()
}
