// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/apiclient"
)

// The CLI as a client of the panel's API (ADR-0058): login, logout and
// whoami. Every API call carries an ordinary API token, so roles, scopes
// and the audit log apply as in the panel.

// apiWait sleeps between the polls of a browser login; tests replace it.
var apiWait = apiclient.Sleep

func userAgent() string { return "hostashi-cli/" + version }

// apiLogin runs "hostashi login [-token] [-name NAME] [URL]".
func apiLogin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fromStdin := fs.Bool("token", false, "read an existing API token from stdin instead of logging in through the browser")
	name := fs.String("name", "", `name of the new token (default "hostashi CLI on <hostname>")`)

	if fs.Parse(args) != nil || fs.NArg() > 1 {
		fmt.Fprint(stderr, "usage: hostashi login [-token] [-name NAME] [URL]\n")

		return 2
	}

	err := func() error {
		path, stored, err := storedLogin()
		if err != nil {
			return err
		}

		target := stored.URL
		if fs.NArg() == 1 {
			target = fs.Arg(0)
		} else if v := os.Getenv(apiclient.EnvURL); v != "" {
			target = v
		}

		if target == "" {
			target = apiclient.DefaultURL
		}

		base, err := apiclient.CheckURL(target)
		if err != nil {
			return err
		}

		var token string

		if *fromStdin {
			if token, err = readToken(stdin); err != nil {
				return err
			}
		} else {
			if token, err = browserLogin(ctx, base, *name, stdout); err != nil {
				return err
			}
		}

		me, err := whoami(ctx, apiclient.New(base, token, userAgent()))
		if err != nil {
			return fmt.Errorf("the token does not work at %s: %w", base, err)
		}

		if err := apiclient.SaveCredentials(path, apiclient.Credentials{URL: base, Token: token}); err != nil {
			return err
		}

		fmt.Fprintf(stdout, "Logged in to %s as %s (%s). The token is stored in %s.\n", base, me.Username, me.Role, path)

		return nil
	}()

	return exitCode("login", err, stderr)
}

func browserLogin(ctx context.Context, base, name string, stdout io.Writer) (string, error) {
	if name == "" {
		host, _ := os.Hostname()

		name = "hostashi CLI on " + host
		if host == "" {
			name = "hostashi CLI"
		}
	}

	c := apiclient.New(base, "", userAgent())

	nt, err := c.DeviceLogin(ctx, name, func(st apiclient.DeviceStart) {
		fmt.Fprintf(stdout, "Open this page in a browser where you are logged in to the panel:\n\n  %s\n\n"+
			"and check that it shows the code %s. Waiting for approval (the code is valid for %d minutes)…\n",
			st.VerificationURIComplete, st.UserCode, st.ExpiresIn/60)
	}, apiWait)
	if err != nil {
		return "", err
	}

	return nt.Token, nil
}

// readToken reads one token line, so that "pass show … | hostashi login
// -token" works.
func readToken(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("no token on stdin")
	}

	return token, nil
}

// apiLogout forgets the stored login. The token itself stays valid until
// it is revoked in the panel; the message says so.
func apiLogout(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, "usage: hostashi logout\n")

		return 2
	}

	err := func() error {
		path, stored, err := storedLogin()
		if err != nil {
			return err
		}

		if stored.Token == "" {
			fmt.Fprintln(stdout, "Not logged in.")

			return nil
		}

		if err := apiclient.RemoveCredentials(path); err != nil {
			return err
		}

		fmt.Fprintf(stdout, "Removed the stored token for %s. Revoke it under Account > API tokens if it may have leaked.\n", stored.URL)

		return nil
	}()

	return exitCode("logout", err, stderr)
}

// apiWhoami shows the account and panel the CLI talks to.
func apiWhoami(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, "usage: hostashi whoami\n")

		return 2
	}

	err := func() error {
		c, err := apiClient()
		if err != nil {
			return err
		}

		me, err := whoami(ctx, c)
		if err != nil {
			return err
		}

		fmt.Fprintf(stdout, "%s (%s) at %s\n", me.Username, me.Role, c.BaseURL)

		if len(me.Scopes) > 0 {
			fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(me.Scopes, " "))
		}

		return nil
	}()

	return exitCode("whoami", err, stderr)
}

type meAnswer struct {
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Scopes   []string `json:"scopes"`
}

func whoami(ctx context.Context, c *apiclient.Client) (meAnswer, error) {
	var me meAnswer

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.Do(ctx, http.MethodGet, "/auth/me", nil, &me)

	return me, err
}

// storedLogin reads the credentials file.
func storedLogin() (string, apiclient.Credentials, error) {
	path, err := apiclient.CredentialsPath()
	if err != nil {
		return "", apiclient.Credentials{}, err
	}

	c, err := apiclient.LoadCredentials(path)

	return path, c, err
}

// apiClient is the client for API commands: the stored login, overridden
// by HOSTASHI_URL and HOSTASHI_TOKEN.
func apiClient() (*apiclient.Client, error) {
	_, stored, err := storedLogin()
	if err != nil {
		return nil, err
	}

	c, err := apiclient.Resolve(stored, os.Getenv)
	if err != nil {
		return nil, err
	}

	if c.Token == "" {
		return nil, apiclient.ErrNotLoggedIn
	}

	return apiclient.New(c.URL, c.Token, userAgent()), nil
}

func exitCode(cmd string, err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	fmt.Fprintf(stderr, "hostashi %s: %v\n", cmd, err)

	return 1
}
