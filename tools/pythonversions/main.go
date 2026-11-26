// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pythonversions checks python-build-standalone for new CPython
// builds of the Python lines Hostashi pins (ADR-0061): new patch versions,
// new builds of the same version, new lines and changed end dates. With
// -write it updates the list in internal/agentapi/python_versions.go, and
// with -verify it writes the archives of every changed build, so the
// "Python versions" workflow can check their checksum and Sigstore build
// provenance before it opens a pull request. It never installs anything
// and never merges. With -auto-merge it writes "yes" when the change may
// merge by itself (Report.PatchOnly, ADR-0077), else "no".
//
//	go run ./tools/pythonversions [-write] [-root DIR] [-summary FILE] [-verify FILE [-verify-newest]] [-auto-merge FILE]
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

const goFile = "internal/agentapi/python_versions.go"

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the file (default: only report)")
	summary := flag.String("summary", "", "write a Markdown summary to this file")
	verify := flag.String("verify", "", `write "SHA256 URL" of every changed archive to this file`)
	newest := flag.Bool("verify-newest", false, "also list the archives of the newest pinned line (a self-test of the check)")
	merge := flag.String("auto-merge", "", `write "yes" to this file when the change may merge by itself, else "no"`)

	flag.Parse()

	if err := run(*root, *write, *summary, *verify, *newest, *merge); err != nil {
		fmt.Fprintln(os.Stderr, "pythonversions:", err)
		os.Exit(1)
	}
}

func run(root string, write bool, summary, verify string, newest bool, merge string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	f := Fetcher{Client: &http.Client{Timeout: time.Minute}}

	cycle, err := f.Cycle(ctx)
	if err != nil {
		return err
	}

	tag, err := f.Latest(ctx)
	if err != nil {
		return err
	}

	builds, err := f.Sums(ctx, tag)
	if err != nil {
		return err
	}

	today := time.Now().UTC().Format(time.DateOnly)

	cand, err := Candidates(cycle, builds, tag, today)
	if err != nil {
		return err
	}

	releases, rep := Plan(agentapi.PythonReleases, cand)

	if newest && len(releases) > 0 {
		rep.Verify = append(rep.Verify, archives(releases[len(releases)-1])...)
	}

	md := Markdown(rep)
	fmt.Print(md)

	//nolint:gosec // reports for the workflow
	if summary != "" {
		if err := os.WriteFile(summary, []byte(md), 0o644); err != nil {
			return err
		}
	}

	//nolint:gosec // reports for the workflow
	if verify != "" {
		if err := os.WriteFile(verify, []byte(VerifyList(rep)), 0o644); err != nil {
			return err
		}
	}

	//nolint:gosec // reports for the workflow
	if merge != "" {
		if err := os.WriteFile(merge, []byte(yesNo(rep.PatchOnly())+"\n"), 0o644); err != nil {
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

	out, err := Rewrite(src, releases, today, tag)
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
