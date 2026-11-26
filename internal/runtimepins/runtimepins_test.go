// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimepins

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

var (
	sumA = strings.Repeat("a", 64)
	sumB = strings.Repeat("b", 64)
)

func builtinNode() []agentapi.NodeRelease {
	return []agentapi.NodeRelease{
		{Major: "22", Version: "22.23.5", Codename: "Jod", End: "2027-04-30", SHA256: map[string]string{"x64": sumA, "arm64": sumA}},
		{Major: "24", Version: "24.22.3", Codename: "Krypton", End: "2028-04-30", SHA256: map[string]string{"x64": sumA, "arm64": sumA}},
	}
}

func builtinPython() []agentapi.PythonRelease {
	return []agentapi.PythonRelease{
		{Line: "3.13", Version: "3.13.18", Tag: "20261124", End: "2029-10-31", SHA256: map[string]string{"x86_64": sumA, "aarch64": sumA}},
	}
}

func TestApply(t *testing.T) {
	data := `{"schema":1,"generated":"2026-11-27T05:00:00Z",
	 "node":[
	  {"major":"24","version":"24.22.4","codename":"Krypton","end":"2028-04-30","sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}},
	  {"major":"22","version":"22.23.5","codename":"Jod","end":"2027-04-30","sha256":{"x64":"` + sumA + `","arm64":"` + sumA + `"}},
	  {"major":"28","version":"28.1.0","codename":"X","end":"2031-04-30","sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}}
	 ],
	 "python":[
	  {"line":"3.13","version":"3.13.18","tag":"20261201","end":"2029-10-31","sha256":{"x86_64":"` + sumB + `","aarch64":"` + sumB + `"}}
	 ]}`

	node, python, res, err := Apply([]byte(data), builtinNode(), builtinPython())
	if err != nil {
		t.Fatal(err)
	}

	if node[1].Version != "24.22.4" || node[1].SHA256["x64"] != sumB {
		t.Errorf("Node.js 24 not updated: %+v", node[1])
	}

	if node[0].Version != "22.23.5" || len(node) != 2 {
		t.Errorf("lines changed: %+v", node)
	}

	if python[0].Tag != "20261201" || python[0].SHA256["aarch64"] != sumB {
		t.Errorf("Python 3.13 not updated: %+v", python[0])
	}

	if len(res.Applied) != 2 || len(res.Skipped) != 0 {
		t.Errorf("result %+v", res)
	}
}

func TestApplyDoesNotChangeTheInput(t *testing.T) {
	in := builtinNode()
	data := `{"schema":1,"node":[{"major":"24","version":"24.22.4","end":"2028-04-30","sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}}]}`

	if _, _, _, err := Apply([]byte(data), in, nil); err != nil {
		t.Fatal(err)
	}

	if in[1].Version != "24.22.3" || in[1].SHA256["x64"] != sumA {
		t.Errorf("input changed: %+v", in[1])
	}
}

func TestApplySkips(t *testing.T) {
	sums := `"sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}`
	psums := `"sha256":{"x86_64":"` + sumB + `","aarch64":"` + sumB + `"}`

	for _, tc := range []struct {
		name, entry, want string
	}{
		{"other line", `"node":[{"major":"24","version":"22.23.9","end":"2028-04-30",` + sums + `}]`, "not a release of this line"},
		{"bad version", `"node":[{"major":"24","version":"24.22.4-rc1","end":"2028-04-30",` + sums + `}]`, "not a release of this line"},
		{"missing arch", `"node":[{"major":"24","version":"24.22.4","end":"2028-04-30","sha256":{"x64":"` + sumB + `"}}]`, "checksums"},
		{"extra arch", `"node":[{"major":"24","version":"24.22.4","end":"2028-04-30","sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `","s390x":"` + sumB + `"}}]`, "checksums"},
		{"bad sum", `"node":[{"major":"24","version":"24.22.4","end":"2028-04-30","sha256":{"x64":"XYZ","arm64":"` + sumB + `"}}]`, "checksums"},
		{"no end", `"node":[{"major":"24","version":"24.22.4",` + sums + `}]`, "end of life"},
		{"bad tag", `"python":[{"line":"3.13","version":"3.13.19","tag":"latest","end":"2029-10-31",` + psums + `}]`, "build tag"},
	} {
		node, python, res, err := Apply([]byte(`{"schema":1,`+tc.entry+`}`), builtinNode(), builtinPython())
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}

		if len(res.Applied) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], tc.want) {
			t.Errorf("%s: result %+v", tc.name, res)
		}

		if node[1].Version != "24.22.3" || python[0].Tag != "20261124" {
			t.Errorf("%s: pins changed", tc.name)
		}
	}
}

