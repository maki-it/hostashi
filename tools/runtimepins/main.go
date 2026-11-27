// SPDX-License-Identifier: AGPL-3.0-or-later

// Command runtimepins writes the runtimes data file (ADR-0077 part 2) from
// the Node.js and CPython pins in internal/agentapi. The workflow that
// builds the package hostashi-runtimes runs it on main; the binary reads the
// file with internal/runtimepins.
//
// -time sets the "generated" field (RFC 3339), so the same pins give the
// same file; the workflow passes the time of the last commit that changed
// them.
//
//	go run ./tools/runtimepins [-out runtimes.json] [-time 2026-11-27T05:00:00Z]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/runtimepins"
)

func main() {
	out := flag.String("out", "", "write the file here (default: standard output)")
	at := flag.String("time", "", `the "generated" time, RFC 3339 (default: now)`)

	flag.Parse()

	now := time.Now().UTC()

	var err error
	if *at != "" {
		now, err = time.Parse(time.RFC3339, *at)
	}

	var data []byte
	if err == nil {
		data, err = Encode(agentapi.NodeReleases, agentapi.PythonReleases, now.UTC())
	}

	if err == nil {
		if *out == "" {
			_, err = os.Stdout.Write(data)
		} else {
			err = os.WriteFile(*out, data, 0o644) //nolint:gosec // a world-readable data file
		}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "runtimepins:", err)
		os.Exit(1)
	}
}

// Encode returns the file for the given pins.
func Encode(node []agentapi.NodeRelease, python []agentapi.PythonRelease, now time.Time) ([]byte, error) {
	f := runtimepins.File{Schema: runtimepins.Schema, Generated: now.Format(time.RFC3339)}

	for _, r := range node {
		f.Node = append(f.Node, runtimepins.Node{Major: r.Major, Version: r.Version, Codename: r.Codename, End: r.End, SHA256: maps.Clone(r.SHA256)})
	}

	for _, r := range python {
		f.Python = append(f.Python, runtimepins.Python{Line: r.Line, Version: r.Version, Tag: r.Tag, End: r.End, SHA256: maps.Clone(r.SHA256)})
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(data, '\n'), nil
}
