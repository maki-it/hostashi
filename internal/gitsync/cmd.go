// SPDX-License-Identifier: AGPL-3.0-or-later

package gitsync

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Flags of `hostashi git-sync`. The agent writes them into the deploy
// unit's drop-in, each quoted; secrets come as files from systemd's
// credentials, never as arguments.
type cmdFlags struct {
	url, branch, commit, auth, tokenUser, tokenFile, keyFile, hostKey string
	cache, release, sharedDir, result                                 string
	shared                                                            listFlag
	depth                                                             int
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// Run is `hostashi git-sync`. It refuses to run as root: it parses data
// from the network and runs as the site user only.
func Run(ctx context.Context, args []string, euid int, stderr io.Writer) error {
	if euid == 0 {
		return errors.New("git-sync must not run as root")
	}

	var f cmdFlags

	fs := flag.NewFlagSet("git-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.url, "url", "", "repository address")
	fs.StringVar(&f.branch, "branch", "", "branch")
	fs.StringVar(&f.commit, "commit", "", "full commit SHA-1 (default: the branch head)")
	fs.StringVar(&f.auth, "auth", "none", "none, token or ssh")
	fs.StringVar(&f.tokenUser, "token-user", "", "user sent with the token")
	fs.StringVar(&f.tokenFile, "token-file", "", "file holding the token")
	fs.StringVar(&f.keyFile, "key-file", "", "file holding the OpenSSH deploy key")
	fs.StringVar(&f.hostKey, "host-key", "", "confirmed host key (type base64)")
	fs.StringVar(&f.cache, "cache", "", "bare cache directory")
	fs.StringVar(&f.release, "release", "", "new release directory")
	fs.StringVar(&f.sharedDir, "shared-dir", "", "directory of the shared paths")
	fs.Var(&f.shared, "shared", "shared path below the release (repeatable)")
	fs.IntVar(&f.depth, "depth", 1, "fetch depth")
	fs.StringVar(&f.result, "result", "", "file to write the result to as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 || f.result == "" {
		return errors.New("git-sync: unexpected arguments or no -result")
	}

	auth := Auth{Kind: f.auth, TokenUser: f.tokenUser, HostKey: f.hostKey}

	if f.tokenFile != "" {
		b, err := readSecret(f.tokenFile)
		if err != nil {
			return err
		}

		auth.Token = strings.TrimSpace(string(b))
	}

	if f.keyFile != "" {
		b, err := readSecret(f.keyFile)
		if err != nil {
			return err
		}

		auth.Key = b
	}

	res, err := Sync(ctx, Options{
		URL: f.url, Branch: f.branch, Commit: f.commit, Auth: auth,
		Cache: f.cache, Release: f.release, Shared: f.shared, SharedDir: f.sharedDir,
		Depth: f.depth, Progress: stderr,
	})
	if err != nil {
		return err
	}

	b, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("git-sync: %w", err)
	}

	if err := os.WriteFile(f.result, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("git-sync: result: %w", err)
	}

	fmt.Fprintf(stderr, "deployed %s (%d entries)\n", res.Commit, res.Files)

	return nil
}

func readSecret(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("git-sync: %w", err)
	}

	if len(b) > 16<<10 {
		return nil, fmt.Errorf("git-sync: %s is too large", p)
	}

	return b, nil
}
