// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSiteSeries(t *testing.T) {
	m := New()
	at := time.Unix(1795600000, 0)

	m.RegisterSites(func(context.Context) ([]Site, error) {
		return []Site{
			{Name: "shop", PHPVersion: "8.4", PHPUp: true, PHPMemoryBytes: 1 << 20, PHPMemoryKnown: true, DiskUsedBytes: 4096, DiskMeasuredAt: at},
			{Name: "down", PHPVersion: "8.3", CacheMB: 64},
			{Name: "cached", PHPVersion: "8.4", CacheMB: 128, CacheUp: true, CacheMemoryBytes: 3 << 20, CacheMemoryKnown: true},
			{Name: "static"},
		}, nil
	}, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	s := string(body)
	for _, want := range []string{
		"hostashi_agent_snapshot_up 1",
		`hostashi_site_php_up{php_version="8.4",site="shop"} 1`,
		`hostashi_site_php_memory_bytes{php_version="8.4",site="shop"} 1.048576e+06`,
		`hostashi_site_php_up{php_version="8.3",site="down"} 0`,
		`hostashi_site_disk_used_bytes{site="shop"} 4096`,
		`hostashi_site_disk_measured_timestamp_seconds{site="shop"} 1.7956e+09`,
		`hostashi_site_object_cache_up{site="cached"} 1`,
		`hostashi_site_object_cache_max_bytes{site="cached"} 1.34217728e+08`,
		`hostashi_site_object_cache_memory_bytes{site="cached"} 3.145728e+06`,
		`hostashi_site_object_cache_up{site="down"} 0`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}

	for _, not := range []string{`site="static"`, `hostashi_site_php_memory_bytes{php_version="8.3"`, `hostashi_site_disk_used_bytes{site="down"}`, `hostashi_site_object_cache_up{site="shop"}`, `hostashi_site_object_cache_memory_bytes{site="down"}`} {
		if strings.Contains(s, not) {
			t.Errorf("unexpected %s", not)
		}
	}
}

func TestSiteSeriesWhenAgentIsDown(t *testing.T) {
	m := New()
	m.RegisterSites(func(context.Context) ([]Site, error) { return nil, errors.New("agent unavailable") }, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(body), "hostashi_agent_snapshot_up 0") || strings.Contains(string(body), "hostashi_site_") {
		t.Fatal(string(body))
	}
}
