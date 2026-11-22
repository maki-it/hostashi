// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// errUsage makes run print the usage text and exit with 2.
var errUsage = errors.New("usage")

// stdin is replaced in tests.
var stdin io.Reader = os.Stdin

// adminCmd runs the root CLI for site and web operations. The panel normally
// calls these; the CLI is for the operator and the installer tests.
func adminCmd(ctx context.Context, c *agentrpc.Client, cmd string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch sub := cmd + " " + args[0]; {
	case sub == "site create" && (len(args) == 2 || len(args) == 3):
		req := agentapi.SiteCreateRequest{User: args[1]}
		if len(args) == 3 {
			req.Pool = args[2]
		}

		s, err := agentapi.SiteCreate.Call(ctx, c, req)
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "created site %s (uid %d, pool %s)\n", s.User, s.UID, s.Pool)
	case sub == "site delete" && len(args) == 2:
		if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: args[1]}); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted site %s\n", args[1])
	case sub == "site list" && len(args) == 1:
		r, err := agentapi.SiteList.Call(ctx, c, agentapi.SiteListRequest{})
		if err != nil {
			return err
		}

		for _, s := range r.Sites {
			fmt.Fprintf(out, "%s\tuid=%d\tpool=%s\n", s.User, s.UID, s.Pool)
		}
	case sub == "site files" && len(args) == 2:
		r, err := agentapi.FilesOpen.Call(ctx, c, agentapi.FilesOpenRequest{Site: args[1]})
		if err != nil {
			return err
		}

		fmt.Fprintln(out, r.Socket)
	case sub == "site limits" && len(args) >= 2:
		return limitsCmd(ctx, args[1], args[2:], out)
	case sub == "web apply" && len(args) == 1:
		var req agentapi.WebApplyRequest

		dec := json.NewDecoder(io.LimitReader(stdin, 1<<20))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&req); err != nil {
			return fmt.Errorf("read web state from stdin: %w", err)
		}

		r, err := agentapi.WebApply.Call(ctx, c, req)
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "generation %d live (changed: %v)\n", r.Generation, r.Changed)
	case cmd == "db":
		return dbCmd(ctx, c, args, out)
	case sub == "pool add" && len(args) == 3:
		p, err := agentapi.PoolAdd.Call(ctx, c, agentapi.PoolAddRequest{Name: args[1], Path: args[2]})
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "added pool %s at %s; new sites can use it (site create NAME %s)\n", p.Name, p.Path, p.Name)

		if p.Quota != "enforced" {
			fmt.Fprintf(out, "note: disk limits on %s are not enforced (quota %s); see the wiki page Command-line, storage pools\n",
				p.Path, orUnknown(p.Quota))
		}
	case sub == "pool remove" && len(args) == 2:
		if _, err := agentapi.PoolRemove.Call(ctx, c, agentapi.PoolRemoveRequest{Name: args[1]}); err != nil {
			return err
		}

		fmt.Fprintf(out, "removed pool %s; the volume can be unmounted\n", args[1])
	case sub == "pool list" && len(args) == 1:
		r, err := agentapi.PoolList.Call(ctx, c, agentapi.PoolListRequest{})
		if err != nil {
			return err
		}

		for _, p := range r.Pools {
			fmt.Fprintf(out, "%s\t%s\tsites=%d\tquota=%s\n", p.Name, p.Path, p.Sites, orUnknown(p.Quota))
		}
	default:
		return errUsage
	}

	return nil
}

// dbCmd runs "db ..." The panel keeps its own list of databases; use these
// for the installer tests and for repairs, not next to the panel.
func dbCmd(ctx context.Context, c *agentrpc.Client, args []string, out io.Writer) error {
	switch {
	case args[0] == "list" && len(args) == 1:
		r, err := agentapi.DBList.Call(ctx, c, agentapi.DBListRequest{})
		if err != nil {
			return err
		}

		for _, d := range r.Databases {
			fmt.Fprintf(out, "%s\tsite=%s\tusers=%s\n", d.Name, d.Site, strings.Join(d.Users, ","))
		}
	case args[0] == "create" && len(args) == 3:
		if _, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: args[1], Name: args[2]}); err != nil {
			return err
		}

		fmt.Fprintf(out, "created database %s for site %s\n", args[2], args[1])
	case args[0] == "delete" && len(args) == 2:
		if _, err := agentapi.DBDelete.Call(ctx, c, agentapi.DBDeleteRequest{Name: args[1]}); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted database %s and its users\n", args[1])
	case len(args) == 4 && args[0] == "user" && args[1] == "create":
		pw, generated, err := readPassword()
		if err != nil {
			return err
		}

		req := agentapi.DBUserCreateRequest{Database: args[2], User: args[3], Password: pw}
		if _, err := agentapi.DBUserCreate.Call(ctx, c, req); err != nil {
			return err
		}

		fmt.Fprintf(out, "created user %s for database %s\n", args[3], args[2])

		if generated {
			fmt.Fprintf(out, "password: %s\n", pw)
		}
	case len(args) == 3 && args[0] == "user" && args[1] == "password":
		pw, generated, err := readPassword()
		if err != nil {
			return err
		}

		if _, err := agentapi.DBUserPassword.Call(ctx, c, agentapi.DBUserPasswordRequest{User: args[2], Password: pw}); err != nil {
			return err
		}

		fmt.Fprintf(out, "changed the password of %s\n", args[2])

		if generated {
			fmt.Fprintf(out, "password: %s\n", pw)
		}
	case len(args) == 3 && (args[0] == "import" || args[0] == "export"):
		return dumpCmd(ctx, c, args[0], args[1], args[2], out)
	case len(args) == 3 && args[0] == "user" && args[1] == "delete":
		if _, err := agentapi.DBUserDelete.Call(ctx, c, agentapi.DBUserDeleteRequest{User: args[2]}); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted user %s\n", args[2])
	default:
		return errUsage
	}

	return nil
}

// readPassword reads one line from stdin, so a password never shows up in
// the process list or the shell history. An empty line generates one.
func readPassword() (pw string, generated bool, err error) {
	line, err := bufio.NewReader(io.LimitReader(stdin, agentapi.MaxDBPasswordLen+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("read password from stdin: %w", err)
	}

	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return agentapi.GenerateDBPassword(), true, nil
	}

	return line, false, nil
}

// jobPoll is how often dumpCmd asks for the state of its job.
var jobPoll = time.Second

// dumpCmd starts an import or export and waits for it. The path is relative
// to the home of the database's site.
func dumpCmd(ctx context.Context, c *agentrpc.Client, kind, database, path string, out io.Writer) error {
	op := agentapi.DBImport
	if kind == "export" {
		op = agentapi.DBExport
	}

	j, err := op.Call(ctx, c, agentapi.DBDumpRequest{Database: database, Path: path})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s of %s started (job %s)\n", kind, database, j.ID)

	for j.State == agentapi.JobRunning {
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped waiting, the job keeps running: %w", ctx.Err())
		case <-time.After(jobPoll):
		}

		if j, err = agentapi.DBJobStatus.Call(ctx, c, agentapi.DBJobRequest{ID: j.ID}); err != nil {
			return err
		}
	}

	if j.State == agentapi.JobFailed {
		return fmt.Errorf("%s of %s failed: %s", kind, database, j.Error)
	}

	fmt.Fprintf(out, "%s of %s done in %s\n", kind, database, j.Finished.Sub(j.Started).Round(time.Second))

	return nil
}

// orUnknown names an empty state: the agent could not check the volume.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}

	return s
}
