// SPDX-License-Identifier: AGPL-3.0-or-later

package wpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

const phar = "/opt/hostashi/wp-cli/2.12.0/wp-cli.phar"

func job() Job {
	return Job{
		WPInstall: agentapi.WPInstall{
			Locale: "de_DE", URL: "https://blog.example.com", Title: "Mein Blog; $(reboot) `id`", AdminUser: "kim",
			AdminEmail: "kim@example.com", DBName: "blog_wp", DBUser: "blog_wp", DBHost: "localhost", Discourage: true,
		},
		PHP: "8.4", Path: "/home/blog/htdocs", Home: "/home/blog", Domain: "blog.example.com",
	}
}

var secrets = map[string]string{CredDBPass: "db-secret-123456", CredAdminPass: "admin-secret-1234"}

type call struct {
	bin   string
	args  []string
	env   []string
	stdin string
}

func fakeRunner(calls *[]call, failAt int) *Runner {
	return &Runner{Euid: 1001, Phar: phar, Secrets: secrets, start: func(_ context.Context, bin string, args, env []string, stdin io.Reader, _ io.Writer) error {
		b, _ := io.ReadAll(stdin)
		*calls = append(*calls, call{bin, args, env, string(b)})

		if len(*calls) == failAt {
			return errors.New("exit status 1")
		}

		return nil
	}}
}

func TestRun(t *testing.T) {
	var (
		calls []call
		out   bytes.Buffer
	)

	r := fakeRunner(&calls, 0)
	r.Out = &out

	if err := r.Run(t.Context(), job()); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 8 {
		t.Fatalf("%d calls", len(calls))
	}

	for _, c := range calls {
		if c.bin != "/usr/bin/php8.4" || c.args[0] != "-d" || c.args[2] != phar {
			t.Fatalf("call %+v", c)
		}

		// Passwords never reach argv or the environment.
		for _, s := range secrets {
			if strings.Contains(strings.Join(c.args, " ")+strings.Join(c.env, " "), s) {
				t.Fatalf("secret in %+v", c)
			}
		}
	}

	if calls[2].stdin != secrets[CredDBPass]+"\n" || calls[5].stdin != secrets[CredAdminPass]+"\n" || calls[0].stdin != "" {
		t.Fatalf("stdin: %q %q %q", calls[2].stdin, calls[5].stdin, calls[0].stdin)
	}

	// The title is one argument, untouched.
	if !strings.Contains(strings.Join(calls[5].args, "\x00"), "\x00--title=Mein Blog; $(reboot) `id`\x00") {
		t.Fatalf("title: %q", calls[5].args)
	}

	if !strings.HasSuffix(out.String(), DoneMarker+" https://blog.example.com\n") || strings.Contains(out.String(), "kim@example.com") {
		t.Fatalf("log: %s", out.String())
	}

	for _, s := range secrets {
		if strings.Contains(out.String(), s) {
			t.Fatal("secret in the log")
		}
	}
}

func TestRunStopsAtFailure(t *testing.T) {
	var calls []call

	err := fakeRunner(&calls, 2).Run(t.Context(), job())
	if err == nil || len(calls) != 2 || !strings.Contains(err.Error(), "verify-checksums") {
		t.Fatalf("%v after %d calls", err, len(calls))
	}
}

