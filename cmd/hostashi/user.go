// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/cpimport"
	"github.com/maki-it/hostashi/internal/store"
)

// envFile holds HOSTASHI_PUBLIC_URL, written by the installer.
const envFile = "/etc/hostashi/hostashi.env"

// userRecover gives someone with root on the server a way back into an
// account (ADR-0035): it removes the password and second factors, enables
// the account and prints a new invitation link. It runs as root and, before
// it opens the database, becomes the database's owner, so no file the
// panel needs ends up owned by root.
func userRecover(ctx context.Context, euid int, dbPath, publicURL, username string, out io.Writer) error {
	if euid != 0 {
		return errors.New("must run as root")
	}

	// Read as root, before the drop.
	if publicURL == "" {
		publicURL = publicURLFromEnv(envFile)
	}

	var fi syscall.Stat_t
	if err := syscall.Stat(dbPath, &fi); err != nil {
		return fmt.Errorf("database %s: %w", dbPath, err)
	}

	if int(fi.Uid) != os.Geteuid() {
		if err := cpimport.Drop(int(fi.Uid), int(fi.Gid)); err != nil {
			return err
		}
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}

	defer func() { _ = st.Close() }()

	svc := auth.NewService(auth.Config{Store: st})

	inv, acc, err := svc.RecoverAccount(ctx, username)
	if errors.Is(err, auth.ErrUserNotFound) {
		return fmt.Errorf("no account %q", username)
	}

	if err != nil {
		return err
	}

	fmt.Fprintf(out, "The account %s (%s) has no password and no second factor now, and its sessions ended.\n", acc.Username, acc.Role)
	fmt.Fprintf(out, "Open this link before %s to set a new password:\n\n", inv.Expires.Local().Format("2006-01-02 15:04 MST"))

	if publicURL == "" {
		fmt.Fprintf(out, "  <panel address>/#/invite/%s\n", inv.Secret)
	} else {
		fmt.Fprintf(out, "  %s/#/invite/%s\n", strings.TrimRight(publicURL, "/"), inv.Secret)
	}

	return nil
}

// publicURLFromEnv reads HOSTASHI_PUBLIC_URL from the installer's env
// file; empty if it is missing.
func publicURLFromEnv(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}

	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "HOSTASHI_PUBLIC_URL="); ok {
			return strings.Trim(v, `"'`)
		}
	}

	return ""
}
