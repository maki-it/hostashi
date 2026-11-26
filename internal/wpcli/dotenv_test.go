// SPDX-License-Identifier: AGPL-3.0-or-later

package wpcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestProjectDir(t *testing.T) {
	for web, want := range map[string]string{"": "htdocs", "public": "htdocs", "app/public": "htdocs/app", "web": "htdocs/web"} {
		if got := ProjectDir(web); got != want {
			t.Errorf("%q: %q", web, got)
		}
	}
}

// dotEnvHome is a home with the given files below htdocs.
func dotEnvHome(t *testing.T, fs map[string]string) string {
	t.Helper()

	home := t.TempDir()

	for name, content := range fs {
		p := filepath.Join(home, "htdocs", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	return home
}

func dotEnvJob(home, kind string) DotEnvJob {
	return DotEnvJob{
		Home: home, Dir: "htdocs", Kind: kind, URL: "https://stage.shop.example.com",
		Databases: []agentapi.WPCloneDB{{Source: "shop_app", Target: "shop_stage_app", User: "shop_stage_app"}},
	}
}

var dotEnvPW = map[string]string{"shop_stage_app": "NewSecret12345"}

func TestRunDotEnvLaravel(t *testing.T) {
	home := dotEnvHome(t, map[string]string{
		".env":                       "APP_URL=https://shop.example.com\nDB_DATABASE=shop_app\nDB_USERNAME=shop_app\nDB_PASSWORD=old\nMAIL_HOST=smtp.example.com\n",
		"bootstrap/cache/config.php": "<?php return ['database' => 'shop_app'];",
	})

	var out bytes.Buffer
	if err := (&Runner{Euid: 1001, Out: &out}).RunDotEnv(dotEnvJob(home, agentapi.DotEnvLaravel), dotEnvPW); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(filepath.Join(home, "htdocs", ".env"))
	want := "APP_URL=\"https://stage.shop.example.com\"\nDB_DATABASE=\"shop_stage_app\"\nDB_USERNAME=\"shop_stage_app\"\nDB_PASSWORD=\"NewSecret12345\"\nMAIL_HOST=smtp.example.com\n"

	if string(b) != want {
		t.Errorf(".env:\n%s", b)
	}

	if fi, _ := os.Stat(filepath.Join(home, "htdocs", ".env")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}

	if _, err := os.Stat(filepath.Join(home, "htdocs", laravelConfigCache)); !os.IsNotExist(err) {
		t.Errorf("configuration cache left: %v", err)
	}

	if strings.Contains(out.String(), "NewSecret") || !strings.HasSuffix(out.String(), DotEnvDoneMarker+" https://stage.shop.example.com\n") {
		t.Errorf("log:\n%s", out.String())
	}
}

func TestRunDotEnvLaravelURL(t *testing.T) {
	home := dotEnvHome(t, map[string]string{".env": "DB_URL=\"mysql://shop_app:old@127.0.0.1:3306/shop_app?charset=utf8mb4\"\n"})

	if err := (&Runner{Euid: 1001}).RunDotEnv(dotEnvJob(home, agentapi.DotEnvLaravel), dotEnvPW); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(filepath.Join(home, "htdocs", ".env"))
	if !strings.Contains(string(b), `DB_URL="mysql://shop_stage_app:NewSecret12345@127.0.0.1:3306/shop_stage_app?charset=utf8mb4"`) {
		t.Errorf(".env:\n%s", b)
	}
}

func TestRunDotEnvSymfony(t *testing.T) {
	home := dotEnvHome(t, map[string]string{
		".env":       "APP_ENV=prod\nDATABASE_URL=\"mysql://app:!ChangeMe!@127.0.0.1:3306/app\"\n",
		".env.local": "DATABASE_URL=\"mysql://shop_app:old@localhost/shop_app?serverVersion=10.11.14-MariaDB\"\n",
	})

	if err := (&Runner{Euid: 1001}).RunDotEnv(dotEnvJob(home, agentapi.DotEnvSymfony), dotEnvPW); err != nil {
		t.Fatal(err)
	}

	local, _ := os.ReadFile(filepath.Join(home, "htdocs", ".env.local"))
	if string(local) != "DATABASE_URL=\"mysql://shop_stage_app:NewSecret12345@localhost/shop_stage_app?serverVersion=10.11.14-MariaDB\"\n" {
		t.Errorf(".env.local:\n%s", local)
	}

	// .env stays: .env.local wins.
	if b, _ := os.ReadFile(filepath.Join(home, "htdocs", ".env")); !strings.Contains(string(b), "!ChangeMe!") {
		t.Errorf(".env changed:\n%s", b)
	}
}

func TestRunDotEnvRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		kind string
		fs   map[string]string
		want string
	}{
		"unknown database":  {agentapi.DotEnvLaravel, map[string]string{".env": "DB_DATABASE=other\n"}, "other"},
		"no .env":           {agentapi.DotEnvLaravel, map[string]string{"index.php": ""}, ".env"},
		"compiled env":      {agentapi.DotEnvSymfony, map[string]string{".env": "DATABASE_URL=x\n", ".env.local.php": "<?php"}, "compiled"},
		"no DATABASE_URL":   {agentapi.DotEnvSymfony, map[string]string{".env": "APP_ENV=prod\n"}, "DATABASE_URL"},
		"variable in URL":   {agentapi.DotEnvSymfony, map[string]string{".env": "DATABASE_URL=${DB}\n"}, "variables"},
		"unclosed quote":    {agentapi.DotEnvLaravel, map[string]string{".env": "DB_DATABASE=shop_app\nX=\"open\n"}, "not closed"},
		"source db in URL2": {agentapi.DotEnvSymfony, map[string]string{".env": "DATABASE_URL=mysql://u:p@h/live\n"}, "live"},
	} {
		home := dotEnvHome(t, tc.fs)

		err := (&Runner{Euid: 1001}).RunDotEnv(dotEnvJob(home, tc.kind), dotEnvPW)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A .env that is a link is not followed.
	home := dotEnvHome(t, map[string]string{"real.env": "DB_DATABASE=shop_app\n"})
	if err := os.Symlink("real.env", filepath.Join(home, "htdocs", ".env")); err != nil {
		t.Fatal(err)
	}

	if err := (&Runner{Euid: 1001}).RunDotEnv(dotEnvJob(home, agentapi.DotEnvLaravel), dotEnvPW); err == nil {
		t.Error("followed a link")
	}

	if err := (&Runner{Euid: 0}).RunDotEnv(dotEnvJob(home, agentapi.DotEnvLaravel), dotEnvPW); err == nil {
		t.Error("ran as root")
	}
}