func TestRunRefuses(t *testing.T) {
	for name, f := range map[string]func(r *Runner, j *Job){
		"root":           func(r *Runner, _ *Job) { r.Euid = 0 },
		"phar elsewhere": func(r *Runner, _ *Job) { r.Phar = "/home/blog/wp-cli.phar" },
		"phar escape":    func(r *Runner, _ *Job) { r.Phar = "/opt/hostashi/wp-cli/../../../home/blog/wp-cli.phar" },
		"short password": func(r *Runner, _ *Job) {
			r.Secrets = map[string]string{CredDBPass: "x", CredAdminPass: "admin-secret-1234"}
		},
		"newline password": func(r *Runner, _ *Job) {
			r.Secrets = map[string]string{CredDBPass: "db-secret-123456\nx", CredAdminPass: "admin-secret-1234"}
		},
		"php":           func(_ *Runner, j *Job) { j.PHP = "8.4 -r x" },
		"path outside":  func(_ *Runner, j *Job) { j.Path = "/etc" },
		"path relative": func(_ *Runner, j *Job) { j.Path = "htdocs" },
		"locale":        func(_ *Runner, j *Job) { j.Locale = "xx_XX" },
		"url host":      func(_ *Runner, j *Job) { j.URL = "https://evil.example.com" },
		"url path":      func(_ *Runner, j *Job) { j.URL = "https://blog.example.com/x" },
		"url scheme":    func(_ *Runner, j *Job) { j.URL = "ftp://blog.example.com" },
		"title dash":    func(_ *Runner, j *Job) { j.Title = "--exec=x" },
		"title control": func(_ *Runner, j *Job) { j.Title = "a\nb" },
		"title long":    func(_ *Runner, j *Job) { j.Title = strings.Repeat("ä", agentapi.MaxWPTitle+1) },
		"user":          func(_ *Runner, j *Job) { j.AdminUser = "kim --role" },
		"user dash":     func(_ *Runner, j *Job) { j.AdminUser = "-kim" },
		"user admin":    func(_ *Runner, j *Job) { j.AdminUser = "Admin" },
		"email":         func(_ *Runner, j *Job) { j.AdminEmail = "Kim <kim@example.com>" },
		"email dash":    func(_ *Runner, j *Job) { j.AdminEmail = "-x@example.com" },
		"db name":       func(_ *Runner, j *Job) { j.DBName = "blog;drop" },
		"db host":       func(_ *Runner, j *Job) { j.DBHost = "db.example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call

			r, j := fakeRunner(&calls, 0), job()
			f(r, &j)

			if err := r.Run(t.Context(), j); err == nil || len(calls) != 0 {
				t.Fatalf("ran %d calls, err %v", len(calls), err)
			}
		})
	}
}

func TestMainCommand(t *testing.T) {
	if err := Main(t.Context(), []string{phar}, 0, io.Discard); !errors.Is(err, ErrRoot) {
		t.Fatalf("root: %v", err)
	}

	t.Setenv("CREDENTIALS_DIRECTORY", "")

	if err := Main(t.Context(), []string{phar}, 1001, io.Discard); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("no credentials: %v", err)
	}

	dir := t.TempDir()
	t.Setenv("CREDENTIALS_DIRECTORY", dir)

	if err := os.WriteFile(filepath.Join(dir, CredJob), []byte(`{"locale":"en_US","evil":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Main(t.Context(), []string{phar}, 1001, io.Discard); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field: %v", err)
	}

	b, _ := json.Marshal(job())
	if err := os.WriteFile(filepath.Join(dir, CredJob), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// Missing passwords stop it before anything runs.
	if err := Main(t.Context(), []string{phar}, 1001, io.Discard); err == nil || !strings.Contains(err.Error(), CredDBPass) {
		t.Fatalf("no password: %v", err)
	}

	if err := Main(t.Context(), nil, 1001, io.Discard); err == nil {
		t.Fatal("no phar accepted")
	}
}

// The download is the zip of the locale, never a tar.gz: WP-CLI's
// PharData cuts long paths of a tarball (WordPress 7, php-ai-client).
func TestDownloadIsZipPerLocale(t *testing.T) {
	for _, l := range agentapi.WPLocales {
		u := DownloadURL[l]
		if !strings.HasPrefix(u, "https://") || !strings.HasSuffix(u, ".zip") {
			t.Fatalf("%s: download %q", l, u)
		}

		j := job()
		j.Locale = l

		dl := Steps(j)[0].Args
		if len(dl) != 4 || dl[0] != "core" || dl[1] != "download" || dl[2] != u || dl[3] != "--path="+j.Path {
			t.Fatalf("%s: download step %q", l, dl)
		}

		vc := Steps(j)[1].Args
		if vc[1] != "verify-checksums" || vc[len(vc)-1] != "--locale="+l {
			t.Fatalf("%s: checksums step %q", l, vc)
		}
	}
}
