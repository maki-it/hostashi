// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wpinstall installs the WP-CLI build that Hostashi pins
// (agentapi.WPCLI, ADR-0069).
//
// Install runs inside the fixed unit hostashi-wpcli-install.service
// (`hostashi wpcli-install`) as root: it downloads the phar of the pinned
// GitHub release, checks its SHA-512 while it writes it to a temporary
// file and only then moves it to Dir/<version>/wp-cli.phar, root-owned and
// readable by everyone. Nothing it downloads is run here; the site users
// run the phar with their own PHP (hostashi-wp@<site>, their wp command).
package wpinstall

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Paths and sources.
const (
	// Dir holds one directory per version, Dir/<version>/wp-cli.phar.
	Dir = "/opt/hostashi/wp-cli"
	// Phar is the file name inside a version's directory.
	Phar = "wp-cli.phar"
	// ReleaseURL is where WP-CLI publishes its release files.
	ReleaseURL = "https://github.com/wp-cli/wp-cli/releases/download"
	// MaxBytes bounds the download; the 2.12.0 phar has about 7 MB.
	MaxBytes = 64 << 20
	// DoneMarker starts the last line of a successful run.
	DoneMarker = "wp-cli installed:"
)

// ErrChecksum: the download is not the pinned build.
var ErrChecksum = errors.New("wpinstall: the download does not match the pinned SHA-512")

// Path returns the phar of a release below root ("" for /).
func Path(root string, r agentapi.WPCLIRelease) string {
	return filepath.Join(root, Dir, r.Version, Phar)
}

// Installer downloads and checks one release.
type Installer struct {
	Root    string // prefixed to Dir; empty means /
	Client  *http.Client
	URL     string // ReleaseURL unless set (tests)
	Out     io.Writer
	Release agentapi.WPCLIRelease // agentapi.WPCLI unless set (tests)
}

func (in *Installer) logf(format string, a ...any) {
	if in.Out != nil {
		_, _ = fmt.Fprintf(in.Out, format+"\n", a...)
	}
}

// Installed reports whether the phar of r is in place with its checksum.
func Installed(root string, r agentapi.WPCLIRelease) (bool, error) {
	f, err := os.Open(Path(root, r))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("wpinstall: %w", err)
	}

	defer f.Close()

	h := sha512.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxBytes+1)); err != nil {
		return false, fmt.Errorf("wpinstall: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)) == r.SHA512, nil
}

// Install puts the pinned phar in place, unless it is there already.
func (in *Installer) Install(ctx context.Context) error {
	rel := in.Release
	if rel.Version == "" {
		rel = agentapi.WPCLI
	}

	if ok, err := Installed(in.Root, rel); err != nil || ok {
		if ok {
			in.logf("%s %s (already there)", DoneMarker, rel.Version)
		}

		return err
	}

	base := in.URL
	if base == "" {
		base = ReleaseURL
	}

	client := in.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}

	dir := filepath.Join(in.Root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	url := base + "/v" + rel.Version + "/wp-cli-" + rel.Version + ".phar"
	in.logf("downloading %s", url)

	tmp, err := os.CreateTemp(dir, ".new-*.phar")
	if err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()
	defer tmp.Close()

	if err := download(ctx, client, url, tmp, rel.SHA512); err != nil {
		return err
	}

	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	vdir := filepath.Join(dir, rel.Version)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	if err := os.Rename(tmp.Name(), filepath.Join(vdir, Phar)); err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	in.logf("%s %s", DoneMarker, rel.Version)

	return nil
}

// download writes url to w and checks its SHA-512 against want.
func download(ctx context.Context, client *http.Client, url string, w io.Writer, want string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("wpinstall: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wpinstall: download: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wpinstall: download: %s", resp.Status)
	}

	h := sha512.New()

	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return fmt.Errorf("wpinstall: download: %w", err)
	}

	if n > MaxBytes {
		return fmt.Errorf("wpinstall: download is larger than %d bytes", MaxBytes)
	}

	if hex.EncodeToString(h.Sum(nil)) != want {
		return ErrChecksum
	}

	return nil
}
