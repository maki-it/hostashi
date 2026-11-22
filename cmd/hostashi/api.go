// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"text/tabwriter"

	"github.com/maki-it/hostashi/api"
	"github.com/maki-it/hostashi/internal/apicli"
	"github.com/maki-it/hostashi/internal/apiclient"
)

// apiInput is where "hostashi api" reads bodies and files; tests replace it.
var apiInput = apicli.OSInput

// apiCommand runs "hostashi api [TAG [VERB [ARGS…]]]": every operation of
// the embedded OpenAPI document is a command (ADR-0058).
func apiCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	sp, err := apicli.Load(api.Spec)
	if err != nil {
		return exitCode("api", err, stderr)
	}

	help := len(args) > 0 && (args[len(args)-1] == "-h" || args[len(args)-1] == "--help" || args[len(args)-1] == "help")
	if help {
		args = args[:len(args)-1]
	}

	switch {
	case len(args) == 0:
		listTags(sp, stdout)

		return 0
	case len(args) == 1:
		ops := sp.InTag(args[0])
		if len(ops) == 0 {
			fmt.Fprintf(stderr, "hostashi api: unknown area %q; hostashi api lists them\n", args[0])

			return 2
		}

		listOps(ops, stdout)

		return 0
	}

	op := sp.Find(args[0], args[1])
	if op == nil {
		fmt.Fprintf(stderr, "hostashi api: unknown command %q in %q; hostashi api %s lists them\n", args[1], args[0], args[0])

		return 2
	}

	if help {
		opHelp(op, stdout)

		return 0
	}

	req, err := op.Build(args[2:], apiInput())
	if err != nil {
		fmt.Fprintf(stderr, "hostashi api %s %s: %v\nusage: hostashi api %s\n", op.Tag, op.Verb, err, op.Usage())

		return 2
	}

	return exitCode("api", callAPI(ctx, req, stdout), stderr)
}

func callAPI(ctx context.Context, req apicli.Request, stdout io.Writer) error {
	c, err := apiClient()
	if err != nil {
		return err
	}

	res, err := c.Send(ctx, req.Method, req.Path, req.ContentType, req.Body)
	if err != nil {
		var e *apiclient.Error
		if errors.As(err, &e) && e.Code == "unauthenticated" {
			return fmt.Errorf("%w; run hostashi login", err)
		}

		return err
	}

	defer func() { _ = res.Body.Close() }()

	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if mt != "application/json" {
		// Downloads (zip, CSV, file content, NDJSON) go out as they are.
		_, err = io.Copy(stdout, res.Body)

		return err
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") != nil {
		_, err = stdout.Write(raw)

		return err
	}

	_ = out.WriteByte('\n')
	_, err = out.WriteTo(stdout)

	return err
}

func listTags(sp *apicli.Spec, w io.Writer) {
	fmt.Fprint(w, "usage: hostashi api AREA COMMAND [ARGS…]\n\n"+
		"Every operation of the panel's API, with your login (hostashi login).\n"+
		"hostashi api AREA lists the commands of an area, and\n"+
		"hostashi api AREA COMMAND -h explains one.\n\nareas:\n")

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, t := range sp.Tags() {
		fmt.Fprintf(tw, "  %s\t%d commands\n", t, len(sp.InTag(t)))
	}

	_ = tw.Flush()
}

func listOps(ops []*apicli.Op, w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	for _, op := range ops {
		note := ""
		if op.SessionOnly {
			note = " (panel only)"
		}

		fmt.Fprintf(tw, "  %s\t%s%s\n", strings.TrimPrefix(op.Usage(), op.Tag+" "), op.Summary, note)
	}

	_ = tw.Flush()
}

func opHelp(op *apicli.Op, w io.Writer) {
	fmt.Fprintf(w, "usage: hostashi api %s\n\n%s.\n\n%s /api/v1%s (operation %s)\n", op.Usage(), op.Summary, op.Method, strings.TrimPrefix(op.Path, "/api/v1"), op.ID)

	switch {
	case op.Public:
		fmt.Fprintln(w, "Needs no login.")
	case op.SessionOnly:
		fmt.Fprintln(w, "Only in the panel: API tokens cannot use it.")
	default:
		fmt.Fprintf(w, "Token scope: %s\n", op.Scope)
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	if len(op.Query) > 0 {
		fmt.Fprintln(tw, "\nparameters:")

		for _, q := range op.Query {
			req := ""
			if q.Required {
				req = ", required"
			}

			fmt.Fprintf(tw, "  %s=\t%s%s\t%s\n", q.Name, q.Type, req, q.Description)
		}
	}

	if len(op.Fields) > 0 {
		fmt.Fprintln(tw, "\nbody fields (key=value, key:=JSON, file=@PATH):")

		for _, f := range op.Fields {
			t := f.Type
			if t == "" {
				t = "JSON"
			}

			fmt.Fprintf(tw, "  %s\t%s\n", f.Name, t)
		}
	} else if op.BodyType == "application/json" {
		fmt.Fprintln(tw, "\nbody: -d JSON, -d @FILE or -d - (stdin)")
	}

	_ = tw.Flush()
}
