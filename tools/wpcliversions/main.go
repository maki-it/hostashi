// SPDX-License-Identifier: AGPL-3.0-or-later

// Command wpcliversions checks GitHub for a new WP-CLI release (ADR-0069).
// It reads the latest release of wp-cli/wp-cli, downloads its phar and the
// .sha512 file next to it, checks that both agree and, with -write,
// updates the pin in internal/agentapi/wpcli.go. The weekly "WP-CLI
// version" workflow runs it and opens a pull request; it never installs
// anything and never merges.
//
//	go run ./tools/wpcliversions [-write] [-root DIR] [-summary FILE]
package main

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
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

	"github.com/maki-it/hostashi/internal/agentapi"
)

const goFile = "internal/agentapi/wpcli.go"

// Sources; tests replace them.
var (
	latestURL  = "https://api.github.com/repos/wp-cli/wp-cli/releases/latest"
	releaseURL = "https://github.com/wp-cli/wp-cli/releases/download"
)

// maxPhar bounds the download.
const maxPhar = 64 << 20

var versionRe = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "rewrite the file (default: only report)")
	summary := flag.String("summary", "", "write a Markdown summary to this file")

	flag.Parse()

	if err := run(*root, *write, *summary); err != nil {
		fmt.Fprintln(os.Stderr, "wpcliversions:", err)
		os.Exit(1)
	}
}

func run(root string, write bool, summary string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := &http.Client{Timeout: 2 * time.Minute}

	rel, err := Latest(ctx, c)
	if err != nil {
		return err
	}

	md := "No new WP-CLI release: " + agentapi.WPCLI.Version + " is the latest.\n"
	changed := rel != agentapi.WPCLI

	if changed {
		md = fmt.Sprintf("- WP-CLI %s → %s (SHA-512 of the phar checked against the release's .sha512 file)\n", agentapi.WPCLI.Version, rel.Version)
	}

	fmt.Print(md)

	if summary != "" {
		if err := os.WriteFile(summary, []byte(md), 0o644); err != nil { //nolint:gosec // a report for the workflow
			return err
		}
	}

	if !write || !changed {
		return nil
	}

	name := filepath.Join(root, goFile)

	src, err := os.ReadFile(name)
	if err != nil {
		return err
	}

	out, err := Rewrite(src, rel, time.Now().UTC().Format(time.DateOnly))
	if err != nil {
		return err
	}

	return os.WriteFile(name, out, 0o644) //nolint:gosec // a source file
}

func get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", url, limit)
	}

	return b, nil
}

// Latest returns the newest release with the SHA-512 of its phar, after
// checking the phar against the published .sha512 file.
func Latest(ctx context.Context, c *http.Client) (agentapi.WPCLIRelease, error) {
	b, err := get(ctx, c, latestURL, 1<<20)
	if err != nil {
		return agentapi.WPCLIRelease{}, err
	}

	var meta struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}

	if err := json.Unmarshal(b, &meta); err != nil {
		return agentapi.WPCLIRelease{}, err
	}

	m := versionRe.FindStringSubmatch(meta.Tag)
	if m == nil || meta.Draft || meta.Prerelease {
		return agentapi.WPCLIRelease{}, fmt.Errorf("latest release %q is no stable version", meta.Tag)
	}

	v := m[1]
	base := releaseURL + "/v" + v + "/wp-cli-" + v + ".phar"

	sumFile, err := get(ctx, c, base+".sha512", 1024)
	if err != nil {
		return agentapi.WPCLIRelease{}, err
	}

	want := strings.ToLower(strings.Fields(string(sumFile) + " ")[0])
	if _, err := hex.DecodeString(want); err != nil || len(want) != 128 {
		return agentapi.WPCLIRelease{}, fmt.Errorf("%s.sha512: no SHA-512", base)
	}

	phar, err := get(ctx, c, base, maxPhar)
	if err != nil {
		return agentapi.WPCLIRelease{}, err
	}

	sum := sha512.Sum512(phar)
	if hex.EncodeToString(sum[:]) != want {
		return agentapi.WPCLIRelease{}, errors.New("the phar does not match its .sha512 file")
	}

	return agentapi.WPCLIRelease{Version: v, SHA512: want}, nil
}

var (
	pinRe     = regexp.MustCompile(`(?s)var WPCLI = WPCLIRelease\{\n\tVersion: "[^"]*",\n\tSHA512:  "[0-9a-f]*",\n\}`)
	checkedRe = regexp.MustCompile(`// WPCLI is the WP-CLI release Hostashi installs\. Checked on [0-9-]+\n// against wp-cli-[0-9.]+\.phar\.sha512`)
)

// Rewrite replaces the pin and its date in the source of goFile.
func Rewrite(src []byte, r agentapi.WPCLIRelease, today string) ([]byte, error) {
	if !pinRe.Match(src) || !checkedRe.Match(src) {
		return nil, errors.New(goFile + ": the pin is not where it is expected")
	}

	out := pinRe.ReplaceAllLiteral(src, fmt.Appendf(nil, "var WPCLI = WPCLIRelease{\n\tVersion: %q,\n\tSHA512:  %q,\n}", r.Version, r.SHA512))
	out = checkedRe.ReplaceAllLiteral(out, fmt.Appendf(nil, "// WPCLI is the WP-CLI release Hostashi installs. Checked on %s\n// against wp-cli-%s.phar.sha512", today, r.Version))

	return out, nil
}
