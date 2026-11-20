// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Upstream sources (ADR-0036). php.net decides when a version is released
// and how long it is supported; sury.org decides whether Hostashi can
// install it on the distributions it supports (ADR-0020).
const (
	activeURL    = "https://www.php.net/releases/active.php"
	supportedURL = "https://www.php.net/supported-versions.php"
	suryURL      = "https://packages.sury.org/php/dists/%s/main/binary-%s/Packages.gz"
)

// SuryDists are the sury.org suites of the distributions Hostashi installs
// on (Debian 13, Ubuntu 26.04), SuryArchs the architectures it ships.
var (
	SuryDists = []string{"trixie", "resolute"}
	SuryArchs = []string{"amd64", "arm64"}
)

// maxBody caps every download; the largest file, a sury.org package list,
// is well below it.
const maxBody = 64 << 20

// Dates are the support dates of one branch, as YYYY-MM-DD.
type Dates struct {
	Active, Security string
}

var minorRe = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)

// ParseActive returns the branches php.net/releases/active.php lists, that
// is the minor versions with a stable release that are still supported.
func ParseActive(b []byte) ([]string, error) {
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("active.php: %w", err)
	}

	var out []string

	for _, branches := range doc {
		for v := range branches {
			if !minorRe.MatchString(v) {
				return nil, fmt.Errorf("active.php: unexpected branch %q", v)
			}

			out = append(out, v)
		}
	}

	if len(out) == 0 {
		return nil, errors.New("active.php: no branches")
	}

	SortVersions(out)

	return out, nil
}

// One row of the table on php.net/supported-versions.php: the branch, then
// pairs of a date and a relative time for the initial release, the end of
// active support and the end of security support.
var rowRe = regexp.MustCompile(`(?s)<tr class="(?:stable|security|eol)">\s*<td>\s*<a href="/downloads\.php\?version=([0-9.]+)">[0-9.]+</a>\s*</td>` +
	`\s*<td>([^<]+)</td>\s*<td[^>]*>.*?</td>` +
	`\s*<td>([^<]+)</td>\s*<td[^>]*>.*?</td>` +
	`\s*<td>([^<]+)</td>`)

// ParseSupported reads the support dates per branch from the table on
// php.net/supported-versions.php.
func ParseSupported(b []byte) (map[string]Dates, error) {
	out := map[string]Dates{}

	for _, m := range rowRe.FindAllSubmatch(b, -1) {
		v := string(m[1])
		if !minorRe.MatchString(v) {
			return nil, fmt.Errorf("supported-versions: unexpected branch %q", v)
		}

		active, err := phpDate(string(m[3]))
		if err != nil {
			return nil, fmt.Errorf("supported-versions: %s: %w", v, err)
		}

		security, err := phpDate(string(m[4]))
		if err != nil {
			return nil, fmt.Errorf("supported-versions: %s: %w", v, err)
		}

		if security < active {
			return nil, fmt.Errorf("supported-versions: %s: security support ends before active support", v)
		}

		out[v] = Dates{Active: active, Security: security}
	}

	if len(out) == 0 {
		return nil, errors.New("supported-versions: no branches found, did the page change?")
	}

	return out, nil
}

// phpDate turns "31 Dec 2028" into "2028-12-31".
func phpDate(s string) (string, error) {
	t, err := time.Parse("2 Jan 2006", strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("date %q: %w", s, err)
	}

	return t.Format(time.DateOnly), nil
}

// ParsePackages returns the package names of a Debian package list.
func ParsePackages(r io.Reader) (map[string]bool, error) {
	out := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "Package: "); ok {
			out[strings.TrimSpace(name)] = true
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("package list: %w", err)
	}

	return out, nil
}

// Fetcher downloads the upstream data.
type Fetcher struct {
	Client *http.Client
}

func (f Fetcher) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	req.Header.Set("User-Agent", "hostashi-phpversions")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	return resp.Body, nil
}

func (f Fetcher) bytes(ctx context.Context, url string) ([]byte, error) {
	body, err := f.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	b, err := io.ReadAll(io.LimitReader(body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return b, nil
}

// Active fetches the supported branches from php.net.
func (f Fetcher) Active(ctx context.Context) ([]string, error) {
	b, err := f.bytes(ctx, activeURL)
	if err != nil {
		return nil, err
	}

	return ParseActive(b)
}

// Supported fetches the support dates from php.net.
func (f Fetcher) Supported(ctx context.Context) (map[string]Dates, error) {
	b, err := f.bytes(ctx, supportedURL)
	if err != nil {
		return nil, err
	}

	return ParseSupported(b)
}

// Packages fetches the package names sury.org has for one suite and
// architecture.
func (f Fetcher) Packages(ctx context.Context, dist, arch string) (map[string]bool, error) {
	url := fmt.Sprintf(suryURL, dist, arch)

	body, err := f.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	zr, err := gzip.NewReader(io.LimitReader(body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer zr.Close()

	pkgs, err := ParsePackages(io.LimitReader(zr, 4*maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return pkgs, nil
}
