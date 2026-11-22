// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// apiDatabase is one entry of GET /databases.
type apiDatabase struct {
	Name  string   `json:"name"`
	Site  string   `json:"site"`
	Users []string `json:"users"`
}

// dbCmd runs "db ..." through the panel's API (ADR-0058), so the panel's
// list of databases, roles and audit log apply. Passwords are read from
// stdin; an empty line lets the panel generate one, which is printed once.
func dbCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		var r struct {
			Databases []apiDatabase `json:"databases"`
		}

		if err := apiDo(ctx, http.MethodGet, "/databases", nil, &r); err != nil {
			return err
		}

		for _, d := range r.Databases {
			fmt.Fprintf(out, "%s\tsite=%s\tusers=%s\n", d.Name, d.Site, strings.Join(d.Users, ","))
		}
	case len(args) == 3 && args[0] == "create":
		path := "/sites/" + url.PathEscape(args[1]) + "/databases"
		if err := apiDo(ctx, http.MethodPost, path, map[string]string{"name": args[2]}, nil); err != nil {
			return err
		}

		fmt.Fprintf(out, "created database %s for site %s\n", args[2], args[1])
	case len(args) == 2 && args[0] == "delete":
		if err := apiDo(ctx, http.MethodDelete, "/databases/"+url.PathEscape(args[1]), nil, nil); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted database %s and its users\n", args[1])
	case len(args) == 3 && (args[0] == "import" || args[0] == "export"):
		return dumpCmd(ctx, args[0], args[1], args[2], out)
	case len(args) >= 3 && args[0] == "user":
		return dbUserCmd(ctx, args[1:], out)
	default:
		return errUsage
	}

	return nil
}

func dbUserCmd(ctx context.Context, args []string, out io.Writer) error {
	var r struct {
		Password string `json:"password"`
	}

	switch {
	case len(args) == 3 && args[0] == "create":
		pw, err := readPassword()
		if err != nil {
			return err
		}

		path := "/databases/" + url.PathEscape(args[1]) + "/users"
		if err := apiDo(ctx, http.MethodPost, path, map[string]string{"user": args[2], "password": pw}, &r); err != nil {
			return err
		}

		fmt.Fprintf(out, "created user %s for database %s\n", args[2], args[1])
	case len(args) == 2 && args[0] == "password":
		db, err := databaseOfUser(ctx, args[1])
		if err != nil {
			return err
		}

		pw, err := readPassword()
		if err != nil {
			return err
		}

		path := "/databases/" + url.PathEscape(db) + "/users/" + url.PathEscape(args[1]) + "/password"
		if err := apiDo(ctx, http.MethodPut, path, map[string]string{"password": pw}, &r); err != nil {
			return err
		}

		fmt.Fprintf(out, "changed the password of %s\n", args[1])
	case len(args) == 2 && args[0] == "delete":
		db, err := databaseOfUser(ctx, args[1])
		if err != nil {
			return err
		}

		path := "/databases/" + url.PathEscape(db) + "/users/" + url.PathEscape(args[1])
		if err := apiDo(ctx, http.MethodDelete, path, nil, nil); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted user %s\n", args[1])

		return nil
	default:
		return errUsage
	}

	// The panel answers with the password only when it generated one.
	if r.Password != "" {
		fmt.Fprintf(out, "password: %s\n", r.Password)
	}

	return nil
}

// databaseOfUser finds the database a user belongs to; a database user
// has exactly one.
func databaseOfUser(ctx context.Context, user string) (string, error) {
	var r struct {
		Databases []apiDatabase `json:"databases"`
	}

	if err := apiDo(ctx, http.MethodGet, "/databases", nil, &r); err != nil {
		return "", err
	}

	for _, d := range r.Databases {
		if slices.Contains(d.Users, user) {
			return d.Name, nil
		}
	}

	return "", fmt.Errorf("no database has the user %s", user)
}

// readPassword reads one line from stdin, so a password never shows up in
// the process list or the shell history. An empty line asks the panel to
// generate one.
func readPassword() (string, error) {
	line, err := bufio.NewReader(io.LimitReader(stdin, agentapi.MaxDBPasswordLen+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}

	return strings.TrimRight(line, "\r\n"), nil
}

// jobPoll is how often dumpCmd asks for the state of its job.
var jobPoll = time.Second

// dumpCmd starts an import or export and waits for it. The path is relative
// to the home of the database's site.
func dumpCmd(ctx context.Context, kind, database, path string, out io.Writer) error {
	base := "/databases/" + url.PathEscape(database)

	var j agentapi.DBJob
	if err := apiDo(ctx, http.MethodPost, base+"/"+kind, map[string]string{"path": path}, &j); err != nil {
		return err
	}

	fmt.Fprintf(out, "%s of %s started (job %s)\n", kind, database, j.ID)

	for j.State == agentapi.JobRunning {
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped waiting, the job keeps running: %w", ctx.Err())
		case <-time.After(jobPoll):
		}

		if err := apiDo(ctx, http.MethodGet, base+"/jobs/"+url.PathEscape(j.ID), nil, &j); err != nil {
			return err
		}
	}

	if j.State == agentapi.JobFailed {
		return fmt.Errorf("%s of %s failed: %s", kind, database, j.Error)
	}

	fmt.Fprintf(out, "%s of %s done in %s\n", kind, database, j.Finished.Sub(j.Started).Round(time.Second))

	return nil
}
