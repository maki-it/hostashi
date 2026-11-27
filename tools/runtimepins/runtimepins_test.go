// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/runtimepins"
)

// The file of the current pins must apply cleanly to the same pins: every
// entry is valid and nothing changes.
func TestEncodeMatchesTheReader(t *testing.T) {
	data, err := Encode(agentapi.NodeReleases, agentapi.PythonReleases, agentapi.WPCLI, time.Date(2026, 11, 27, 5, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	node, python, res, err := runtimepins.Apply(data, agentapi.NodeReleases, agentapi.PythonReleases)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Applied)+len(res.Skipped) != 0 {
		t.Errorf("result %+v", res)
	}

	if !reflect.DeepEqual(node, agentapi.NodeReleases) || !reflect.DeepEqual(python, agentapi.PythonReleases) {
		t.Error("pins changed")
	}

	wp, res, err := runtimepins.ApplyWPCLI(data, agentapi.WPCLI)
	if err != nil || wp != agentapi.WPCLI || len(res.Applied)+len(res.Skipped) != 0 {
		t.Errorf("WP-CLI %+v %+v %v", wp, res, err)
	}
}

// A newer WP-CLI in the file reaches the reader.
func TestEncodeNewerWPCLI(t *testing.T) {
	newer := agentapi.WPCLIRelease{Version: "99.0.0", SHA512: agentapi.WPCLI.SHA512}

	data, err := Encode(nil, nil, newer, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	wp, res, err := runtimepins.ApplyWPCLI(data, agentapi.WPCLI)
	if err != nil || len(res.Applied) != 1 || wp != newer {
		t.Errorf("%+v %+v %v", wp, res, err)
	}
}

// A newer patch in the file reaches the reader.
func TestEncodeNewerPatch(t *testing.T) {
	if len(agentapi.NodeReleases) == 0 {
		t.Skip("no Node.js pins")
	}

	newer := []agentapi.NodeRelease{agentapi.NodeReleases[0]}
	newer[0].Version += "0" // 22.23.5 -> 22.23.50

	data, err := Encode(newer, nil, agentapi.WPCLIRelease{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	node, _, res, err := runtimepins.Apply(data, agentapi.NodeReleases, agentapi.PythonReleases)
	if err != nil || len(res.Applied) != 1 || node[0].Version != newer[0].Version {
		t.Errorf("%+v %v", res, err)
	}
}
