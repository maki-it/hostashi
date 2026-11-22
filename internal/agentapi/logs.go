// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// LogKind names one log of a site (ADR-0053). The set is closed; the agent
// derives the path from the kind and the site name.
type LogKind string

// The logs of a site.
const (
	LogAccess LogKind = "access" // nginx access log
	LogError  LogKind = "error"  // nginx error log
	LogPHP    LogKind = "php"    // PHP-FPM error log
	LogApp    LogKind = "app"    // output of the site's app (ADR-0054)
)

// LogKinds lists the kinds in the order the panel shows them.
var LogKinds = []LogKind{LogAccess, LogError, LogPHP, LogApp}

// Limits of site.logs.read. An answer stays well below agentrpc.MaxFrame
// even when JSON escapes every byte.
const (
	MaxLogTailLines = 1000
	// MaxLogLine is the longest line in bytes after JSON escaping; longer
	// lines are cut.
	MaxLogLine = 4 << 10
	// MaxLogAnswer is the most line bytes, after JSON escaping, in one
	// answer.
	MaxLogAnswer = 40 << 10
)

// Valid reports whether k is one of LogKinds.
func (k LogKind) Valid() bool {
	for _, x := range LogKinds {
		if k == x {
			return true
		}
	}

	return false
}

// SiteLogsRead returns lines of one log of a site: the last Lines lines, or
// with After the lines written since an earlier answer (live follow).
var SiteLogsRead = agentrpc.Op[SiteLogsReadRequest, SiteLogsReadResponse]{Name: "site.logs.read"}

// LogCursor is a position in a log file. Inode tells a rotated file from
// the one that was read before.
type LogCursor struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

// SiteLogsReadRequest names the log and what to read.
type SiteLogsReadRequest struct {
	Site string  `json:"site"`
	Kind LogKind `json:"kind"`
	// Lines is the number of last lines, 1..MaxLogTailLines; only without
	// After.
	Lines int `json:"lines,omitempty"`
	// After continues from an earlier answer's Cursor.
	After *LogCursor `json:"after,omitempty"`
}

// Validate checks the request.
func (r SiteLogsReadRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if !r.Kind.Valid() {
		return fmt.Errorf("unknown log %q", r.Kind)
	}

	if r.After != nil {
		if r.Lines != 0 {
			return errors.New("lines and after go not together")
		}

		if r.After.Offset < 0 {
			return errors.New("negative offset")
		}

		return nil
	}

	if r.Lines < 1 || r.Lines > MaxLogTailLines {
		return fmt.Errorf("lines must be 1 to %d", MaxLogTailLines)
	}

	return nil
}

// SiteLogsReadResponse holds whole lines, oldest first, without their line
// breaks. Invalid UTF-8 and control characters other than tab are replaced.
type SiteLogsReadResponse struct {
	Lines []string `json:"lines"`
	// Cursor is where the next read with After continues.
	Cursor LogCursor `json:"cursor"`
	// Rotated is true if the file was rotated or truncated since After;
	// the lines then start at the beginning of the new file.
	Rotated bool `json:"rotated,omitempty"`
	// More is true if more lines are waiting after Cursor than fit in one
	// answer.
	More bool `json:"more,omitempty"`
}

// LogsRetentionSet sets how many days rotated logs are kept (ADR-0053):
// the site logs and the server-wide nginx logs. The panel stores the value
// and sends it again when it starts.
var LogsRetentionSet = agentrpc.Op[LogsRetentionSetRequest, LogsRetentionSetResponse]{Name: "logs.retention.set"}

// The retention range in days (ADR-0053).
const (
	MinLogRetentionDays     = 1
	MaxLogRetentionDays     = 365
	DefaultLogRetentionDays = 7
)

// LogsRetentionSetRequest holds the days.
type LogsRetentionSetRequest struct {
	Days int `json:"days"`
}

// Validate checks the range.
func (r LogsRetentionSetRequest) Validate() error {
	if r.Days < MinLogRetentionDays || r.Days > MaxLogRetentionDays {
		return fmt.Errorf("days must be %d to %d", MinLogRetentionDays, MaxLogRetentionDays)
	}

	return nil
}

// LogsRetentionSetResponse is empty.
type LogsRetentionSetResponse struct{}
