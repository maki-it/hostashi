// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

var errRebootConfirm = errors.New("system reboot: pass the server's hostname to confirm")

// systemCmd runs "hostashi system" (ADR-0060). The agent checks every
// value again.
//
//	system info
//	system timezones
//	system timezone ZONE
//	system hostname NAME
//	system reboot HOSTNAME
func systemCmd(ctx context.Context, c *agentrpc.Client, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "info":
		return printJSON[agentapi.SystemInfo](out)(agentapi.SystemInfoOp.Call(ctx, c, agentapi.SystemInfoRequest{}))
	case len(args) == 1 && args[0] == "timezones":
		r, err := agentapi.SystemTimezonesOp.Call(ctx, c, agentapi.SystemTimezonesRequest{})
		if err != nil {
			return err
		}

		for _, z := range r.Zones {
			fmt.Fprintln(out, z)
		}

		return nil
	case len(args) == 2 && args[0] == "timezone":
		return printJSON[agentapi.SystemInfo](out)(agentapi.SystemTimezoneSet.Call(ctx, c, agentapi.SystemTimezoneRequest{Zone: args[1]}))
	case len(args) == 2 && args[0] == "hostname":
		return printJSON[agentapi.SystemInfo](out)(agentapi.SystemHostnameSet.Call(ctx, c, agentapi.SystemHostnameRequest{Name: args[1]}))
	case len(args) == 2 && args[0] == "reboot":
		info, err := agentapi.SystemInfoOp.Call(ctx, c, agentapi.SystemInfoRequest{})
		if err != nil {
			return err
		}

		if args[1] != info.Hostname {
			return errRebootConfirm
		}

		r, err := agentapi.SystemReboot.Call(ctx, c, agentapi.SystemRebootRequest{})
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "rebooting in %d seconds\n", r.DelaySeconds)

		return nil
	}

	return errUsage
}
