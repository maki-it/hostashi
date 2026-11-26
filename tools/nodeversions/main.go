// SPDX-License-Identifier: AGPL-3.0-or-later

// Command nodeversions checks nodejs.org for new releases of the Node.js
// LTS lines Hostashi pins (ADR-0054): patch and security releases, new LTS
// lines and changed end dates. It verifies the signed SHASUMS256.txt of
// every new release against the release team's keys and, with -write,
// updates the list in internal/agentapi/node_versions.go. The daily "Node
// versions" workflow runs it and opens a pull request; it never installs
// anything and never merges.
//
// With -auto-merge it writes "yes" when the change may merge by itself
// (Report.PatchOnly, ADR-0077), else "no".
//
//	go run ./tools/nodeversions [-write] [-root DIR] [-summary FILE] [-auto-merge FILE]
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

const goFile = "internal/agentapi/node_versions.go"

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the file (default: only report)")
	summary := flag.String("summary", "", "write a Markdown summary to this file")
	merge := flag.String("auto-merge", "", `write "yes" to this file when the change may merge by itself, else "no"`)

	flag.Parse()

	if err := run(*root, *write, *summary, *merge); err != nil {
		fmt.Fprintln(os.Stderr, "nodeversions:", err)
		os.Exit(1)
	}
}

func run(root string, write bool, summary, merge string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	f := Fetcher{Client: &http.Client{Timeout: time.Minute}}

	index, err := f.Index(ctx)
	if err != nil {
		return err
	}

	schedule, err := f.Schedule(ctx)
	if err != nil {
		return err
	}

	today := time.Now().UTC().Format(time.DateOnly)
	cand := Candidates(index, schedule, today)

	releases, rep, err := Plan(agentapi.NodeReleases, cand, func(v string) (map[string]string, error) { return f.Sums(ctx, v) })
	if err != nil {
		return err
	}

	md := Markdown(rep)
	fmt.Print(md)

	if summary != "" {
		if err := os.WriteFile(summary, []byte(md), 0o644); err != nil { //nolint:gosec // a report for the workflow
			return err
		}
	}

	if merge != "" {
		if err := os.WriteFile(merge, []byte(yesNo(rep.PatchOnly())+"\n"), 0o644); err != nil { //nolint:gosec // a report for the workflow
			return err
		}
	}

	if !write || !rep.Changed() {
		return nil
	}

	name := filepath.Join(root, goFile)

	src, err := os.ReadFile(name)
	if err != nil {
		return err
	}

	out, err := Rewrite(src, releases, today)
	if err != nil {
		return err
	}

	return os.WriteFile(name, out, 0o644) //nolint:gosec // a source file
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}
