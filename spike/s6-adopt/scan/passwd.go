package scan

import (
	"strconv"
	"strings"
)

type passwdEntry struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
	Line  int
}

// parsePasswd parses /etc/passwd. Malformed lines are reported, not fatal.
func parsePasswd(src string) (map[string]passwdEntry, []Finding) {
	out := map[string]passwdEntry{}
	var fs []Finding
	for i, line := range strings.Split(src, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) != 7 {
			fs = append(fs, Finding{SevReview, "passwd_malformed", "line does not have 7 fields", "/etc/passwd", i + 1})
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil || uid < 0 || gid < 0 {
			fs = append(fs, Finding{SevReview, "passwd_malformed", "uid or gid is not a number", "/etc/passwd", i + 1})
			continue
		}
		if _, dup := out[f[0]]; dup {
			fs = append(fs, Finding{SevReview, "passwd_duplicate", "duplicate user " + f[0], "/etc/passwd", i + 1})
			continue
		}
		out[f[0]] = passwdEntry{Name: f[0], UID: uid, GID: gid, Home: f[5], Shell: f[6], Line: i + 1}
	}
	return out, fs
}
