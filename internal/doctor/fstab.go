// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FstabEntry is one active line of /etc/fstab that names a block device.
type FstabEntry struct {
	Line   int
	Source string // as written, e.g. LABEL=web or /dev/sdb1
	Target string // mount point, or "swap"
	// Wait: neither nofail nor noauto. systemd then waits 90 seconds for a
	// missing device, puts the boot into emergency mode and holds every
	// login whose home depends on the mount point.
	Wait bool
}

// ParseFstab returns the active entries that name a block device: a path
// below /dev or a LABEL=, UUID=, PARTUUID= or PARTLABEL= tag. Network file
// systems, bind mounts and pseudo file systems name none and are left out.
func ParseFstab(r io.Reader) []FstabEntry {
	var out []FstabEntry

	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}

		src := unescapeFstab(f[0])
		if !strings.HasPrefix(src, "/dev/") && tagDir(src) == "" {
			continue
		}

		opts := ""
		if len(f) >= 4 {
			opts = f[3]
		}

		wait := true

		for o := range strings.SplitSeq(opts, ",") {
			switch o {
			case "nofail", "noauto":
				wait = false
			case "bind", "rbind":
				src = ""
			}
		}

		if src == "" {
			continue
		}

		out = append(out, FstabEntry{Line: n, Source: f[0], Target: unescapeFstab(f[1]), Wait: wait})
	}

	return out
}

// tagDir is the /dev/disk directory udev keeps the links of a tag in.
func tagDir(src string) string {
	for _, t := range []struct{ tag, dir string }{
		{"LABEL=", "by-label"}, {"UUID=", "by-uuid"}, {"PARTUUID=", "by-partuuid"}, {"PARTLABEL=", "by-partlabel"},
	} {
		if strings.HasPrefix(src, t.tag) && len(src) > len(t.tag) {
			return t.dir
		}
	}

	return ""
}

// devicePaths are the paths at which the device of src would appear below
// devDir. UUIDs are tried in lower case too: udev writes them that way.
func devicePaths(devDir, src string) []string {
	src = unescapeFstab(src)
	if p, found := strings.CutPrefix(src, "/dev/"); found {
		return []string{filepath.Join(devDir, filepath.FromSlash(p))}
	}

	dir := tagDir(src)
	_, val, _ := strings.Cut(src, "=")
	val = strings.Trim(val, `"`)

	paths := []string{filepath.Join(devDir, "disk", dir, udevEscape(val))}
	if dir == "by-uuid" || dir == "by-partuuid" {
		if l := strings.ToLower(val); l != val {
			paths = append(paths, filepath.Join(devDir, "disk", dir, udevEscape(l)))
		}
	}

	return paths
}

// unescapeFstab decodes the octal escapes of fstab(5), such as \040.
func unescapeFstab(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}

	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))

				i += 3

				continue
			}
		}

		b.WriteByte(s[i])
	}

	return b.String()
}

// udevEscape encodes a tag value the way udev names its /dev/disk links:
// every byte outside a small safe set becomes \xNN.
func udevEscape(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.IndexByte("#+-.:=@_", c) >= 0 {
			b.WriteByte(c)

			continue
		}

		fmt.Fprintf(&b, `\x%02x`, c)
	}

	return b.String()
}

func (s System) fstab() string {
	if s.Fstab == "" {
		return "/etc/fstab"
	}

	return s.Fstab
}

func (s System) devDir() string {
	if s.DevDir == "" {
		return "/dev"
	}

	return s.DevDir
}

// FstabDevices checks that every block device /etc/fstab names exists. A line
// left over from a removed or reformatted volume makes systemd wait for it:
// without nofail the boot ends in emergency mode, and logins whose home is
// below the mount point hang for 90 seconds.
func (s System) FstabDevices() Check {
	const id = "system.fstab"

	f, err := os.Open(s.fstab())
	if errors.Is(err, fs.ErrNotExist) {
		return ok(id, s.fstab()+" does not exist")
	}

	if err != nil {
		return warn(id, "cannot read "+s.fstab()+": "+describe(err), "")
	}

	defer f.Close()

	var waiting, optional []string

	for _, e := range ParseFstab(f) {
		if s.deviceExists(e.Source) {
			continue
		}

		what := fmt.Sprintf("%s for %s (line %d)", e.Source, e.Target, e.Line)
		if e.Wait {
			waiting = append(waiting, what)
		} else {
			optional = append(optional, what)
		}
	}

	const fix = " Correct the line or comment it out, then run systemctl daemon-reload. Before formatting a volume, check with lsblk -f and /etc/fstab that nothing uses it."

	switch {
	case len(waiting) > 0:
		return fail(id, s.fstab()+" names a device that does not exist: "+strings.Join(append(waiting, optional...), ", "),
			"Without nofail, the next boot stops in emergency mode, and logins wait 90 seconds for the device."+fix)
	case len(optional) > 0:
		return warn(id, s.fstab()+" names a device that does not exist: "+strings.Join(optional, ", "),
			"The entry has nofail or noauto, so the boot goes on, but anything below the mount point now lands on the root disk."+fix)
	}

	return ok(id, "every device in "+s.fstab()+" exists")
}

func (s System) deviceExists(src string) bool {
	for _, p := range devicePaths(s.devDir(), src) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}

	return false
}
