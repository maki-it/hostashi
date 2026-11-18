// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MaxDumpPathLen bounds a dump path relative to the site home.
const MaxDumpPathLen = 255

var (
	dumpComponentRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	jobIDRe         = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ValidateDumpPath accepts a relative path below the site home that ends in
// .sql or .sql.gz. Components may not start with a dot or dash, so "..",
// hidden files and option-like names are out.
func ValidateDumpPath(p string) error {
	if p == "" || len(p) > MaxDumpPathLen {
		return fmt.Errorf("path: must have 1 to %d characters", MaxDumpPathLen)
	}

	if !strings.HasSuffix(p, ".sql") && !strings.HasSuffix(p, ".sql.gz") {
		return errors.New("path: must end in .sql or .sql.gz")
	}

	for c := range strings.SplitSeq(p, "/") {
		if !dumpComponentRe.MatchString(c) {
			return fmt.Errorf("path: component %q must match %s", c, dumpComponentRe)
		}
	}

	return nil
}

// Job states.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// DBJob is the state of an import or export.
type DBJob struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // "import" or "export"
	Database string    `json:"database"`
	Path     string    `json:"path"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"` // fixed text, at most a line number and an error code
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

// DBDumpRequest names a database and a dump file relative to its site's home.
type DBDumpRequest struct {
	Database string `json:"database"`
	Path     string `json:"path"`
}

// Validate checks the request.
func (r DBDumpRequest) Validate() error {
	if err := ValidateDBName(r.Database); err != nil {
		return err
	}

	return ValidateDumpPath(r.Path)
}

// DBImport starts reading a dump into the database. The file must belong to
// the site user. It answers at once with the running job.
var DBImport = agentrpc.Op[DBDumpRequest, DBJob]{Name: "db.import"}

// DBExport starts writing the database into a new file, owned by the site
// user. An existing file is never replaced.
var DBExport = agentrpc.Op[DBDumpRequest, DBJob]{Name: "db.export"}

// DBJobRequest asks for one job.
type DBJobRequest struct {
	ID string `json:"id"`
}

// Validate checks the request.
func (r DBJobRequest) Validate() error {
	if !jobIDRe.MatchString(r.ID) {
		return fmt.Errorf("id: must match %s", jobIDRe)
	}

	return nil
}

// DBJobStatus reports a job the agent still remembers.
var DBJobStatus = agentrpc.Op[DBJobRequest, DBJob]{Name: "db.job"}
