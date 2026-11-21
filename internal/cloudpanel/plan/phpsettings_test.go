// SPDX-License-Identifier: AGPL-3.0-or-later

package plan_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

const mib = 1 << 20

func withPool(settings map[string]string) scan.Site {
	s := base()
	s.Pool.Settings = settings

	return s
}

func TestPHPSettingsOfTakesAllowlist(t *testing.T) {
	pi := plan.PHPSettingsOf(withPool(map[string]string{
		"php_admin_value[memory_limit]":           "512M",
		"php_value[memory_limit]":                 "128M", // admin wins
		"php_admin_value[upload_max_filesize]":    "256M",
		"php_admin_value[post_max_size]":          "64M", // below the upload: raised
		"php_admin_value[max_execution_time]":     "300",
		"php_value[max_input_vars]":               "10000",
		"php_value[date.timezone]":                "Europe/Berlin",
		"php_admin_flag[display_errors]":          "on",
		"php_value[error_reporting]":              "E_ALL & ~E_DEPRECATED",
		"php_admin_value[session.gc_maxlifetime]": "1440", // the default: left out
		"php_admin_value[open_basedir]":           "/home/shop/:/tmp/",
		"php_admin_value[opcache.enable]":         "1",
		"php_value[output_buffering]":             "4096",
	}))

	on := true

	want := agentapi.PHPSettings{
		MemoryLimit: 512 * mib, UploadMaxFilesize: 256 * mib, PostMaxSize: 256 * mib, MaxExecutionTime: 300,
		MaxInputVars: 10000, Timezone: "Europe/Berlin", DisplayErrors: &on,
	}
	if got := pi.Settings; got.MemoryLimit != want.MemoryLimit || got.UploadMaxFilesize != want.UploadMaxFilesize ||
		got.Effective().PostMaxSize != want.PostMaxSize || got.MaxExecutionTime != want.MaxExecutionTime || got.MaxInputVars != want.MaxInputVars ||
		got.Timezone != want.Timezone || got.DisplayErrors == nil || !*got.DisplayErrors || got.ErrorReporting != "" || got.SessionGCMaxLifetime != 0 {
		t.Fatalf("settings %+v", got)
	}

	if err := pi.Settings.Validate(); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(pi.NotTaken, []string{"php_admin_value[opcache.enable]", "php_value[output_buffering]"}) {
		t.Errorf("not taken %v (open_basedir is Hostashi's own)", pi.NotTaken)
	}

	if len(pi.Changed) != 1 || !strings.HasPrefix(pi.Changed[0], "post_max_size: 64M -> 256M") {
		t.Errorf("changed %v", pi.Changed)
	}
}

func TestPHPSettingsOfClamps(t *testing.T) {
	pi := plan.PHPSettingsOf(withPool(map[string]string{
		"php_admin_value[memory_limit]":        "-1",
		"php_admin_value[upload_max_filesize]": "100K",
		"php_admin_value[post_max_size]":       "8G",
		"php_admin_value[max_execution_time]":  "0",
		"php_admin_value[max_input_time]":      "-1",
		"php_admin_value[max_input_vars]":      "abc",
		"php_admin_value[date.timezone]":       "Mars/Olympus",
		"php_admin_value[error_reporting]":     "E_ALL & ~E_NOTICE & ~E_STRICT",
		"php_admin_flag[short_open_tag]":       "maybe",
		"php_value[max_file_uploads]":          "9999",
	}))

	s := pi.Settings
	if s.MemoryLimit != agentapi.MaxPHPMemoryLimit || s.UploadMaxFilesize != agentapi.MinPHPUploadSize ||
		s.PostMaxSize != agentapi.MaxPHPUploadSize || s.MaxExecutionTime != agentapi.MaxPHPTime ||
		s.MaxInputTime != 0 || s.MaxInputVars != 0 || s.Timezone != "" || s.ErrorReporting != "" || s.ShortOpenTag != nil ||
		s.MaxFileUploads != agentapi.MaxPHPFileUploads {
		t.Fatalf("settings %+v", s)
	}

	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}

	// production is the default, so the mapped preset is left out.
	if len(pi.Changed) != 10 {
		t.Errorf("changed %d: %v", len(pi.Changed), pi.Changed)
	}
}

func TestBuildPHPSettingsReasons(t *testing.T) {
	target := plan.Target{PHP: []string{"8.4"}}

	clean := withPool(map[string]string{"php_admin_value[memory_limit]": "512M", "php_admin_value[open_basedir]": "/home/shop/:/tmp/"})
	clean.ClientMaxBody = "64m"

	got := one(clean, target)
	if got.Action != plan.ActionImport || got.PHPSettings != 1 {
		t.Fatalf("clean: %s %d %v", got.Action, got.PHPSettings, got.Reasons)
	}

	for _, tc := range []struct {
		name   string
		change func(*scan.Site)
		code   string
	}{
		{"clamped", func(s *scan.Site) { s.Pool.Settings["php_admin_value[memory_limit]"] = "8G" }, "php_settings_changed"},
		{"not taken", func(s *scan.Site) { s.Pool.Settings["php_admin_value[opcache.enable]"] = "1" }, "php_settings_not_taken"},
		{"body larger", func(s *scan.Site) { s.ClientMaxBody = "512m" }, "body_size"},
		{"body unlimited", func(s *scan.Site) { s.ClientMaxBody = "0" }, "body_size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := withPool(map[string]string{"php_admin_value[memory_limit]": "512M"})
			s.ClientMaxBody = "64M"
			tc.change(&s)

			got := one(s, target)
			if got.Action != plan.ActionReview || !hasCode(got, tc.code) {
				t.Fatalf("got %s %v, want review with %s", got.Action, got.Reasons, tc.code)
			}
		})
	}

	// A body size that matches a raised post_max_size is fine.
	s := withPool(map[string]string{"php_admin_value[post_max_size]": "512M", "php_admin_value[upload_max_filesize]": "512M"})
	s.ClientMaxBody = "512m"

	if got := one(s, target); got.Action != plan.ActionImport {
		t.Fatalf("matching body size: %s %v", got.Action, got.Reasons)
	}
}
