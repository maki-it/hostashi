// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// siteLimits is the part of GET /sites/{name}/limits the CLI shows and
// changes (ADR-0015). 0 means unlimited, except for phpWorkers.
type siteLimits struct {
	Preset string     `json:"preset"`
	Limits limitsBody `json:"limits"`
}

type limitsBody struct {
	DiskBytes           int64  `json:"diskBytes"`
	DiskInodes          int64  `json:"diskInodes"`
	MemoryBytes         int64  `json:"memoryBytes"`
	PHPWorkers          int64  `json:"phpWorkers"`
	Tasks               int64  `json:"tasks"`
	CPUPriority         string `json:"cpuPriority"`
	CPUQuotaPercent     int64  `json:"cpuQuotaPercent"`
	DatabaseBytes       int64  `json:"databaseBytes"`
	DatabaseConnections int64  `json:"databaseConnections"`
}

// limitsCmd shows a site's limits, gives it a preset (preset=NAME), or
// changes single values; the values not named keep what the site has now.
func limitsCmd(ctx context.Context, site string, kv []string, out io.Writer) error {
	path := "/sites/" + url.PathEscape(site) + "/limits"

	var cur siteLimits
	if err := apiDo(ctx, http.MethodGet, path, nil, &cur); err != nil {
		return err
	}

	if len(kv) > 0 {
		in, err := limitsInput(cur.Limits, kv)
		if err != nil {
			return err
		}

		if err := apiDo(ctx, http.MethodPut, path, in, &cur); err != nil {
			return err
		}
	}

	preset, l := cur.Preset, cur.Limits
	if preset == "" {
		preset = "custom"
	}

	fmt.Fprintf(out, "preset=%s memory=%d tasks=%d cpu-priority=%s cpu-quota=%d disk=%d inodes=%d php-workers=%d db-size=%d db-connections=%d\n",
		preset, l.MemoryBytes, l.Tasks, l.CPUPriority, l.CPUQuotaPercent, l.DiskBytes, l.DiskInodes, l.PHPWorkers,
		l.DatabaseBytes, l.DatabaseConnections)

	return nil
}

// limitsInput is the PUT body: a preset alone, or the current values with
// the named ones changed.
func limitsInput(l limitsBody, kv []string) (any, error) {
	if len(kv) == 1 {
		if name, ok := strings.CutPrefix(kv[0], "preset="); ok && name != "" {
			return map[string]string{"preset": name}, nil
		}
	}

	nums := map[string]*int64{
		"memory": &l.MemoryBytes, "tasks": &l.Tasks, "cpu-quota": &l.CPUQuotaPercent,
		"disk": &l.DiskBytes, "inodes": &l.DiskInodes, "php-workers": &l.PHPWorkers,
		"db-size": &l.DatabaseBytes, "db-connections": &l.DatabaseConnections,
	}

	for _, a := range kv {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return nil, errUsage
		}

		if k == "cpu-priority" {
			l.CPUPriority = v

			continue
		}

		p, ok := nums[k]
		if !ok {
			return nil, errUsage
		}

		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}

		*p = n
	}

	return map[string]limitsBody{"custom": l}, nil
}
