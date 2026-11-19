// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cpimport"
	"github.com/maki-it/hostashi/internal/cpsource"
)

// cloudpanelCmd runs "hostashi cloudpanel scan" (ADR-0017). It is meant to
// run as root on the old CloudPanel server, or on a read-only copy of its
// disk, and only reads.
func cloudpanelCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "scan" {
		fmt.Fprint(stderr, usage)

		return 2
	}

	fs := flag.NewFlagSet("cloudpanel scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "/", "directory that stands for / of the old server (read-only)")
	asJSON := fs.Bool("json", false, "print the full report as JSON")
	full := fs.Bool("full", false, "keep custom nginx snippets and cron commands verbatim; they may contain passwords or tokens, so treat the output as secret")
	strict := fs.Bool("strict", true, "refuse config files that are not root-owned or are writable by others")

	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
		return 2
	}

	rep, err := scan.Run(scan.Options{Root: *root, RequireRootOwned: *strict})
	if err != nil {
		fmt.Fprintln(stderr, "hostashi cloudpanel scan:", err)

		return 1
	}

	if !*full {
		rep.Redact()
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(stderr, "hostashi cloudpanel scan:", err)

			return 1
		}

		return 0
	}

	printScan(stdout, rep)

	return 0
}

func printScan(w io.Writer, rep *scan.Report) {
	fmt.Fprintf(w, "%-32s %-8s %-10s %s\n", "SITE", "STATUS", "TYPE", "FINDINGS")

	for _, s := range rep.Sites {
		fmt.Fprintf(w, "%-32s %-8s %-10s %s\n", s.Name, s.Status, s.Type, findingCodes(s.Findings))
	}

	sm := rep.Summary
	fmt.Fprintf(w, "\n%d sites: %d clean, %d review, %d blocked; adoptable %.0f %%\n",
		sm.Sites, sm.Clean, sm.Review, sm.Blocked, sm.AdoptablePc)

	for _, list := range [][]scan.Finding{rep.Global, rep.Ignored, rep.Orphans} {
		for _, f := range list {
			fmt.Fprintf(w, "  [%s] %s: %s (%s)\n", f.Severity, f.Code, f.Msg, f.Source)
		}
	}
}

// findingCodes lists the distinct codes of all findings above info level.
func findingCodes(fs []scan.Finding) string {
	var out []string

	for _, f := range fs {
		if f.Severity != scan.SevInfo && !slices.Contains(out, f.Code) {
			out = append(out, f.Code)
		}
	}

	slices.Sort(out)

	return strings.Join(out, ", ")
}

// cloudpanelImportRun is hostashi cloudpanel import-run ID (ADR-0026).
func cloudpanelImportRun(ctx context.Context, stateDir, id string, log *slog.Logger) error {
	dir := filepath.Join(stateDir, "cloudpanel")

	return cpimport.RunOnce(ctx, cpimport.RunConfig{
		Dir: filepath.Join(dir, "imports"), StateDir: stateDir, Source: &cpsource.Source{Dir: dir}, Log: log,
	}, id)
}

// printJSON prints the answer of an agent call, indented.
func printJSON[T any](out io.Writer) func(T, error) error {
	return func(v T, err error) error {
		if err != nil {
			return err
		}

		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")

		return enc.Encode(v)
	}
}
