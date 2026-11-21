// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"slices"
	"testing"
)

func TestPHPSettingsDefaults(t *testing.T) {
	var s PHPSettings
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}

	want := []PHPIniValue{
		{"memory_limit", "256M"},
		{"upload_max_filesize", "64M"},
		{"post_max_size", "64M"},
		{"max_execution_time", "60"},
		{"max_input_time", "60"},
		{"max_input_vars", "3000"},
		{"max_file_uploads", "20"},
		{"date.timezone", "UTC"},
		{"display_errors", "Off"},
		{"error_reporting", "E_ALL & ~E_DEPRECATED"},
		{"session.gc_maxlifetime", "1440"},
		{"short_open_tag", "Off"},
	}
	if got := s.IniValues(); !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}

	if s.ClientMaxBodySize() != 64<<20 || s.FastCGIReadTimeout() != 70 {
		t.Fatalf("nginx values %d %d", s.ClientMaxBodySize(), s.FastCGIReadTimeout())
	}
}

func TestPHPSettingsCustom(t *testing.T) {
	on := true

	s := PHPSettings{
		MemoryLimit: 1 << 30, UploadMaxFilesize: 512 << 20, PostMaxSize: 520 << 20,
		MaxExecutionTime: 300, MaxInputVars: 10000, Timezone: "Europe/Berlin",
		DisplayErrors: &on, ErrorReporting: ErrorReportingDevelopment, ShortOpenTag: &on,
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, v := range s.IniValues() {
		got[v.Key] = v.Value
	}

	for k, v := range map[string]string{
		"memory_limit": "1024M", "upload_max_filesize": "512M", "post_max_size": "520M",
		"max_execution_time": "300", "max_input_vars": "10000", "date.timezone": "Europe/Berlin",
		"display_errors": "On", "error_reporting": "E_ALL", "short_open_tag": "On", "max_input_time": "60",
	} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	if s.ClientMaxBodySize() != 520<<20 || s.FastCGIReadTimeout() != 310 {
		t.Fatalf("nginx values %d %d", s.ClientMaxBodySize(), s.FastCGIReadTimeout())
	}

	if up := (PHPSettings{UploadMaxFilesize: 128 << 20}); up.Validate() != nil || up.ClientMaxBodySize() != 128<<20 {
		t.Fatal("upload size alone does not raise post_max_size")
	}

	if (PHPSettings{MaxExecutionTime: 10}).FastCGIReadTimeout() != 60 {
		t.Fatal("timeout below nginx's default")
	}
}

func TestPHPSettingsRejects(t *testing.T) {
	cases := map[string]PHPSettings{
		"memory_limit":           {MemoryLimit: 16 << 20},
		"memory_limit high":      {MemoryLimit: 8 << 30},
		"upload high":            {UploadMaxFilesize: 3 << 30, PostMaxSize: 3 << 30},
		"post below upload":      {UploadMaxFilesize: 128 << 20, PostMaxSize: 64 << 20},
		"post below default up":  {PostMaxSize: 32 << 20},
		"not whole MiB":          {MemoryLimit: 256<<20 + 1},
		"time low":               {MaxExecutionTime: 5},
		"time negative":          {MaxInputTime: -1},
		"time high":              {MaxExecutionTime: 3601},
		"input vars":             {MaxInputVars: 999},
		"file uploads":           {MaxFileUploads: 501},
		"session":                {SessionGCMaxLifetime: 59},
		"timezone unknown":       {Timezone: "Mars/Olympus"},
		"timezone local":         {Timezone: "Local"},
		"timezone path":          {Timezone: "../../etc/passwd"},
		"timezone absolute":      {Timezone: "/etc/localtime"},
		"timezone newline":       {Timezone: "UTC\nphp_admin_value[open_basedir] = /"},
		"timezone dots":          {Timezone: "Europe/../UTC"},
		"error_reporting free":   {ErrorReporting: "E_ALL"},
		"error_reporting inject": {ErrorReporting: "production\nauto_prepend_file=/x"},
	}
	for name, s := range cases {
		err := s.Validate()
		if !errors.Is(err, ErrPHPSetting) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	var se *PHPSettingError
	if err := (PHPSettings{MaxInputVars: 5}).Validate(); !errors.As(err, &se) || se.Key != "max_input_vars" {
		t.Fatalf("got %v", err)
	}
}

func TestWebSitePHPSettingsNeedPHP(t *testing.T) {
	s := WebSite{User: "blog", Domains: []string{"blog.example"}, PHPSettings: &PHPSettings{}}
	if err := (WebApplyRequest{Sites: []WebSite{s}}).Validate(); err == nil {
		t.Fatal("settings without PHP accepted")
	}

	s.PHP = PHPVersions[0]
	if err := (WebApplyRequest{Sites: []WebSite{s}}).Validate(); err != nil {
		t.Fatal(err)
	}

	s.PHPSettings = &PHPSettings{MemoryLimit: 1}
	if err := (WebApplyRequest{Sites: []WebSite{s}}).Validate(); !errors.Is(err, ErrPHPSetting) {
		t.Fatalf("got %v", err)
	}
}
