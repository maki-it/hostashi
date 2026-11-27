// SPDX-License-Identifier: AGPL-3.0-or-later

package wpcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func cloneJob() CloneJob {
	return CloneJob{
		PHP: "8.4", Path: "/home/shop-stage/htdocs", Home: "/home/shop-stage", URL: "https://stage.shop.example.com",
		From: []string{"shop.example.com", "www.shop.example.com"},
		Databases: []agentapi.WPCloneDB{
			{Source: "legacy", Target: "shop_stage_legacy", User: "shop_stage_legacy"},
			{Source: "shop_wp", Target: "shop_stage_wp", User: "shop_stage_wp"},
		},
		Staging: true,
	}
}

var clonePW = map[string]string{"shop_stage_wp": "wp-secret-123456", "shop_stage_legacy": "legacy-secret-1234"}

func TestReplacements(t *testing.T) {
	got := Replacements("https", "stage.shop.example.com", []string{"shop.example.com", "www.shop.example.com", "stage.shop.example.com"})
	want := []Replacement{
		{"//shop.example.com", "//stage.shop.example.com"},
		{`\/\/shop.example.com`, `\/\/stage.shop.example.com`},
		{"//www.shop.example.com", "//stage.shop.example.com"},
		{`\/\/www.shop.example.com`, `\/\/stage.shop.example.com`},
		{"//www.stage.shop.example.com", "//stage.shop.example.com"},
		{`\/\/www.stage.shop.example.com`, `\/\/stage.shop.example.com`},
		{"http://stage.shop.example.com", "https://stage.shop.example.com"},
		{`http:\/\/stage.shop.example.com`, `https:\/\/stage.shop.example.com`},
	}

	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}

	// A clone without a certificate goes to http.
	h := Replacements("http", "b.example.com", []string{"a.example.com"})
	if last := h[len(h)-2]; last != (Replacement{"https://b.example.com", "http://b.example.com"}) {
		t.Errorf("got %q", last)
	}
}

func TestRunClone(t *testing.T) {
	var (
		calls []call
		out   bytes.Buffer
	)

	r := &Runner{Euid: 1001, Phar: phar, Out: &out, start: func(_ context.Context, bin string, args, env []string, stdin io.Reader, w io.Writer) error {
		b, _ := io.ReadAll(stdin)
		calls = append(calls, call{bin, args, env, string(b)})

		if slices.Contains(args, "get") {
			_, _ = io.WriteString(w, "shop_wp\n")
		}

		if slices.Contains(args, "DB_PASSWORD") {
			// WP-CLI may echo what it read.
			_, _ = io.WriteString(w, "Success: Updated the constant 'DB_PASSWORD' with value '"+string(b)+"'.\n")
		}

		return nil
	}}

	if err := r.RunClone(t.Context(), cloneJob(), clonePW); err != nil {
		t.Fatal(err)
	}

	var cmds []string

	for _, c := range calls {
		if c.bin != "/usr/bin/php8.4" || c.args[2] != phar {
			t.Fatalf("call %+v", c)
		}

		cmd := strings.Join(c.args[3:], " ")
		cmds = append(cmds, cmd)

		if strings.Contains(cmd, "secret") {
			t.Errorf("a password in argv: %s", cmd)
		}
	}

	for _, want := range []string{
		"config get DB_NAME --path=/home/shop-stage/htdocs",
		"config set DB_NAME shop_stage_wp --type=constant --path=/home/shop-stage/htdocs",
		"config set DB_USER shop_stage_wp --type=constant --path=/home/shop-stage/htdocs",
		"config set WP_ENVIRONMENT_TYPE staging --type=constant --path=/home/shop-stage/htdocs",
		"search-replace //shop.example.com //stage.shop.example.com --path=/home/shop-stage/htdocs --all-tables-with-prefix --skip-columns=guid --skip-plugins --skip-themes --report-changed-only",
		"cache flush --path=/home/shop-stage/htdocs --skip-plugins --skip-themes",
	} {
		if !slices.Contains(cmds, want) {
			t.Errorf("no call %q in\n%s", want, strings.Join(cmds, "\n"))
		}
	}

	if calls[3].stdin != "wp-secret-123456\n" || !slices.Contains(calls[3].args, "eval") || !slices.Contains(calls[3].args, "--skip-wordpress") {
		t.Errorf("password call %+v", calls[3])
	}

	if strings.Contains(out.String(), "secret") || !strings.HasSuffix(out.String(), "wordpress configured: https://stage.shop.example.com\n") {
		t.Errorf("log:\n%s", out.String())
	}
}

func TestRunCloneRefuses(t *testing.T) {
	start := func(_ context.Context, _ string, args, _ []string, _ io.Reader, w io.Writer) error {
		if slices.Contains(args, "get") {
			_, _ = io.WriteString(w, "other_db\n")
		}

		return nil
	}

	r := &Runner{Euid: 1001, Phar: phar, start: start}
	if err := r.RunClone(t.Context(), cloneJob(), clonePW); err == nil || !strings.Contains(err.Error(), "other_db") {
		t.Errorf("unknown DB_NAME: %v", err)
	}

	if err := (&Runner{Euid: 0, Phar: phar, start: start}).RunClone(t.Context(), cloneJob(), clonePW); !errors.Is(err, ErrRoot) {
		t.Errorf("root: %v", err)
	}

	for name, f := range map[string]func(*CloneJob, map[string]string){
		"no password":      func(_ *CloneJob, pw map[string]string) { delete(pw, "shop_stage_wp") },
		"short password":   func(_ *CloneJob, pw map[string]string) { pw["shop_stage_wp"] = "short" },
		"path outside":     func(j *CloneJob, _ map[string]string) { j.Path = "/srv/other" },
		"url with path":    func(j *CloneJob, _ map[string]string) { j.URL = "https://stage.shop.example.com/x" },
		"ftp url":          func(j *CloneJob, _ map[string]string) { j.URL = "ftp://stage.shop.example.com" },
		"source is clone":  func(j *CloneJob, _ map[string]string) { j.From = []string{"stage.shop.example.com"} },
		"no source":        func(j *CloneJob, _ map[string]string) { j.From = nil },
		"bad domain":       func(j *CloneJob, _ map[string]string) { j.From = []string{"-x.example.com"} },
		"bad database":     func(j *CloneJob, _ map[string]string) { j.Databases[0].Target = "x;drop" },
		"twice the source": func(j *CloneJob, _ map[string]string) { j.Databases[0].Source = "shop_wp" },
	} {
		j := cloneJob()
		j.Databases = slices.Clone(j.Databases)

		pw := map[string]string{}
		for k, v := range clonePW {
			pw[k] = v
		}

		f(&j, pw)

		var called bool

		r := &Runner{Euid: 1001, Phar: phar, start: func(context.Context, string, []string, []string, io.Reader, io.Writer) error {
			called = true

			return nil
		}}

		if err := r.RunClone(t.Context(), j, pw); err == nil || called {
			t.Errorf("%s: err %v, called %v", name, err, called)
		}
	}
}
