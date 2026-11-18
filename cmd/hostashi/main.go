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

	"github.com/maki-it/hostashi/internal/agent"
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
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
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		if err = serve.CheckEUID(euid); err == nil {
			err = serve.Run(ctx, serve.Config{Listen: *listen, Socket: *socket, DBPath: *dbPath, KeyPath: *keyPath, Version: version, Log: log})
		}
	case "agent":
		serveUser := fs.String("serve-user", "hostashi", "the only unprivileged user allowed to call the agent")
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		if err = agent.CheckEUID(euid); err == nil {
			err = agent.Run(ctx, agent.Config{Socket: *socket, ServeUser: *serveUser, Version: version, Log: log})
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
