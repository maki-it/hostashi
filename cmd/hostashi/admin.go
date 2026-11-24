// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	case sub == "site create" || sub == "site delete" || sub == "site list":
		return siteCmd(ctx, args, out)
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
	case sub == "pool add" && (len(args) == 3 || len(args) == 4 && args[1] == "-default"):
		req := agentapi.PoolAddRequest{Name: args[len(args)-2], Path: args[len(args)-1], Default: len(args) == 4}

		p, err := agentapi.PoolAdd.Call(ctx, c, req)
		if err != nil {
			return err
		}

		if req.Default {
			fmt.Fprintf(out, "added pool %s at %s; new sites land on it by default\n", p.Name, p.Path)
		} else {
			fmt.Fprintf(out, "added pool %s at %s; new sites can use it (site create NAME DOMAIN %s)\n", p.Name, p.Path, p.Name)
		}

		if p.Quota != "enforced" {
			fmt.Fprintf(out, "note: disk limits on %s are not enforced (quota %s); see the wiki page Command-line, storage pools\n",
				p.Path, orUnknown(p.Quota))
		}
	case sub == "pool rename" && len(args) == 3:
		// Through the panel's API, so the panel's sites follow the new name
		// (ADR-0016 addendum 2).
		return apiPrint(ctx, out, http.MethodPost, "/pools/"+url.PathEscape(args[1])+"/rename", map[string]string{"name": args[2]})
	case sub == "pool default" && len(args) == 2:
		return apiPrint(ctx, out, http.MethodPost, "/pools/"+url.PathEscape(args[1])+"/default", nil)
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
			mark := ""
			if p.Default {
				mark = "\tdefault"
			}

			fmt.Fprintf(out, "%s\t%s\tsites=%d\tquota=%s%s\n", p.Name, p.Path, p.Sites, orUnknown(p.Quota), mark)
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
