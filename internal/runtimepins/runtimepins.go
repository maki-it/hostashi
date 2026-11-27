// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runtimepins applies newer Node.js, CPython and WP-CLI pins from
// the runtimes data file over the ones built into the binary (ADR-0077
// part 2, ADR-0069 addendum 2).
//
// The file comes from the package hostashi-runtimes in the signed apt suite
// "runtimes"; apt checks its signature, this package only checks what may
// change. A pin from the file wins only when it is a newer release (Python:
// or a newer build) of a line the binary already has, with a checksum for
// every architecture. It never adds a line, never removes one and never
// goes back to an older release, so the file can bring patches but nothing
// new. Anything else in the file is skipped and reported.
package runtimepins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// DefaultPath is where the package hostashi-runtimes puts the file.
const DefaultPath = "/usr/share/hostashi/runtimes.json"

// Schema is the file format this binary reads.
const Schema = 1

// File is the runtimes data file. tools/runtimepins writes it from the pins
// in internal/agentapi on main.
type File struct {
	Schema    int      `json:"schema"`
	Generated string   `json:"generated"` // UTC, RFC 3339
	Node      []Node   `json:"node"`
	Python    []Python `json:"python"`
	// WPCLI is the pinned WP-CLI release; older binaries ignore it.
	WPCLI *WPCLI `json:"wpcli,omitempty"`
}

// WPCLI is the pinned WP-CLI phar.
type WPCLI struct {
	Version string `json:"version"`
	SHA512  string `json:"sha512"`
}

// Node is one pinned Node.js line.
type Node struct {
	Major    string            `json:"major"`
	Version  string            `json:"version"`
	Codename string            `json:"codename"`
	End      string            `json:"end"`
	SHA256   map[string]string `json:"sha256"`
}

// Python is one pinned CPython line.
type Python struct {
	Line    string            `json:"line"`
	Version string            `json:"version"`
	Tag     string            `json:"tag"`
	End     string            `json:"end"`
	SHA256  map[string]string `json:"sha256"`
}

// Result says what Apply changed and which malformed entries it skipped,
// one line each. Older releases and unknown lines are left out silently.
type Result struct {
	Applied []string
	Skipped []string
}

var (
	// ErrSchema: a file format this binary does not know.
	ErrSchema = errors.New("unknown runtimes file schema")
	// ErrUnsafe: the file is not owned by root or others can write it.
	ErrUnsafe = errors.New("runtimes file is not owned by root or is writable by others")
)

var (
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	tagRe     = regexp.MustCompile(`^[0-9]{8}$`)
	dateRe    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	sumRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sum512Re  = regexp.MustCompile(`^[0-9a-f]{128}$`)
)

// nodeArches and pythonArches are the checksums every pin needs.
var (
	nodeArches   = []string{"x64", "arm64"}
	pythonArches = []string{"x86_64", "aarch64"}
)

// Load reads the file at path and applies it to agentapi.NodeReleases and
// agentapi.PythonReleases. A missing file is no error: the built-in pins
// stay. Call it once at start, before anything reads the pins.
func Load(path string) (Result, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, nil
	} else if err != nil {
		return Result{}, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return Result{}, err
	}

	if !safe(fi) {
		return Result{}, fmt.Errorf("%s: %w", path, ErrUnsafe)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return Result{}, err
	}

	node, python, res, err := Apply(buf.Bytes(), agentapi.NodeReleases, agentapi.PythonReleases)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	wp, wres, err := ApplyWPCLI(buf.Bytes(), agentapi.WPCLI)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	agentapi.NodeReleases, agentapi.PythonReleases, agentapi.WPCLI = node, python, wp

	res.Applied = append(res.Applied, wres.Applied...)
	res.Skipped = append(res.Skipped, wres.Skipped...)

	return res, nil
}

// ApplyWPCLI returns cur, or the WP-CLI pin of data when it is a newer
// release with a well-formed SHA-512. It never goes back to an older one.
func ApplyWPCLI(data []byte, cur agentapi.WPCLIRelease) (agentapi.WPCLIRelease, Result, error) {
	var f File

	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&f); err != nil {
		return cur, Result{}, err
	}

	if f.Schema != Schema {
		return cur, Result{}, fmt.Errorf("%w %d", ErrSchema, f.Schema)
	}

	var res Result

	p := f.WPCLI

	switch {
	case p == nil:
	case !versionRe.MatchString(p.Version):
		res.Skipped = append(res.Skipped, "WP-CLI "+p.Version+": not a release")
	case !sum512Re.MatchString(p.SHA512):
		res.Skipped = append(res.Skipped, "WP-CLI "+p.Version+": checksum missing or malformed")
	case compare(p.Version, cur.Version) > 0:
		res.Applied = append(res.Applied, "WP-CLI: "+cur.Version+" -> "+p.Version)
		cur = agentapi.WPCLIRelease{Version: p.Version, SHA512: p.SHA512}
	}

	return cur, res, nil
}

