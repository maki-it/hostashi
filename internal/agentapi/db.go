// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MaxDatabases bounds the answer of DBList, like MaxSites.
const MaxDatabases = 2000

// Password length bounds for database users. The lower bound still accepts
// passwords of sites migrated from another panel, the upper one keeps the
// statement short.
const (
	MinDBPasswordLen = 12
	MaxDBPasswordLen = 128
)

var (
	// Lowercase only: MariaDB on Linux treats database names as case
	// sensitive file names, and "Shop" next to "shop" would only confuse.
	dbNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// 32 characters is the limit of MySQL clients and of older MariaDB.
	dbUserRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// reservedDatabases belong to the server. The agent's own registry protects
// them too; this list only gives a clear error early.
var reservedDatabases = map[string]bool{
	"mysql": true, "information_schema": true, "performance_schema": true,
	"sys": true, "test": true,
}

// reservedDBUsers are accounts the distribution packages create.
var reservedDBUsers = map[string]bool{
	"root": true, "mysql": true, "mariadb": true, "debian_sys_maint": true,
	"healthcheck": true, "admin": true,
}

// ValidateDBName accepts a database name. The name is put into SQL only
// quoted, but the rule keeps it free of anything that would need escaping.
func ValidateDBName(name string) error {
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("database: must match %s", dbNameRe)
	}

	if reservedDatabases[name] || strings.HasPrefix(name, "hostashi") {
		return fmt.Errorf("database %q is reserved", name)
	}

	return nil
}

// ValidateDBUser accepts a database user name.
func ValidateDBUser(name string) error {
	if !dbUserRe.MatchString(name) {
		return fmt.Errorf("user: must match %s", dbUserRe)
	}

	if reservedDBUsers[name] || strings.HasPrefix(name, "hostashi") || strings.HasPrefix(name, "mariadb") {
		return fmt.Errorf("database user %q is reserved", name)
	}

	return nil
}

// ValidateDBPassword accepts printable ASCII without quotes, backslash and
// backtick. MariaDB takes no placeholder in CREATE USER, so the password is
// a string literal; the rule makes that literal safe without escaping, and
// the password also survives being pasted into wp-config.php or a shell.
func ValidateDBPassword(pw string) error {
	if len(pw) < MinDBPasswordLen || len(pw) > MaxDBPasswordLen {
		return fmt.Errorf("password: must have %d to %d characters", MinDBPasswordLen, MaxDBPasswordLen)
	}

	for i := range len(pw) {
		c := pw[i]
		if c < 0x21 || c > 0x7e || c == '\'' || c == '"' || c == '\\' || c == '`' {
			return errors.New("password: only printable ASCII without spaces, quotes, backslash and backtick")
		}
	}

	return nil
}

// GeneratedDBPasswordLen is the length of GenerateDBPassword's passwords:
// 32 characters from 62 give about 190 bits.
const GeneratedDBPasswordLen = 32

// GenerateDBPassword returns a random password of letters and digits, which
// survives every config file format and shell without quoting.
func GenerateDBPassword() string {
	return randomAlnum(GeneratedDBPasswordLen)
}

func randomAlnum(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	out := make([]byte, 0, n)
	buf := make([]byte, 64)

	for len(out) < n {
		_, _ = rand.Read(buf) // never fails (crypto/rand since Go 1.24)

		for _, b := range buf {
			// 62*4 = 248: rejecting 248..255 keeps the choice uniform.
			if b < 248 && len(out) < n {
				out = append(out, alphabet[b%62])
			}
		}
	}

	return string(out)
}

// DBInfo is what the agent reports about one database.
type DBInfo struct {
	Name  string   `json:"name"`
	Site  string   `json:"site"`
	Users []string `json:"users"`
}

// DBCreateRequest asks for a new, empty database owned by a site.
type DBCreateRequest struct {
	Site string `json:"site"`
	Name string `json:"name"`
}

// Validate checks the request.
func (r DBCreateRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	return ValidateDBName(r.Name)
}

// DBCreate creates the database (utf8mb4) and records it for the site.
var DBCreate = agentrpc.Op[DBCreateRequest, DBInfo]{Name: "db.create"}

// DBDeleteRequest names a database to drop.
type DBDeleteRequest struct {
	Name string `json:"name"`
}

// Validate checks the request.
func (r DBDeleteRequest) Validate() error { return ValidateDBName(r.Name) }

// DBDeleteResponse is empty on success.
type DBDeleteResponse struct{}

// DBDelete drops the database and all its users.
var DBDelete = agentrpc.Op[DBDeleteRequest, DBDeleteResponse]{Name: "db.delete"}

// DBListRequest has no parameters.
type DBListRequest struct{}

// Validate accepts the empty request.
func (DBListRequest) Validate() error { return nil }

// DBListResponse lists the databases the agent manages, sorted by name.
type DBListResponse struct {
	Databases []DBInfo `json:"databases"`
}

// DBList reports the agent's database registry.
var DBList = agentrpc.Op[DBListRequest, DBListResponse]{Name: "db.list"}

// DBUserCreateRequest asks for a user with all privileges on one database.
// The user can log in only from localhost (the Unix socket or 127.0.0.1).
type DBUserCreateRequest struct {
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Validate checks the request.
func (r DBUserCreateRequest) Validate() error {
	if err := ValidateDBName(r.Database); err != nil {
		return err
	}

	if err := ValidateDBUser(r.User); err != nil {
		return err
	}

	return ValidateDBPassword(r.Password)
}

// DBUserResponse is empty on success.
type DBUserResponse struct{}

// DBUserCreate creates the user and grants it the database.
var DBUserCreate = agentrpc.Op[DBUserCreateRequest, DBUserResponse]{Name: "db.user.create"}

// DBUserDeleteRequest names a database user to drop.
type DBUserDeleteRequest struct {
	User string `json:"user"`
}

// Validate checks the request.
func (r DBUserDeleteRequest) Validate() error { return ValidateDBUser(r.User) }

// DBUserDelete drops the user.
var DBUserDelete = agentrpc.Op[DBUserDeleteRequest, DBUserResponse]{Name: "db.user.delete"}

// DBUserPasswordRequest sets a new password.
type DBUserPasswordRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// Validate checks the request.
func (r DBUserPasswordRequest) Validate() error {
	if err := ValidateDBUser(r.User); err != nil {
		return err
	}

	return ValidateDBPassword(r.Password)
}

// DBUserPassword replaces the password of a user.
var DBUserPassword = agentrpc.Op[DBUserPasswordRequest, DBUserResponse]{Name: "db.user.password"}
