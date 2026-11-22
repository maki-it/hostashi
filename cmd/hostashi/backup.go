// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// backupCmd runs the backup commands that go through the panel's API
// (ADR-0028, ADR-0058). set reads the settings as JSON and hostkey one
// public key line from stdin. The answers are the API's JSON.
//
//	backup get|status|set|hostkey|start
//	backup restore SITE SNAPSHOT
//	backup restore-status ID
func backupCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 3 && args[0] == "restore":
		path := "/sites/" + url.PathEscape(args[1]) + "/backups/" + url.PathEscape(args[2]) + "/restore"

		return apiPrint(ctx, out, http.MethodPost, path, nil)
	case len(args) == 2 && args[0] == "restore-status":
		return apiPrint(ctx, out, http.MethodGet, "/backups/restores/"+url.PathEscape(args[1]), nil)
	case len(args) != 1:
		return errUsage
	}

	switch args[0] {
	case "get":
		return apiPrint(ctx, out, http.MethodGet, "/backups", nil)
	case "status":
		var b struct {
			Status json.RawMessage `json:"status"`
		}

		if err := apiDo(ctx, http.MethodGet, "/backups", nil, &b); err != nil {
			return err
		}

		return printIndented(out, b.Status)
	case "set":
		s, err := readJSON(io.LimitReader(stdin, 1<<16))
		if err != nil {
			return fmt.Errorf("read backup settings from stdin: %w", err)
		}

		return apiPrint(ctx, out, http.MethodPut, "/backups/settings", s)
	case "hostkey":
		line, err := bufio.NewReader(io.LimitReader(stdin, agentapi.MaxHostKey)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read host key from stdin: %w", err)
		}

		return apiPrint(ctx, out, http.MethodPut, "/backups/hostkey", map[string]string{"key": strings.TrimSpace(line)})
	case "start":
		if err := apiDo(ctx, http.MethodPost, "/backups/run", nil, nil); err != nil {
			return err
		}

		fmt.Fprintln(out, "backup started")

		return nil
	}

	return errUsage
}

// readJSON reads one JSON value; the panel checks its fields.
func readJSON(r io.Reader) (json.RawMessage, error) {
	var v json.RawMessage
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		return nil, err
	}

	return v, nil
}
