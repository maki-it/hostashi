// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// serviceCmd runs "hostashi service": list the catalog's units, or run one
// catalog action (ADR-0052). The agent checks everything again.
//
//	service list [-json]
//	service ID [SITE] ACTION
func serviceCmd(ctx context.Context, c *agentrpc.Client, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		r, err := agentapi.ServicesStatus.Call(ctx, c, agentapi.ServicesStatusRequest{})
		if err != nil {
			return err
		}

		printServices(out, r.Services)

		return nil
	case len(args) == 2 && args[0] == "list" && args[1] == "-json":
		return printJSON[agentapi.ServicesStatusResponse](out)(agentapi.ServicesStatus.Call(ctx, c, agentapi.ServicesStatusRequest{}))
	case len(args) > 0 && args[0] == "list":
		return errUsage
	case len(args) == 2:
		return printJSON[agentapi.ServiceState](out)(agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: args[0], Action: args[1]}))
	case len(args) == 3:
		return printJSON[agentapi.ServiceState](out)(agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: args[0], Site: args[1], Action: args[2]}))
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
