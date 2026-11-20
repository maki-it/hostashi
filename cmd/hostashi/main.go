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
	"strings"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/acme"
	"github.com/maki-it/hostashi/internal/agent"
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/reqid"
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
  doctor [-json]
            check the server and every site without changing anything (root
            on the server); exit code 3 if a check failed, 0 otherwise
  site create NAME [POOL] | site delete NAME | site list
            manage site accounts through the agent (root on the server)
  site limits NAME [memory=BYTES] [tasks=N] [cpu-weight=N] [cpu-quota=PERCENT]
            show or set the CPU, memory and process limits of a site's slice;
            with values, every one left out means no limit (ADR-0015)
  web apply read the complete web state as JSON from stdin and make it live,
            e.g. {"sites":[{"user":"blog","domains":["blog.example.com"]}]}
  db list | db create SITE NAME | db delete NAME
  db user create DATABASE USER | db user password USER | db user delete USER
  db import DATABASE FILE | db export DATABASE FILE
            with FILE relative to the site home, ending in .sql or .sql.gz
            manage databases through the agent (root on the server); the
            password is read from stdin, an empty line generates one
  backup run   one backup now (root; normally started by hostashi-backup.service)
  backup get | backup set | backup hostkey | backup start | backup status
            backup settings through the agent (root on the server); set
            reads the settings as JSON and hostkey one public key from stdin
  backup restore SITE SNAPSHOT | backup restore-status ID
            restore a snapshot into a new directory restore-<time> in the
            site's home, through the agent (root on the server)
  backup restore-run ID
            one restore (root; started by hostashi-restore@ID.service)
  backup key show
            print the repository password and the panel key to keep outside
            the server (root only, never available over the API)
  cloudpanel scan [-root DIR] [-json] [-full]
            read a CloudPanel server (root on the old server, or a read-only
            copy of its disk) and show which sites can be imported; changes
            nothing, hides snippets and cron commands unless -full is given
  cloudpanel database
            make a new password for the read-only database user on the old
            server and print the SQL to run there (shown once)
  cloudpanel import SITE REMOTE_USER DOCROOT [OLD_DB[:NEW_DB] ...] | cloudpanel import-status ID
            copy the files of DOCROOT (below /home/REMOTE_USER/ on the old
            server) into SITE's home as cloudpanel-import, and dump each old
            database into cloudpanel-import-db/NEW_DB-<time>.sql for
            "db import", through the agent (root on the server); run it
            again for the final sync
  cloudpanel import-run ID
            one import (root; started by hostashi-import@ID.service)
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

	log := slog.New(reqid.Handler(slog.NewJSONHandler(stderr, nil)))
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

		metricsListen := fs.String("metrics-listen", "", "loopback IP and port that serves /metrics without a token, e.g. 127.0.0.1:9471 (empty: off)")
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		if err = serve.CheckEUID(euid); err == nil {
			err = serve.Run(ctx, serve.Config{
				Listen: *listen, Socket: *socket, DBPath: *dbPath, KeyPath: *keyPath, PublicURL: *publicURL, Version: version, Log: log,
				ACMEDirectory: *acmeDir, ACMECAFile: *acmeCA, ACMEChallengeDir: *acmeChallenges,
				MetricsListen: *metricsListen,
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
	case "doctor":
		asJSON := fs.Bool("json", false, "print the checks as JSON")
		if fs.Parse(args) != nil || fs.NArg() != 0 {
			return 2
		}

		return doctorCmd(ctx, agentrpc.NewClient(*socket), *asJSON, stdout, stderr)
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
		if fs.Parse(args) != nil {
			return 2
		}

		a := fs.Args()

		switch {
		case len(a) == 1 && a[0] == "run":
			err = backup.RunOnce(ctx, backup.RunConfig{StateDir: *stateDir, Log: log})
		case len(a) == 2 && a[0] == "restore-run":
			err = backup.RestoreOnce(ctx, backup.RunConfig{StateDir: *stateDir, Log: log}, a[1])
		case strings.Join(a, " ") == "key show":
			err = showBackupKeys(euid, stdout)
		default:
			err = backupAgentCmd(ctx, agentrpc.NewClient(*socket), fs.Args(), stdout)
			if errors.Is(err, errUsage) {
				fmt.Fprint(stderr, usage)

				return 2
			}
		}
	case "cloudpanel":
		if len(args) > 0 && args[0] == "scan" {
			return cloudpanelCmd(args, stdout, stderr)
		}

		stateDir := fs.String("state-dir", agent.DefaultStateDir, "the agent's state directory")
		if fs.Parse(args) != nil {
			return 2
		}

		a := fs.Args()

		switch {
		case len(a) == 2 && a[0] == "import-run":
			err = cloudpanelImportRun(ctx, *stateDir, a[1], log)
		case len(a) >= 4 && a[0] == "import":
			err = printJSON[agentapi.CloudPanelImport](stdout)(agentapi.CloudPanelImportStart.Call(ctx, agentrpc.NewClient(*socket),
				agentapi.CloudPanelImportRequest{Site: a[1], RemoteUser: a[2], DocRoot: a[3], Databases: dbMaps(a[4:])}))
		case len(a) == 1 && a[0] == "database":
			var c agentapi.CloudPanelDBCredentials

			if c, err = agentapi.CloudPanelSourceDB.Call(ctx, agentrpc.NewClient(*socket), agentapi.CloudPanelDBRequest{}); err == nil {
				fmt.Fprintf(stdout, "-- Run this as root in MariaDB on the old server:\n%s\n", c.SQL)
			}
		case len(a) == 2 && a[0] == "import-status":
			err = printJSON[agentapi.CloudPanelImport](stdout)(agentapi.CloudPanelImportGet.Call(ctx, agentrpc.NewClient(*socket),
				agentapi.CloudPanelImportGetRequest{ID: a[1]}))
		default:
			fmt.Fprint(stderr, usage)

			return 2
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

// showBackupKeys prints what an administrator keeps outside the server.
func showBackupKeys(euid int, w io.Writer) error {
	if euid != 0 {
		return errors.New("must run as root")
	}

	e, err := backup.ExportKeys(backup.ConfigDir, backup.PanelKeyFile)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Keep these values outside this server, for example in a password manager.\n"+
		"Without them the backups cannot be read after the server is lost.\n\n"+
		"repository:      %s\nrestic password: %s\n", e.Repository, e.Password)

	if e.PanelKey != "" {
		fmt.Fprintf(w, "panel key:       %s\n                 (base64 of %s, needed to restore the panel's stored secrets)\n", e.PanelKey, backup.PanelKeyFile)
	}

	return nil
}
