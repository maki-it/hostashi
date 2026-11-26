// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "testing"

func TestParseWPCloneURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://stage.example.com":   "https stage.example.com",
		"http://stage.example.com/":   "http stage.example.com",
		"https://stage.example.com:8": "",
		"https://u@stage.example.com": "",
		"https://stage.example.com/x": "",
		"https://stage.example.com?a": "",
		"ftp://stage.example.com":     "",
		"stage.example.com":           "",
		"https://-x.example.com":      "",
	} {
		scheme, host, err := ParseWPCloneURL(raw)
		if got := scheme + " " + host; (want == "" && err == nil) || (want != "" && got != want) {
			t.Errorf("%s: %q %v", raw, got, err)
		}
	}
}

func TestWPCloneRequestValidate(t *testing.T) {
	ok := WPCloneRequest{
		Site: "shop-stage", PHP: "8.4", URL: "https://stage.shop.example.com", From: []string{"shop.example.com"},
		Databases: []WPCloneDB{
			{Source: "shop_wp", Target: "shop_stage_wp", User: "shop_stage"},
			{Source: "legacy", Target: "shop_stage_legacy", User: "shop_stage"},
		},
		DBPasswords: map[string]string{"shop_stage": "secret-secret-1"},
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, f := range map[string]func(*WPCloneRequest){
		"web dir":       func(r *WPCloneRequest) { r.WebDir = "../x" },
		"php":           func(r *WPCloneRequest) { r.PHP = "5.6" },
		"reserved user": func(r *WPCloneRequest) { r.Databases[0].User = "root" },
		"stray user":    func(r *WPCloneRequest) { r.DBPasswords["other"] = "secret-secret-1" },
		"newline pw":    func(r *WPCloneRequest) { r.DBPasswords["shop_stage"] = "secret-secret\n1" },
	} {
		r := ok
		r.Databases = append([]WPCloneDB(nil), ok.Databases...)
		r.DBPasswords = map[string]string{"shop_stage": "secret-secret-1"}
		f(&r)

		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
