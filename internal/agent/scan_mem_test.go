// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/hoststats"
)

type memHost struct {
	total uint64
	err   error
}

func (m memHost) Read([]hoststats.Mount) (agentapi.HostStats, error) {
	return agentapi.HostStats{Memory: agentapi.HostMemory{TotalBytes: m.total}}, m.err
}

func TestScanMemTotal(t *testing.T) {
	if got := (&siteOps{}).memTotal(); got != 0 {
		t.Errorf("no reader: %d", got)
	}

	if got := (&siteOps{host: memHost{total: 4 << 30}}).memTotal(); got != 4<<30 {
		t.Errorf("4 GiB: %d", got)
	}

	if got := (&siteOps{host: memHost{total: 1, err: errors.New("no /proc")}}).memTotal(); got != 0 {
		t.Errorf("error: %d", got)
	}
}
