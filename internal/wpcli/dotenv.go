// SPDX-License-Identifier: AGPL-3.0-or-later

package wpcli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dotenv"
	"github.com/maki-it/hostashi/internal/files"
)

// DotEnvDoneMarker starts the last line of a successful .env step.
const DotEnvDoneMarker = ".env configured:"

// DotEnvJob is the .env step of a cloned Laravel or Symfony site
// (ADR-0070). It starts no process: it edits files below the home as the
// site user, through package files (no link is followed).
type DotEnvJob struct {
	Home string `json:"home"`
	// Dir is the project folder relative to the home, like htdocs.
	Dir       string               `json:"dir"`
	Kind      string               `json:"kind"`
	URL       string               `json:"url"`
	Databases []agentapi.WPCloneDB `json:"databases"`
}

// laravelConfigCache holds Laravel's cached configuration, the live
// site's database settings among it: while it exists, .env is not read.
const laravelConfigCache = "bootstrap/cache/config.php"

// symfonyDumped is Symfony's compiled environment (composer dump-env): it
// replaces the .env files, and it is PHP, not ours to edit.
const symfonyDumped = ".env.local.php"

// ProjectDir is the project folder of a document root below htdocs: the
// parent of a folder called public, else the document root itself.
func ProjectDir(webDir string) string {
	dir := "htdocs"
	if webDir != "" {
		dir += "/" + webDir
	}

	if p, ok := strings.CutSuffix(dir, "/public"); ok {
		return p
	}

	return dir
}

// RunDotEnv runs the .env step. pw holds the passwords of the clone's
// database users.
func (r *Runner) RunDotEnv(j DotEnvJob, pw map[string]string) error {
	if r.Euid == 0 {
		return ErrRoot
	}

	if j.Kind != agentapi.DotEnvLaravel && j.Kind != agentapi.DotEnvSymfony {
		return fmt.Errorf("wpcli: unknown kind %q", j.Kind)
	}

	if _, _, err := agentapi.ParseWPCloneURL(j.URL); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	}

	if err := agentapi.ValidateWPCloneDBs(j.Databases, pw); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	}

	if _, err := files.CleanPath(j.Dir); err != nil || j.Dir == "" {
		return fmt.Errorf("wpcli: invalid project folder %q", j.Dir)
	}

	out := r.Out
	if out == nil {
		out = io.Discard
	}

	root, err := files.Open(j.Home)
	if err != nil {
		return fmt.Errorf("wpcli: %w", err)
	}
	defer root.Close()

	e := dotEnvEdit{root: root, dir: j.Dir, out: out}

	if j.Kind == agentapi.DotEnvLaravel {
		err = e.laravel(j, pw)
	} else {
		err = e.symfony(j, pw)
	}

	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s %s\n", DotEnvDoneMarker, j.URL)

	return nil
}

type dotEnvEdit struct {
	root *files.Root
	dir  string
	out  io.Writer
}

func (e dotEnvEdit) path(name string) string { return e.dir + "/" + name }

func (e dotEnvEdit) exists(name string) (bool, error) {
	_, err := e.root.Stat(e.path(name))
	if errors.Is(err, files.ErrNotFound) {
		return false, nil
	}

	return err == nil, err
}

func (e dotEnvEdit) read(name string) ([]byte, string, error) {
	f, ent, err := e.root.OpenFile(e.path(name))
	if err != nil {
		return nil, "", fmt.Errorf("wpcli: %s: %w", e.path(name), err)
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, files.MaxEditBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("wpcli: %s: %w", e.path(name), err)
	}

	if len(b) > files.MaxEditBytes {
		return nil, "", fmt.Errorf("wpcli: %s is larger than %d bytes", e.path(name), files.MaxEditBytes)
	}

	return b, ent.Version, nil
}

