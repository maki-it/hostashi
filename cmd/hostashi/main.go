// SPDX-License-Identifier: AGPL-3.0-or-later

// Command hostashi is the single binary with three modes:
//
//	hostashi serve   unprivileged web process (user hostashi)
//	hostashi agent   root agent, Unix socket only, typed operations only
//	hostashi ping    CLI: ask the agent directly (root on the server)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/acme"
	"github.com/maki-it/hostashi/internal/agent"
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/serve"
	"github.com/maki-it/hostashi/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: hostashi <command> [flags]

commands:
  serve     run the web process (never as root)
  agent     run the privileged agent (root only)
  ping      check that the agent answers
  site create NAME [POOL] | site delete NAME | site list
            manage site accounts through the agent (root on the server)
  web apply read the complete web state as JSON from stdin and make it live,
            e.g. {"sites":[{"user":"blog","domains":["blog.example.com"]}]}
  db list | db create SITE NAME | db delete NAME
  db user create DATABASE USER | db user password USER | db user delete USER
            manage databases through the agent (root on the server); the
            password is read from stdin, an empty line generates one
  backup run   one backup now (root; normally started by hostashi-backup.service)
  setup-token  print the one-time token for the first login (read as the serve user or root)
  version   print the version
`

func main() {
	os.Exit(mainWithSignals())
}

// mainWithSignals keeps the deferred stop out of the function that calls os.Exit.
func mainWithSignals() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return run(ctx, os.Args[1:], os.Geteuid(), os.Stdout, os.Stderr)
}

// run returns the process exit code: 0 ok, 1 runtime failure, 2 usage error.
func run(ctx context.Context, args []string, euid int, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", agentapi.DefaultSocket, "agent socket path")

	var err error

	switch cmd {
	case "serve":
		listen := fs.String("listen", "127.0.0.1:8080", "listen address (loopback only for now)")

		dbPath := fs.String("db", store.DefaultPath, "SQLite database path")

		keyPath := fs.String("key", secrets.DefaultKeyPath, "encryption key file for stored secrets (created on first start)")

		publicURL := fs.String("public-url", "", "address users open the panel at, e.g. https://panel.example.com (enables passkeys)")

		acmeDir := fs.String("acme-directory", acme.LetsEncrypt, "ACME directory for site certificates, empty turns certificates off")

		acmeCA := fs.String("acme-ca", "", "PEM file with an extra root CA for the ACME server (test CAs such as Pebble)")

		acmeChallenges := fs.String("acme-challenges", serve.DefaultChallengeDir, "directory for HTTP-01 challenge files, served by nginx")
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		if err = serve.CheckEUID(euid); err == nil {
			err = serve.Run(ctx, serve.Config{
				Listen: *listen, Socket: *socket, DBPath: *dbPath, KeyPath: *keyPath, PublicURL: *publicURL, Version: version, Log: log,
				ACMEDirectory: *acmeDir, ACMECAFile: *acmeCA, ACMEChallengeDir: *acmeChallenges,
			})
		}
	case "agent":
		serveUser := fs.String("serve-user", "hostashi", "the only unprivileged user allowed to call the agent")

		stateDir := fs.String("state-dir", agent.DefaultStateDir, "root-owned directory for the agent's site registry")
		panelPort := fs.Int("panel-port", agent.DefaultPanelPort, "HTTPS port of the panel's own address, served by nginx")

		if fs.Parse(args) != nil || fs.NArg() != 0 || *panelPort < 1 || *panelPort > 65535 {
			return 2
		}

		if err = agent.CheckEUID(euid); err == nil {
			err = agent.Run(ctx, agent.Config{Socket: *socket, ServeUser: *serveUser, StateDir: *stateDir, PanelPort: *panelPort, Version: version, Log: log})
		}
	case "ping":
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		var r agentapi.PingResponse

		r, err = agentapi.Ping.Call(ctx, agentrpc.NewClient(*socket), agentapi.PingRequest{})
		if err == nil {
			fmt.Fprintf(stdout, "agent %s answered, caller uid %d\n", r.Version, r.CallerUID)
		}
	case "site", "web", "db":
		if fs.Parse(args) != nil {
			return 2
		}

		err = adminCmd(ctx, agentrpc.NewClient(*socket), cmd, fs.Args(), stdout)
		if errors.Is(err, errUsage) {
			fmt.Fprint(stderr, usage)

			return 2
		}
	case "backup":
		stateDir := fs.String("state-dir", agent.DefaultStateDir, "the agent's state directory with the site and database registries")
		if fs.Parse(args) != nil || fs.NArg() != 1 || fs.Arg(0) != "run" {
			fmt.Fprint(stderr, usage)

			return 2
		}

		err = backup.RunOnce(ctx, backup.RunConfig{StateDir: *stateDir, Log: log})
	case "setup-token":
		dbPath := fs.String("db", store.DefaultPath, "SQLite database path")
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		var tok string
		if tok, err = auth.ReadSetupToken(serve.SetupTokenPath(*dbPath), time.Now()); err == nil {
			fmt.Fprintln(stdout, tok)
		}
	case "version":
		fmt.Fprintln(stdout, version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}

		fmt.Fprintf(stderr, "hostashi %s: %v\n", cmd, err)

		return 1
	}

	return 0
}
