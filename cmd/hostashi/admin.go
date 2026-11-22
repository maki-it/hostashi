// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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
		return dbCmd(ctx, args, out)
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

// orUnknown names an empty state: the agent could not check the volume.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}

	return s
}
