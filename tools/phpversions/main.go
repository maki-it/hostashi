// SPDX-License-Identifier: AGPL-3.0-or-later

// Command phpversions checks php.net and sury.org for new PHP minor versions
// and changed support dates (ADR-0036). With -write it updates the
// allowlist in internal/agentapi, packaging/install.sh,
// packaging/php/php-install and docs/03-features.md. The weekly "PHP
// versions" workflow runs it and opens a pull request; it never installs
// anything and never merges.
//
//	go run ./tools/phpversions [-write] [-root DIR] [-summary FILE]
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Files the tool reads and rewrites, relative to the repository root.
const (
	goFile         = "internal/agentapi/php_versions.go"
	installerFile  = "packaging/install.sh"
	phpInstallFile = "packaging/php/php-install"
	featuresFile   = "docs/03-features.md"
)

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the files (default: only report)")
	summary := flag.String("summary", "", "write a Markdown summary to this file")

	flag.Parse()

	if err := run(*root, *write, *summary); err != nil {
		fmt.Fprintln(os.Stderr, "phpversions:", err)
		os.Exit(1)
	}
}

func run(root string, write bool, summary string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	phpInstall, err := os.ReadFile(filepath.Join(root, phpInstallFile))
	if err != nil {
		return err
	}

	suffixes, err := PackageSuffixes(phpInstall)
	if err != nil {
		return err
	}

	f := Fetcher{Client: &http.Client{Timeout: time.Minute}}

	released, err := f.Active(ctx)
	if err != nil {
		return err
	}

	dates, err := f.Supported(ctx)
	if err != nil {
		return err
	}

	lists := map[string]map[string]bool{}
	missing := func(v string) []string {
		var out []string

		for _, dist := range SuryDists {
			for _, arch := range SuryArchs {
				key := dist + "/" + arch
				if lists[key] == nil {
					pkgs, err := f.Packages(ctx, dist, arch)
					if err != nil {
						// Treated as missing: the version waits for the next run.
						out = append(out, key+": "+err.Error())

						continue
					}

					lists[key] = pkgs
				}

				for _, s := range suffixes {
					if p := "php" + v + "-" + s; !lists[key][p] {
						out = append(out, key+": "+p)
					}
				}
			}
		}

		return out
	}

	cur := State{
		Versions: agentapi.PHPVersions,
		Legacy:   agentapi.LegacyPHPVersions,
		Security: agentapi.PHPSecurityUntil,
		Active:   agentapi.PHPActiveUntil,
	}
	next, rep := Plan(cur, Upstream{Released: released, Dates: dates, Missing: missing})

	md := Markdown(rep)
	fmt.Print(md)

	if summary != "" {
		if err := os.WriteFile(summary, []byte(md), 0o600); err != nil {
			return err
		}
	}

	if !write || !rep.Changed() {
		return nil
	}

	return Apply(root, next)
}

// Apply writes the state into the repository.
func Apply(root string, s State) error {
	src, err := RenderGo(s)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(root, goFile), src, 0o644); err != nil { //nolint:gosec // source file in the repository
		return err
	}

	for name, rewrite := range map[string]func([]byte, []string) ([]byte, error){
		installerFile:  RewriteInstaller,
		phpInstallFile: RewritePHPInstall,
		featuresFile:   RewriteFeatures,
	} {
		path := filepath.Join(root, name)

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		out, err := rewrite(b, s.Versions)
		if err != nil {
			return err
		}

		info, err := os.Stat(path)
		if err != nil {
			return err
		}

		if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
			return err
		}
	}

	return nil
}

// Markdown describes the report for the job summary and the pull request.
func Markdown(r Report) string {
	var b strings.Builder

	if !r.Changed() && len(r.Waiting) == 0 {
		b.WriteString("No new PHP version and no changed support dates.\n")
	}

	for _, v := range r.Added {
		fmt.Fprintf(&b, "- New version **PHP %s**: released on php.net, full package set on sury.org for %s (%s).\n",
			v, strings.Join(SuryDists, " and "), strings.Join(SuryArchs, ", "))
	}

	for _, d := range r.Dates {
		fmt.Fprintf(&b, "- Changed date: %s.\n", d)
	}

	waiting := make([]string, 0, len(r.Waiting))
	for v := range r.Waiting {
		waiting = append(waiting, v)
	}

	SortVersions(waiting)

	for _, v := range waiting {
		miss := r.Waiting[v]
		more := ""

		if len(miss) > 5 {
			more = fmt.Sprintf(" and %d more", len(miss)-5)
			miss = slices.Clone(miss[:5])
		}

		fmt.Fprintf(&b, "- PHP %s is released on php.net but not complete on sury.org yet, not added: %s%s.\n",
			v, strings.Join(miss, ", "), more)
	}

	return b.String()
}
