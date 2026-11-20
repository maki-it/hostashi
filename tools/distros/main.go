// SPDX-License-Identifier: AGPL-3.0-or-later

// Command distros checks whether a Debian stable or Ubuntu LTS release
// newer than the ones Hostashi supports is out (ADR-0020). For each one it
// writes an issue text: title on the first line, body after it. It never
// changes the repository; the monthly "Distributions" workflow opens the
// issues.
//
//	go run ./tools/distros [-root DIR] [-out DIR]
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	debianStableURL = "https://deb.debian.org/debian/dists/stable/Release"
	ubuntuLTSURL    = "https://changelogs.ubuntu.com/meta-release-lts"
	suryReleaseURL  = "https://packages.sury.org/php/dists/%s/Release"
	debianPkgsURL   = "https://deb.debian.org/debian/dists/%s/%s/binary-amd64/Packages.gz"
	ubuntuPkgsURL   = "http://archive.ubuntu.com/ubuntu/dists/%s/%s/binary-amd64/Packages.gz"
	maxBody         = 256 << 20
	installerFile   = "packaging/install.sh"
)

// Components that hold nginx and its brotli modules.
var components = map[string][]string{"debian": {"main"}, "ubuntu": {"main", "universe"}}

var nginxRe = regexp.MustCompile(`(?m)^NGINX_PKGS=\(([^)]*)\)$`)

// NginxPackages reads the nginx packages the installer needs.
func NginxPackages(installer []byte) ([]string, error) {
	m := nginxRe.FindAllSubmatch(installer, -1)
	if len(m) != 1 {
		return nil, errors.New("install.sh: NGINX_PKGS not found")
	}

	return strings.Fields(string(m[0][1])), nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "", "write one issue text per new release into this directory")

	flag.Parse()

	if err := run(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "distros:", err)
		os.Exit(1)
	}
}

func run(root, out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	installer, err := os.ReadFile(filepath.Join(root, installerFile))
	if err != nil {
		return err
	}

	supported, err := Supported(installer)
	if err != nil {
		return err
	}

	pkgs, err := NginxPackages(installer)
	if err != nil {
		return err
	}

	c := &http.Client{Timeout: 5 * time.Minute}

	b, err := get(ctx, c, debianStableURL)
	if err != nil {
		return err
	}

	debian, err := ParseDebianStable(b)
	if err != nil {
		return err
	}

	if b, err = get(ctx, c, ubuntuLTSURL); err != nil {
		return err
	}

	ubuntu, err := ParseUbuntuLTS(b, time.Now())
	if err != nil {
		return err
	}

	for _, r := range []Release{debian, ubuntu} {
		if !IsNew(r, supported) {
			fmt.Printf("%s (%s): supported or older, nothing to do\n", r.Name, r.Codename)

			continue
		}

		rd := readiness(ctx, c, r, pkgs)
		text := Title(r) + "\n" + IssueBody(r, rd, supported)
		fmt.Print(text)

		if out != "" {
			if err := os.WriteFile(filepath.Join(out, r.Codename+".md"), []byte(text), 0o600); err != nil {
				return err
			}
		}
	}

	return nil
}

// readiness looks up sury.org and the nginx packages; a failed download
// counts as "not yet".
func readiness(ctx context.Context, c *http.Client, r Release, pkgs []string) Readiness {
	rd := Readiness{Packages: map[string]bool{}}
	_, err := get(ctx, c, fmt.Sprintf(suryReleaseURL, r.Codename))
	rd.Sury = err == nil

	for _, p := range pkgs {
		rd.Packages[p] = false
	}

	url := debianPkgsURL
	if r.ID == "ubuntu" {
		url = ubuntuPkgsURL
	}

	for _, comp := range components[r.ID] {
		names, err := packageNames(ctx, c, fmt.Sprintf(url, r.Codename, comp))
		if err != nil {
			fmt.Fprintln(os.Stderr, "distros:", err)

			continue
		}

		for _, p := range pkgs {
			if names[p] {
				rd.Packages[p] = true
			}
		}
	}

	return rd
}

func open(ctx context.Context, c *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	req.Header.Set("User-Agent", "hostashi-distros")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	return resp.Body, nil
}

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	body, err := open(ctx, c, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	b, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return b, nil
}

func packageNames(ctx context.Context, c *http.Client, url string) (map[string]bool, error) {
	body, err := open(ctx, c, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	zr, err := gzip.NewReader(io.LimitReader(body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer zr.Close()

	out := map[string]bool{}
	sc := bufio.NewScanner(io.LimitReader(zr, 4*maxBody))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "Package: "); ok {
			out[strings.TrimSpace(name)] = true
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return out, nil
}
