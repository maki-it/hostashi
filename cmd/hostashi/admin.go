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
	"strconv"
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
		return limitsCmd(ctx, c, args[1], args[2:], out)
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
			fmt.Fprintf(out, "%s\t%s\tsites=%d\n", p.Name, p.Path, p.Sites)
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

// backupAgentCmd runs the backup operations through the agent: set reads
// the settings as JSON and hostkey one public key line from stdin.
func backupAgentCmd(ctx context.Context, c *agentrpc.Client, args []string, out io.Writer) error {
	var (
		v   any
		err error
	)

	switch {
	case len(args) == 3 && args[0] == "restore":
		v, err = agentapi.BackupRestoreStart.Call(ctx, c, agentapi.BackupRestoreRequest{Site: args[1], Snapshot: args[2]})
	case len(args) == 2 && args[0] == "restore-status":
		v, err = agentapi.BackupRestoreGet.Call(ctx, c, agentapi.BackupRestoreGetRequest{ID: args[1]})
	default:
		v, err = backupSimpleCmd(ctx, c, args)
	}

	if err != nil {
		return err
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}

func backupSimpleCmd(ctx context.Context, c *agentrpc.Client, args []string) (any, error) {
	var (
		v   any
		err error
	)

	switch strings.Join(args, " ") {
	case "get":
		v, err = agentapi.BackupGet.Call(ctx, c, agentapi.BackupGetRequest{})
	case "set":
		var s agentapi.BackupSettings

		dec := json.NewDecoder(io.LimitReader(stdin, 1<<16))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("read backup settings from stdin: %w", err)
		}

		v, err = agentapi.BackupSet.Call(ctx, c, agentapi.BackupSetRequest{Settings: s})
	case "hostkey":
		line, rerr := bufio.NewReader(io.LimitReader(stdin, agentapi.MaxHostKey)).ReadString('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, fmt.Errorf("read host key from stdin: %w", rerr)
		}

		v, err = agentapi.BackupHostKey.Call(ctx, c, agentapi.BackupHostKeyRequest{Key: strings.TrimSpace(line)})
	case "start":
		v, err = agentapi.BackupRun.Call(ctx, c, agentapi.BackupRunRequest{})
	case "status":
		v, err = agentapi.BackupStatusGet.Call(ctx, c, agentapi.BackupStatusRequest{})
	default:
		return nil, errUsage
	}

	return v, err
}

// limitsCmd shows a site's limits, or sets them from key=value arguments.
func limitsCmd(ctx context.Context, c *agentrpc.Client, site string, kv []string, out io.Writer) error {
	var (
		l   agentapi.SiteLimits
		err error
	)

	if len(kv) == 0 {
		l, err = agentapi.SiteLimitsGet.Call(ctx, c, agentapi.SiteLimitsGetRequest{User: site})
	} else {
		for _, a := range kv {
			k, v, ok := strings.Cut(a, "=")
			if !ok {
				return errUsage
			}

			n, perr := strconv.ParseUint(v, 10, 64)
			if perr != nil {
				return fmt.Errorf("%s: %w", k, perr)
			}

			switch k {
			case "memory":
				l.MemoryMax = n
			case "tasks":
				l.TasksMax = n
			case "cpu-weight":
				l.CPUWeight = n
			case "cpu-quota":
				l.CPUQuotaPercent = n
			default:
				return errUsage
			}
		}

		l, err = agentapi.SiteLimitsSet.Call(ctx, c, agentapi.SiteLimitsSetRequest{User: site, Limits: l})
	}

	if err != nil {
		return err
	}

	fmt.Fprintf(out, "memory=%d tasks=%d cpu-weight=%d cpu-quota=%d\n", l.MemoryMax, l.TasksMax, l.CPUWeight, l.CPUQuotaPercent)

	return nil
}
