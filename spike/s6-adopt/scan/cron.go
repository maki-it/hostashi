package scan

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	cronFieldRe = regexp.MustCompile(`^(\*|[0-9]{1,2}(-[0-9]{1,2})?|[a-zA-Z]{3}(-[a-zA-Z]{3})?)(/[0-9]{1,2})?(,(\*|[0-9]{1,2}(-[0-9]{1,2})?|[a-zA-Z]{3}(-[a-zA-Z]{3})?)(/[0-9]{1,2})?)*$`)
	cronEnvRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*=`)
	homeRefRe   = regexp.MustCompile(`/home/([a-z][a-z0-9_.-]*)`)
)

var cronSpecial = map[string]bool{"@reboot": true, "@yearly": true, "@annually": true, "@monthly": true, "@weekly": true, "@daily": true, "@midnight": true, "@hourly": true}

// parseCrontab parses a user crontab (no user column). Commands stay opaque
// strings: they run as the site user, not as root, and Hostashi does not
// interpret them.
func parseCrontab(src, source, owner string) ([]CronJob, []Finding) {
	var jobs []CronJob
	var fs []Finding
	for i, raw := range strings.Split(src, "\n") {
		ln := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if cronEnvRe.MatchString(line) {
			name, _, _ := strings.Cut(line, "=")
			fs = append(fs, Finding{SevReview, "cron_env", "crontab sets " + strings.TrimSpace(name) + "; Hostashi cron jobs carry no environment yet", source, ln})
			continue
		}
		f := strings.Fields(line)
		var sched string
		var cmdStart int
		if strings.HasPrefix(f[0], "@") {
			if !cronSpecial[f[0]] || len(f) < 2 {
				fs = append(fs, Finding{SevReview, "cron_unparsed", "unknown schedule keyword", source, ln})
				continue
			}
			sched, cmdStart = f[0], 1
		} else {
			if len(f) < 6 {
				fs = append(fs, Finding{SevReview, "cron_unparsed", "fewer than 5 time fields and a command", source, ln})
				continue
			}
			ok := true
			for i, x := range f[:5] {
				if !cronFieldRe.MatchString(x) || !cronInRange(x, cronRanges[i]) {
					ok = false
				}
			}
			if !ok {
				fs = append(fs, Finding{SevReview, "cron_unparsed", "time field not understood", source, ln})
				continue
			}
			sched, cmdStart = strings.Join(f[:5], " "), 5
		}
		cmd := strings.Join(f[cmdStart:], " ")
		// Keep the original spacing of the command.
		if idx := indexAfterFields(line, cmdStart); idx >= 0 {
			cmd = line[idx:]
		}
		if strings.Contains(cmd, "%") {
			fs = append(fs, Finding{SevReview, "cron_percent", "command contains '%', which cron turns into a newline", source, ln})
		}
		for _, m := range homeRefRe.FindAllStringSubmatch(cmd, -1) {
			if m[1] != owner {
				fs = append(fs, Finding{SevReview, "cron_cross_site", "command references another home directory /home/" + m[1], source, ln})
			}
		}
		jobs = append(jobs, CronJob{Schedule: sched, Command: cmd, Line: ln})
	}
	return jobs, fs
}

var cronRanges = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

// cronInRange checks the numbers of a field (not the step values).
func cronInRange(field string, r [2]int) bool {
	for _, part := range strings.Split(field, ",") {
		base, _, _ := strings.Cut(part, "/")
		for _, n := range strings.Split(base, "-") {
			if n == "*" || n == "" || n[0] > '9' { // names like mon are not range-checked
				continue
			}
			v, err := strconv.Atoi(n)
			if err != nil || v < r[0] || v > r[1] {
				return false
			}
		}
	}
	return true
}

func indexAfterFields(line string, n int) int {
	i := 0
	for k := 0; k < n; k++ {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return -1
	}
	return i
}
