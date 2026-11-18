// Command s6adopt scans a server tree read-only and prints what Hostashi
// could adopt. It writes nothing below -root.
//
//	s6adopt -gen /tmp/fixture            build the synthetic test server
//	s6adopt -root /tmp/fixture [-json]   scan a tree ("/" on a real server)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maki-it/hostashi/spike/s6-adopt/fixture"
	"github.com/maki-it/hostashi/spike/s6-adopt/scan"
)

func main() {
	root := flag.String("root", "", "directory that stands for / (read-only)")
	gen := flag.String("gen", "", "write the synthetic fixture to this new directory and exit")
	asJSON := flag.Bool("json", false, "print the full report as JSON")
	strict := flag.Bool("strict", os.Geteuid() == 0, "require root-owned config files")
	show := flag.Bool("show-raw", false, "include custom nginx snippets and cron commands verbatim (may contain tokens or passwords)")
	flag.Parse()

	if *gen != "" {
		if _, err := os.Stat(*gen); err == nil {
			fail(fmt.Errorf("%s exists, refusing to write into it", *gen))
		}
		cases, err := fixture.Build(*gen, time.Now())
		if err != nil {
			fail(err)
		}
		fmt.Printf("wrote %d synthetic sites to %s\n", len(cases), *gen)
		return
	}
	if *root == "" {
		flag.Usage()
		os.Exit(2)
	}
	rep, err := scan.Run(scan.Options{Root: *root, RequireRootOwned: *strict})
	if err != nil {
		fail(err)
	}
	if !*show {
		redact(rep)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fail(err)
		}
		return
	}
	fmt.Printf("%-24s %-8s %-10s %s\n", "SITE", "STATUS", "TYPE", "MAIN FINDINGS")
	for _, s := range rep.Sites {
		fmt.Printf("%-24s %-8s %-10s %s\n", s.Name, s.Status, s.Type, codes(s.Findings))
	}
	sm := rep.Summary
	fmt.Printf("\n%d sites: %d clean (%.0f %%), %d review, %d blocked; adoptable %.0f %%\n",
		sm.Sites, sm.Clean, sm.CleanPc, sm.Review, sm.Blocked, sm.AdoptablePc)
	for _, f := range append(append(rep.Global, rep.Ignored...), rep.Orphans...) {
		fmt.Printf("  [%s] %s: %s (%s)\n", f.Severity, f.Code, f.Msg, f.Source)
	}
}

// redact replaces free text that may hold credentials (proxy_set_header
// Authorization, curl tokens in cron) so a report can be shared safely.
func redact(rep *scan.Report) {
	for i := range rep.Sites {
		s := &rep.Sites[i]
		for j := range s.Custom {
			s.Custom[j].Raw = fmt.Sprintf("[%d bytes hidden, use -show-raw]", len(s.Custom[j].Raw))
		}
		for j := range s.Cron {
			s.Cron[j].Command = fmt.Sprintf("[%d bytes hidden, use -show-raw]", len(s.Cron[j].Command))
		}
	}
}

func codes(fs []scan.Finding) string {
	m := map[string]bool{}
	for _, f := range fs {
		if f.Severity != scan.SevInfo {
			m[f.Code] = true
		}
	}
	var out []string
	for c := range m {
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "s6adopt:", err)
	os.Exit(1)
}
