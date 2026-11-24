// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSiteUsageSeries(t *testing.T) {
	m := New()
	m.RegisterSiteUsage(func(context.Context) ([]SiteUsage, error) {
		return []SiteUsage{
			{Name: "shop", CPUUsec: 2_500_000, MemoryBytes: 1 << 20, MemoryKnown: true, Tasks: 7, IOReadBytes: 10, IOWriteBytes: 20, IOKnown: true, HTTPRequests: 3, HTTPBytesIn: 100, HTTPBytesOut: 2000},
			{Name: "docs"},
		}, nil
	}, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	s := string(body)
	for _, want := range []string{
		"hostashi_agent_site_usage_up 1",
		`hostashi_site_cpu_seconds_total{site="shop"} 2.5`,
		`hostashi_site_memory_bytes{site="shop"} 1.048576e+06`,
		`hostashi_site_tasks{site="shop"} 7`,
		`hostashi_site_io_bytes_total{direction="read",site="shop"} 10`,
		`hostashi_site_io_bytes_total{direction="write",site="shop"} 20`,
		`hostashi_site_http_bytes_total{direction="in",site="shop"} 100`,
		`hostashi_site_http_bytes_total{direction="out",site="shop"} 2000`,
		`hostashi_site_http_requests_total{site="shop"} 3`,
		`hostashi_site_cpu_seconds_total{site="docs"} 0`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}

	for _, not := range []string{`hostashi_site_memory_bytes{site="docs"}`, `hostashi_site_io_bytes_total{direction="read",site="docs"}`} {
		if strings.Contains(s, not) {
			t.Errorf("unexpected %s", not)
		}
	}
}

func TestSiteUsageWhenAgentIsDown(t *testing.T) {
	m := New()
	m.RegisterSiteUsage(func(context.Context) ([]SiteUsage, error) { return nil, errors.New("agent unavailable") }, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	if s := string(body); !strings.Contains(s, "hostashi_agent_site_usage_up 0") || strings.Contains(s, "hostashi_site_cpu_seconds_total") {
		t.Fatalf("%s", s)
	}
}
