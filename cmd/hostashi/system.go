// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// systemCmd runs "hostashi system" through the panel's API (ADR-0060,
// ADR-0058).
//
//	system info
//	system timezones
//	system timezone ZONE
//	system hostname NAME
//	system reboot HOSTNAME
func systemCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "info":
		return apiPrint(ctx, out, http.MethodGet, "/system", nil)
	case len(args) == 1 && args[0] == "timezones":
		var r struct {
			Zones []string `json:"zones"`
		}

		if err := apiDo(ctx, http.MethodGet, "/system/timezones", nil, &r); err != nil {
			return err
		}

		for _, z := range r.Zones {
			fmt.Fprintln(out, z)
		}

		return nil
	case len(args) == 2 && args[0] == "timezone":
		return apiPrint(ctx, out, http.MethodPut, "/system/timezone", map[string]string{"zone": args[1]})
	case len(args) == 2 && args[0] == "hostname":
		return apiPrint(ctx, out, http.MethodPut, "/system/hostname", map[string]string{"hostname": args[1]})
	case len(args) == 2 && args[0] == "reboot":
		// The panel checks the hostname as the confirmation.
		var r struct {
			DelaySeconds int `json:"delaySeconds"`
		}

		if err := apiDo(ctx, http.MethodPost, "/system/reboot", map[string]string{"confirm": args[1]}, &r); err != nil {
			return err
		}

		fmt.Fprintf(out, "rebooting in %d seconds\n", r.DelaySeconds)

		return nil
	}

	return errUsage
}
