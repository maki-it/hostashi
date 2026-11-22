// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Upstream sources.
const (
	// cycleURL is the Python release cycle of the devguide: status and end
	// of life of every line.
	cycleURL = "https://peps.python.org/api/release-cycle.json"
	// latestURL names the newest python-build-standalone release.
	latestURL = "https://raw.githubusercontent.com/astral-sh/python-build-standalone/latest-release/latest-release.json"
	// DistURL is where the releases' files are.
	DistURL = "https://github.com/astral-sh/python-build-standalone/releases/download"
	maxBody = 8 << 20
)

// Cycle is one line of the Python release cycle.
type Cycle struct {
	Status string `json:"status"` // feature, prerelease, bugfix, security, end-of-life
	// EndOfLife is "YYYY-MM" while planned, "YYYY-MM-DD" once it ended.
	EndOfLife string `json:"end_of_life"`
}

// ParseCycle reads release-cycle.json, keyed by line ("3.13").
func ParseCycle(b []byte) (map[string]Cycle, error) {
	var out map[string]Cycle
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("release-cycle.json: %w", err)
	}

	if len(out) == 0 {
		return nil, errors.New("release-cycle.json: no lines")
	}

	return out, nil
}

// ParseLatest reads latest-release.json and returns the release tag.
func ParseLatest(b []byte) (string, error) {
	var v struct {
		Version int    `json:"version"`
		Tag     string `json:"tag"`
	}

	if err := json.Unmarshal(b, &v); err != nil {
		return "", fmt.Errorf("latest-release.json: %w", err)
	}

	if v.Version != 1 || !tagRe.MatchString(v.Tag) {
		return "", fmt.Errorf("latest-release.json: version %d, tag %q", v.Version, v.Tag)
	}

	return v.Tag, nil
}

var tagRe = regexp.MustCompile(`^[0-9]{8}$`)

// Build is the archive of one CPython version for both architectures.
type Build struct {
	Version string
	SHA256  map[string]string // by arch: x86_64, aarch64
}

// archRe matches the install_only_stripped glibc archives Hostashi uses.
var archiveRe = regexp.MustCompile(`^cpython-(3\.[0-9]+\.[0-9]+)\+([0-9]{8})-(x86_64|aarch64)-unknown-linux-gnu-install_only_stripped\.tar\.gz$`)

// ParseSums reads a release's SHA256SUMS and returns, per line, the build
// of both architectures. Versions with a suffix (rc, freethreaded) and
// other flavors are skipped; a line with only one architecture is left
// out.
func ParseSums(b []byte, tag string) (map[string]Build, error) {
	out := map[string]Build{}
	sc := bufio.NewScanner(bytes.NewReader(b))

	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if !ok {
			continue
		}

		m := archiveRe.FindStringSubmatch(name)
		if m == nil || m[2] != tag {
			continue
		}

		if h, err := hex.DecodeString(sum); err != nil || len(h) != 32 {
			return nil, fmt.Errorf("SHA256SUMS: bad sum for %s", name)
		}

		version, arch := m[1], m[3]
		line := version[:strings.LastIndex(version, ".")]

		bld, ok := out[line]
		if !ok || CompareVersions(version, bld.Version) > 0 {
			bld = Build{Version: version, SHA256: map[string]string{}}
		} else if version != bld.Version {
			continue
		}

		bld.SHA256[arch] = sum
		out[line] = bld
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	for line, bld := range out {
		if len(bld.SHA256) != 2 {
			delete(out, line)
		}
	}

	if len(out) == 0 {
		return nil, errors.New("SHA256SUMS: no install_only_stripped builds for linux")
	}

	return out, nil
}

// Fetcher downloads the upstream data.
type Fetcher struct {
	Client *http.Client
}

func (f Fetcher) bytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	req.Header.Set("User-Agent", "hostashi-pythonversions")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return b, nil
}

// Cycle fetches the release cycle.
func (f Fetcher) Cycle(ctx context.Context) (map[string]Cycle, error) {
	b, err := f.bytes(ctx, cycleURL)
	if err != nil {
		return nil, err
	}

	return ParseCycle(b)
}

// Latest fetches the tag of the newest python-build-standalone release.
func (f Fetcher) Latest(ctx context.Context) (string, error) {
	b, err := f.bytes(ctx, latestURL)
	if err != nil {
		return "", err
	}

	return ParseLatest(b)
}

// Sums fetches the SHA256SUMS of a release.
func (f Fetcher) Sums(ctx context.Context, tag string) (map[string]Build, error) {
	b, err := f.bytes(ctx, DistURL+"/"+tag+"/SHA256SUMS")
	if err != nil {
		return nil, err
	}

	return ParseSums(b, tag)
}