// write sets keys in the file, which must not have changed since it was
// read.
func (e dotEnvEdit) write(name string, content []byte, version string, keys []string, set map[string]string) error {
	b, err := dotenv.Set(content, keys, set)
	if err != nil {
		return fmt.Errorf("wpcli: %s: %w", e.path(name), err)
	}

	if _, err := e.root.WriteFile(e.path(name), strings.NewReader(string(b)), files.WriteOptions{Version: version}); err != nil {
		return fmt.Errorf("wpcli: %s: %w", e.path(name), err)
	}

	fmt.Fprintf(e.out, "%s: set %s\n", e.path(name), strings.Join(keys, ", "))

	return nil
}

func pairOf(dbs []agentapi.WPCloneDB, name string) (agentapi.WPCloneDB, error) {
	for _, d := range dbs {
		if d.Source == name {
			return d, nil
		}
	}

	return agentapi.WPCloneDB{}, fmt.Errorf("wpcli: the database %q of the configuration is not one of the copied ones", name)
}

// rewriteDBURL points a database URL (mysql://user:pass@host/db?...) at
// the clone's database, user and password; host, port and options stay.
func rewriteDBURL(raw string, dbs []agentapi.WPCloneDB, pw map[string]string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !strings.HasPrefix(u.Path, "/") {
		return "", errors.New("wpcli: the database URL could not be read (variables in it are not resolved)")
	}

	d, err := pairOf(dbs, strings.TrimPrefix(u.Path, "/"))
	if err != nil {
		return "", err
	}

	u.User, u.Path, u.RawPath = url.UserPassword(d.User, pw[d.User]), "/"+d.Target, ""

	return u.String(), nil
}

func (e dotEnvEdit) laravel(j DotEnvJob, pw map[string]string) error {
	b, ver, err := e.read(".env")
	if err != nil {
		return err
	}

	keys := []string{"APP_URL"}
	set := map[string]string{"APP_URL": j.URL}

	if raw, ok := dotenv.Get(b, "DB_URL"); ok && raw != "" {
		v, err := rewriteDBURL(raw, j.Databases, pw)
		if err != nil {
			return fmt.Errorf("%w (DB_URL)", err)
		}

		keys, set["DB_URL"] = append(keys, "DB_URL"), v
	} else {
		name, _ := dotenv.Get(b, "DB_DATABASE")

		d, err := pairOf(j.Databases, name)
		if err != nil {
			return err
		}

		keys = append(keys, "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD")
		set["DB_DATABASE"], set["DB_USERNAME"], set["DB_PASSWORD"] = d.Target, d.User, pw[d.User]
	}

	if err := e.write(".env", b, ver, keys, set); err != nil {
		return err
	}

	// The cached configuration still holds the source's database.
	if ok, err := e.exists(laravelConfigCache); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	} else if ok {
		if err := e.root.Delete([]string{e.path(laravelConfigCache)}); err != nil {
			return fmt.Errorf("wpcli: remove the configuration cache: %w", err)
		}

		fmt.Fprintf(e.out, "%s: removed (php artisan config:cache builds it again)\n", e.path(laravelConfigCache))
	}

	return nil
}

func (e dotEnvEdit) symfony(j DotEnvJob, pw map[string]string) error {
	if ok, err := e.exists(symfonyDumped); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	} else if ok {
		return fmt.Errorf("wpcli: %s holds the compiled environment; change DATABASE_URL there by hand or remove it and run composer dump-env again", e.path(symfonyDumped))
	}

	// .env.local wins over .env; the key goes where it is set last.
	name := ".env"

	if ok, err := e.exists(".env.local"); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	} else if ok {
		b, _, err := e.read(".env.local")
		if err != nil {
			return err
		}

		if _, set := dotenv.Get(b, "DATABASE_URL"); set {
			name = ".env.local"
		}
	}

	b, ver, err := e.read(name)
	if err != nil {
		return err
	}

	raw, ok := dotenv.Get(b, "DATABASE_URL")
	if !ok {
		return fmt.Errorf("wpcli: %s sets no DATABASE_URL", e.path(name))
	}

	v, err := rewriteDBURL(raw, j.Databases, pw)
	if err != nil {
		return fmt.Errorf("%w (DATABASE_URL)", err)
	}

	return e.write(name, b, ver, []string{"DATABASE_URL"}, map[string]string{"DATABASE_URL": v})
}