func TestApplyIgnoresStalePins(t *testing.T) {
	sums := `"sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}`
	psums := `"sha256":{"x86_64":"` + sumB + `","aarch64":"` + sumB + `"}`

	for _, entry := range []string{
		`"node":[{"major":"24","version":"24.22.2","end":"2028-04-30",` + sums + `}]`,
		`"node":[{"major":"28","version":"28.1.0","end":"2031-04-30",` + sums + `}]`,
		`"python":[{"line":"3.16","version":"3.16.0","tag":"20261201","end":"2032-10-31",` + psums + `}]`,
		`"python":[{"line":"3.13","version":"3.13.18","tag":"20261110","end":"2029-10-31",` + psums + `}]`,
		`"python":[{"line":"3.13","version":"3.13.17","tag":"20261201","end":"2029-10-31",` + psums + `}]`,
	} {
		node, python, res, err := Apply([]byte(`{"schema":1,`+entry+`}`), builtinNode(), builtinPython())
		if err != nil || len(res.Applied)+len(res.Skipped) != 0 || len(node) != 2 || node[1].Version != "24.22.3" || python[0].Tag != "20261124" {
			t.Errorf("%s: %+v %v", entry, res, err)
		}
	}
}

func TestApplyRefusesTheFile(t *testing.T) {
	for _, data := range []string{`{"schema":2}`, `{}`, `not json`, `{"schema":1,"node":{}}`} {
		if _, _, _, err := Apply([]byte(data), builtinNode(), builtinPython()); err == nil {
			t.Errorf("%s: accepted", data)
		}
	}

	if _, _, _, err := Apply([]byte(`{"schema":2}`), nil, nil); !errors.Is(err, ErrSchema) {
		t.Errorf("schema 2: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	res, err := Load(filepath.Join(t.TempDir(), "runtimes.json"))
	if err != nil || len(res.Applied)+len(res.Skipped) != 0 {
		t.Errorf("missing file: %+v %v", res, err)
	}
}

func TestLoadRefusesAWritableFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "runtimes.json")
	if err := os.WriteFile(name, []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(name, 0o666); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}

	if _, err := Load(name); !errors.Is(err, ErrUnsafe) {
		t.Errorf("world-writable file: %v", err)
	}
}

func TestLoad(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs a file owned by root")
	}

	saved := agentapi.NodeReleases
	t.Cleanup(func() { agentapi.NodeReleases = saved })

	agentapi.NodeReleases = builtinNode()

	name := filepath.Join(t.TempDir(), "runtimes.json")
	data := `{"schema":1,"node":[{"major":"24","version":"24.22.4","end":"2028-04-30","sha256":{"x64":"` + sumB + `","arm64":"` + sumB + `"}}]}`

	if err := os.WriteFile(name, []byte(data), 0o644); err != nil { //nolint:gosec // world-readable like the real file
		t.Fatal(err)
	}

	res, err := Load(name)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("%+v %v", res, err)
	}

	if r, _ := agentapi.NodeReleaseOf("24"); r.Version != "24.22.4" {
		t.Errorf("pin %+v", r)
	}
}
