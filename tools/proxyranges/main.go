// SPDX-License-Identifier: AGPL-3.0-or-later

// Command proxyranges compares Cloudflare's published edge ranges with
// agentapi.CloudflareRanges (ADR-0059). It reads the three sources, needs
// them to agree, checks the ranges like an own range of a site, and with
// -write rewrites internal/agentapi/cloudflare.go. The weekly "Proxy
// ranges" workflow runs it and opens a pull request; servers never fetch
// the ranges themselves, and nothing is merged automatically.
//
//	go run ./tools/proxyranges [-write] [-root DIR] [-summary FILE]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Sources of the ranges.
const (
	urlV4  = "https://www.cloudflare.com/ips-v4"
	urlV6  = "https://www.cloudflare.com/ips-v6"
	urlAPI = "https://api.cloudflare.com/client/v4/ips"
)

// goFile is the file -write rewrites, relative to the repository root.
const goFile = "internal/agentapi/cloudflare.go"

// Lower bounds against a truncated or empty answer [A: Cloudflare has 15
// IPv4 and 7 IPv6 ranges on 2026-11-22].
const (
	minV4 = 5
	minV6 = 3
)

// maxBody bounds each answer.
const maxBody = 1 << 20

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the file (default: only report)")
	summary := flag.String("summary", "", "write a Markdown summary to this file")

	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := run(ctx, http.DefaultClient, *root, *write, *summary, time.Now().UTC())

	cancel()

	if err != nil {
		fmt.Fprintln(os.Stderr, "proxyranges:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c *http.Client, root string, write bool, summary string, now time.Time) error {
	got, err := Fetch(ctx, c, urlV4, urlV6, urlAPI)
	if err != nil {
		return err
	}

	added, removed := Diff(agentapi.CloudflareRanges, got)

	report := Summary(added, removed)
	fmt.Print(report)

	if summary != "" {
		if err := os.WriteFile(summary, []byte(report), 0o644); err != nil { //nolint:gosec // job summary, read by the next step
			return err
		}
	}

	if !write || (len(added) == 0 && len(removed) == 0) {
		return nil
	}

	return os.WriteFile(filepath.Join(root, goFile), Render(got, now), 0o644) //nolint:gosec // source file in the repository
}

// Fetch reads the plain lists and the API and returns the ranges, IPv4
// first, in the order Cloudflare publishes them. It fails unless both
// sources hold the same set and every range passes the checks of
// agentapi.WebRealIP.
func Fetch(ctx context.Context, c *http.Client, v4URL, v6URL, apiURL string) ([]string, error) {
	v4, err := get(ctx, c, v4URL)
	if err != nil {
		return nil, err
	}

	v6, err := get(ctx, c, v6URL)
	if err != nil {
		return nil, err
	}

	api, err := get(ctx, c, apiURL)
	if err != nil {
		return nil, err
	}

	plain4, plain6 := strings.Fields(string(v4)), strings.Fields(string(v6))

	var doc struct {
		Success bool `json:"success"`
		Result  struct {
			V4 []string `json:"ipv4_cidrs"`
			V6 []string `json:"ipv6_cidrs"`
		} `json:"result"`
	}

	if err := json.Unmarshal(api, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", apiURL, err)
	}

	if !doc.Success {
		return nil, fmt.Errorf("%s: success is false", apiURL)
	}

	if !sameSet(plain4, doc.Result.V4) || !sameSet(plain6, doc.Result.V6) {
		return nil, errors.New("the plain lists and the API disagree; not changing anything")
	}

	if len(plain4) < minV4 || len(plain6) < minV6 {
		return nil, fmt.Errorf("only %d IPv4 and %d IPv6 ranges; refusing a truncated list", len(plain4), len(plain6))
	}

	out := append(slices.Clone(plain4), plain6...)

	for i, s := range out {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("range %q: %w", s, err)
		}

		if (i < len(plain4)) != p.Addr().Is4() {
			return nil, fmt.Errorf("range %s is in the wrong list", s)
		}

		if p.Masked() != p {
			return nil, fmt.Errorf("range %s is not canonical", s)
		}
	}

	if err := (agentapi.WebRealIP{Ranges: out}).Validate(); err != nil {
		return nil, err
	}

	return out, nil
}

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "hostashi-proxyranges")

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	if len(b) > maxBody {
		return nil, fmt.Errorf("%s: answer larger than %d bytes", url, maxBody)
	}

	return b, nil
}

// sameSet reports whether a and b hold the same ranges, each once.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b) && len(slices.Compact(a)) == len(b)
}

// Diff returns the ranges in got but not in have, and the other way round.
func Diff(have, got []string) (added, removed []string) {
	for _, s := range got {
		if !slices.Contains(have, s) {
			added = append(added, s)
		}
	}

	for _, s := range have {
		if !slices.Contains(got, s) {
			removed = append(removed, s)
		}
	}

	return added, removed
}

// Summary is the Markdown report of a run.
func Summary(added, removed []string) string {
	if len(added) == 0 && len(removed) == 0 {
		return "Cloudflare's ranges are unchanged.\n"
	}

	var b strings.Builder

	for _, s := range added {
		b.WriteString("- added `" + s + "`\n")
	}

	for _, s := range removed {
		b.WriteString("- removed `" + s + "`\n")
	}

	return b.String()
}

// Render returns cloudflare.go for ranges, dated now.
func Render(ranges []string, now time.Time) []byte {
	var b bytes.Buffer

	b.WriteString(`// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

// CloudflareRanges are Cloudflare's published edge ranges (ADR-0059), from
// https://www.cloudflare.com/ips-v4, https://www.cloudflare.com/ips-v6
// and https://api.cloudflare.com/client/v4/ips, which agreed on
// CloudflareRangesDate. The weekly "Proxy ranges" workflow
// (tools/proxyranges) compares them again and opens a pull request when
// they change; servers never fetch them themselves.
var CloudflareRanges = []string{
`)

	for _, s := range ranges {
		b.WriteString("\t\"" + s + "\",\n")
	}

	b.WriteString(`}

// CloudflareRangesDate is the day CloudflareRanges last changed from
// Cloudflare's sources (YYYY-MM-DD).
const CloudflareRangesDate = "` + now.Format(time.DateOnly) + `"
`)

	return b.Bytes()
}