// safe accepts a regular file owned by root that only root can write.
func safe(fi fs.FileInfo) bool {
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o022 != 0 {
		return false
	}

	st, ok := fi.Sys().(*syscall.Stat_t)

	return ok && st.Uid == 0
}

// Apply returns the built-in pins with the newer ones from data put in.
// It does not change node or python.
func Apply(data []byte, node []agentapi.NodeRelease, python []agentapi.PythonRelease) ([]agentapi.NodeRelease, []agentapi.PythonRelease, Result, error) {
	var f File

	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return nil, nil, Result{}, err
	}

	if f.Schema != Schema {
		return nil, nil, Result{}, fmt.Errorf("%w %d", ErrSchema, f.Schema)
	}

	var res Result

	outNode := cloneNode(node)
	for _, p := range f.Node {
		i := slices.IndexFunc(outNode, func(r agentapi.NodeRelease) bool { return r.Major == p.Major })
		cur := agentapi.NodeRelease{}

		if i >= 0 {
			cur = outNode[i]
		}

		why := checkNode(p, cur, i >= 0)
		if why == stale {
			continue
		} else if why != "" {
			res.Skipped = append(res.Skipped, "Node.js "+p.Major+" "+p.Version+": "+why)
			continue
		}

		if compare(p.Version, cur.Version) == 0 {
			continue // the same release
		}

		outNode[i] = agentapi.NodeRelease{Major: p.Major, Version: p.Version, Codename: p.Codename, End: p.End, SHA256: maps.Clone(p.SHA256)}
		res.Applied = append(res.Applied, "Node.js "+p.Major+": "+cur.Version+" -> "+p.Version)
	}

	outPython := clonePython(python)
	for _, p := range f.Python {
		i := slices.IndexFunc(outPython, func(r agentapi.PythonRelease) bool { return r.Line == p.Line })
		cur := agentapi.PythonRelease{}

		if i >= 0 {
			cur = outPython[i]
		}

		why := checkPython(p, cur, i >= 0)
		if why == stale {
			continue
		} else if why != "" {
			res.Skipped = append(res.Skipped, "Python "+p.Line+" "+p.Version+"+"+p.Tag+": "+why)
			continue
		}

		if p.Version == cur.Version && p.Tag == cur.Tag {
			continue
		}

		outPython[i] = agentapi.PythonRelease{Line: p.Line, Version: p.Version, Tag: p.Tag, End: p.End, SHA256: maps.Clone(p.SHA256)}
		res.Applied = append(res.Applied, "Python "+p.Line+": "+cur.Version+"+"+cur.Tag+" -> "+p.Version+"+"+p.Tag)
	}

	return outNode, outPython, res, nil
}

// stale marks a pin that is expected not to apply: an older release than
// the built-in one (the binary is newer than the file) or a line the binary
// does not know (the file is newer than the binary). It is not reported.
const stale = "stale"

// checkNode returns why p cannot replace cur, or "".
func checkNode(p Node, cur agentapi.NodeRelease, known bool) string {
	switch {
	case !known:
		return stale
	case !versionRe.MatchString(p.Version) || !strings.HasPrefix(p.Version, p.Major+"."):
		return "not a release of this line"
	case compare(p.Version, cur.Version) < 0:
		return stale
	case !dateRe.MatchString(p.End):
		return "no end of life"
	case !sums(p.SHA256, nodeArches):
		return "checksums missing or malformed"
	}

	return ""
}

// checkPython returns why p cannot replace cur, or "".
func checkPython(p Python, cur agentapi.PythonRelease, known bool) string {
	switch {
	case !known:
		return stale
	case !versionRe.MatchString(p.Version) || !strings.HasPrefix(p.Version, p.Line+"."):
		return "not a release of this line"
	case !tagRe.MatchString(p.Tag):
		return "not a build tag"
	case compare(p.Version, cur.Version) < 0,
		p.Version == cur.Version && p.Tag < cur.Tag:
		return stale
	case !dateRe.MatchString(p.End):
		return "no end of life"
	case !sums(p.SHA256, pythonArches):
		return "checksums missing or malformed"
	}

	return ""
}

func sums(m map[string]string, arches []string) bool {
	if len(m) != len(arches) {
		return false
	}

	for _, a := range arches {
		if !sumRe.MatchString(m[a]) {
			return false
		}
	}

	return true
}

// compare compares dotted numeric versions; both must match versionRe or
// be empty.
func compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}

		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}

		if x != y {
			if x < y {
				return -1
			}

			return 1
		}
	}

	return 0
}

func cloneNode(in []agentapi.NodeRelease) []agentapi.NodeRelease {
	out := make([]agentapi.NodeRelease, len(in))
	for i, r := range in {
		r.SHA256 = maps.Clone(r.SHA256)
		out[i] = r
	}

	return out
}

func clonePython(in []agentapi.PythonRelease) []agentapi.PythonRelease {
	out := make([]agentapi.PythonRelease, len(in))
	for i, r := range in {
		r.SHA256 = maps.Clone(r.SHA256)
		out[i] = r
	}

	return out
}
