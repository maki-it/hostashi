// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Exit code of hostashi doctor when at least one check failed. Warnings
// alone keep 0, so a monitoring check can tell "broken" from "not ideal".
const exitDoctorFailed = 3

// doctorCmd runs the checks through the agent and prints them, as text
// for a person or as JSON for a script.
func doctorCmd(ctx context.Context, c *agentrpc.Client, asJSON bool, stdout, stderr io.Writer) int {
	r, err := agentapi.Doctor.Call(ctx, c, agentapi.DoctorRequest{})
	if err != nil {
		fmt.Fprintf(stderr, "hostashi doctor: the agent does not answer: %v\n", err)

		return 1
	}

	if asJSON {
		e := json.NewEncoder(stdout)
		e.SetIndent("", "  ")

		if err := e.Encode(r); err != nil {
			return 1
		}
	} else {
		printDoctor(stdout, r)
	}

	if r.Worst() == agentapi.DoctorFail {
		return exitDoctorFailed
	}

	return 0
}

func printDoctor(w io.Writer, r agentapi.DoctorReport) {
	label := map[string]string{agentapi.DoctorOK: "ok  ", agentapi.DoctorWarn: "WARN", agentapi.DoctorFail: "FAIL"}
	site, first := "", true
	counts := map[string]int{}

	for _, c := range r.Checks {
		counts[c.Status]++

		if first || c.Site != site {
			site, first = c.Site, false

			if site == "" {
				fmt.Fprintln(w, "system")
			} else {
				fmt.Fprintf(w, "\nsite %s\n", site)
			}
		}

		fmt.Fprintf(w, "  %s  %-16s %s\n", label[c.Status], c.ID, c.Summary)

		if c.Hint != "" {
			fmt.Fprintf(w, "        %-16s %s\n", "", c.Hint)
		}
	}

	fmt.Fprintf(w, "\n%d ok, %d warnings, %d failed\n", counts[agentapi.DoctorOK], counts[agentapi.DoctorWarn], counts[agentapi.DoctorFail])
}
