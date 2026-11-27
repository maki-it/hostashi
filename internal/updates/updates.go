// SPDX-License-Identifier: AGPL-3.0-or-later

// Package updates tells whether a newer hostashi package is available
// (ADR-0050). It only reads files apt and dpkg keep: the installed version
// from dpkg's status, the versions in the Hostashi repository from the
// package lists that apt fetched last (apt-daily.timer, every day), and
// whether unattended-upgrades installs them (--auto-update). It runs no
// command and reaches no network.
package updates

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Package is the name of the package.
const Package = "hostashi"

// Paths are the files Read reads. The zero value reads the server's.
type Paths struct {
	DpkgStatus string // default /var/lib/dpkg/status
	Sources    string // default /etc/apt/sources.list.d/hostashi.sources
	Lists      string // default /var/lib/apt/lists
	// Unattended is the installer's unattended-upgrades file; default
	// /etc/apt/apt.conf.d/52hostashi-unattended-upgrades. Servers set up
	// before the switch in the panel name the suite stable there.
	Unattended string
	// Auto is the file that lets unattended-upgrades take the suite
	// stable; default AutoFile.
	Auto string
}

// AutoFile adds the suite stable of the Hostashi repository to the origins
// of unattended-upgrades. The installer writes it with --auto-update and
// the panel's switch writes or removes it; it sorts after Unattended, whose
// #clear lines it must follow.
const AutoFile = "/etc/apt/apt.conf.d/53hostashi-auto-update"

// AutoContent is AutoFile's content.
const AutoContent = `// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Managed by Hostashi (ADR-0050): System, Updates, Automatic updates in the
// panel, or the installer option --auto-update. unattended-upgrades also
// installs new Hostashi versions from the suite stable.
Unattended-Upgrade::Origins-Pattern {
	"origin=Hostashi,codename=stable";
};
`

func (p Paths) withDefaults() Paths {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&p.DpkgStatus, "/var/lib/dpkg/status")
	def(&p.Sources, "/etc/apt/sources.list.d/hostashi.sources")
	def(&p.Lists, "/var/lib/apt/lists")
	def(&p.Unattended, "/etc/apt/apt.conf.d/52hostashi-unattended-upgrades")
	def(&p.Auto, AutoFile)

	return p
}

// Status is what Read found.
type Status struct {
	// Installed is the installed version of the package; empty when the
	// server was not set up from the package.
	Installed string `json:"installed"`
	// Available is the newest version in the Hostashi repository's list;
	// empty when apt has no list of it (yet).
	Available string `json:"available"`
	// Update: Available is newer than Installed.
	Update bool `json:"update"`
	// Auto: unattended-upgrades installs new versions (--auto-update).
	Auto bool `json:"auto"`
	// Repository is the repository's address from the apt source.
	Repository string `json:"repository"`
	// ListedAt is when apt last fetched the repository's list; zero
	// without a list.
	ListedAt time.Time `json:"listedAt,omitzero"`
	// Runtimes is the installed version of RuntimesPackage (ADR-0077);
	// empty when it is not installed.
	Runtimes string `json:"runtimes,omitempty"`
	// RuntimesAuto: unattended-upgrades takes the suite runtimes.
	RuntimesAuto bool `json:"runtimesAuto"`
}

// RuntimesPackage brings the pinned Node.js and CPython releases as data
// from the suite runtimes (ADR-0077).
const RuntimesPackage = "hostashi-runtimes"

// Read reads the status. A missing file is no error: it leaves the fields
// it would fill empty.
func Read(p Paths) (Status, error) {
	p = p.withDefaults()

	var st Status

	arch := ""

	err := eachStanza(p.DpkgStatus, func(f map[string]string) bool {
		if f["Package"] == Package && strings.HasSuffix(f["Status"], " installed") {
			st.Installed, arch = f["Version"], f["Architecture"]

			return false
		}

		return true
	})
	if err != nil || st.Installed == "" {
		return st, err
	}

	src, err := source(p.Sources)
	if err != nil || src.uri == "" {
		return st, err
	}

	st.Repository = src.uri

	if st.Runtimes, err = Installed(p.DpkgStatus, RuntimesPackage); err != nil {
		return st, err
	}

	if b, err := os.ReadFile(p.Unattended); err == nil {
		st.RuntimesAuto = strings.Contains(string(b), `"origin=Hostashi,codename=runtimes"`)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return st, err
	}

	for _, f := range []string{p.Auto, p.Unattended} {
		on, err := auto(f)
		if err != nil {
			return st, err
		}

		st.Auto = st.Auto || on
	}

	for _, a := range []string{arch, "all"} {
		if a == "" {
			continue
		}

		list := filepath.Join(p.Lists, listName(src.uri, src.suite, src.component, a))

		fi, err := os.Stat(list)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return st, err
		}

		err = eachStanza(list, func(f map[string]string) bool {
			if f["Package"] == Package && (st.Available == "" || Compare(f["Version"], st.Available) > 0) {
				st.Available = f["Version"]
			}

			return true
		})
		if err != nil {
			return st, err
		}

		if fi.ModTime().After(st.ListedAt) {
			st.ListedAt = fi.ModTime().UTC()
		}
	}

	st.Update = st.Available != "" && Compare(st.Available, st.Installed) > 0

	return st, nil
}

type aptSource struct{ uri, suite, component string }

// source reads the first URI, suite and component of the deb822 source
// the install script writes.
func source(path string) (aptSource, error) {
	var s aptSource

	err := eachStanza(path, func(f map[string]string) bool {
		if s.uri != "" {
			return false
		}

		first := func(v string) string { w, _, _ := strings.Cut(strings.TrimSpace(v), " "); return w }
		if u := first(f["URIs"]); u != "" {
			s = aptSource{uri: strings.TrimSuffix(u, "/"), suite: first(f["Suites"]), component: first(f["Components"])}
		}

		return true
	})

	if s.suite == "" || s.component == "" {
		s = aptSource{}
	}

	return s, err
}

// auto reports whether the unattended-upgrades file lets the suite stable
// of the Hostashi repository in (packaging/install.sh, uu_origins). The
// suite runtimes (ADR-0077) does not count: it brings no Hostashi code.
func auto(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	return strings.Contains(string(b), `"origin=Hostashi,codename=stable"`), err
}

// listName is the file name apt gives a repository's package list
// (URItoFileName: no scheme, no login, some characters quoted, / as _).
func listName(uri, suite, component, arch string) string {
	if i := strings.Index(uri, "://"); i >= 0 {
		uri = uri[i+3:]
	}

	if at := strings.IndexByte(uri, '@'); at >= 0 && at < strings.IndexByte(uri+"/", '/') {
		uri = uri[at+1:]
	}

	u := uri + "/dists/" + suite + "/" + component + "/binary-" + arch + "/Packages"

	var b strings.Builder

	for i := range len(u) {
		c := u[i]

		switch {
		case c == '/':
			_ = b.WriteByte('_')
		case strings.IndexByte("\\|{}[]<>\"^~_=!@#$%^&*", c) >= 0 || c <= ' ' || c >= 0x7f:
			fmt.Fprintf(&b, "%%%02x", c)
		default:
			_ = b.WriteByte(c)
		}
	}

	return b.String()
}

// Installed returns the installed version of pkg from the dpkg status
// file (default /var/lib/dpkg/status); empty when it is not installed.
func Installed(dpkgStatus, pkg string) (string, error) {
	if dpkgStatus == "" {
		dpkgStatus = "/var/lib/dpkg/status"
	}

	v := ""
	err := eachStanza(dpkgStatus, func(f map[string]string) bool {
		if f["Package"] == pkg && strings.HasSuffix(f["Status"], " installed") {
			v = f["Version"]

			return false
		}

		return true
	})

	return v, err
}

// eachStanza calls fn with the fields of each deb822 stanza in the file
// until fn returns false. Continuation lines are joined with a space. A
// missing file has no stanzas.
func eachStanza(path string, fn func(map[string]string) bool) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()

	return stanzas(f, fn)
}

// maxLine bounds one line of a list; a description longer than that is
// not ours anyway.
const maxLine = 1 << 20

func stanzas(r io.Reader, fn func(map[string]string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)

	cur := map[string]string{}
	last := ""

	flush := func() bool {
		if len(cur) == 0 {
			return true
		}

		more := fn(cur)
		cur, last = map[string]string{}, ""

		return more
	}

	for sc.Scan() {
		line := sc.Text()

		switch {
		case strings.TrimSpace(line) == "":
			if !flush() {
				return nil
			}
		case strings.HasPrefix(line, "#"):
		case line[0] == ' ' || line[0] == '\t':
			if last != "" {
				cur[last] += " " + strings.TrimSpace(line)
			}
		default:
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}

			last = k
			cur[k] = strings.TrimSpace(v)
		}
	}

	if err := sc.Err(); err != nil {
		return err
	}

	flush()

	return nil
}
