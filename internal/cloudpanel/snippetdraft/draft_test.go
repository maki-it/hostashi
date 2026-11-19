// SPDX-License-Identifier: AGPL-3.0-or-later

package snippetdraft

import (
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/snippet"
)

var target = Target{Home: "/srv/pool/shop", WebRoot: "/srv/pool/shop/htdocs", PHPSocket: "/run/hostashi-php/shop/php-fpm.sock"}

func custom(ctx, raw string, line int) scan.Custom {
	return scan.Custom{Context: ctx, Raw: raw, Source: "/etc/nginx/sites-enabled/shop.conf", Line: line}
}

func TestDraft(t *testing.T) {
	s := scan.Site{
		User:    "shop",
		DocRoot: "/home/shop/htdocs/shop.example.com",
		Custom: []scan.Custom{
			custom("server: add_header", `add_header X-Frame-Options "SAMEORIGIN";`, 10),
			custom("server: add_header", `add_header X-Frame-Options "SAMEORIGIN";`, 40), // backend tier
			custom("server: if", `if ($scheme != "https") { rewrite ^ https://$host$request_uri? permanent; }`, 11),
			custom("server: pagespeed", `pagespeed on;`, 12),
			custom("location ^~ /.well-known/acme-challenge/", `location ^~ /.well-known/acme-challenge/ { root /home/clp/acme; }`, 13),
			custom("location /", `location / { try_files $uri $uri/ /index.php?$args; add_header X-Root yes; }`, 14),
			custom("location /media/", `location /media/ { alias /home/shop/htdocs/shop.example.com/media/; }`, 15),
			custom("location /private/", `location /private/ { auth_basic "x"; auth_basic_user_file /home/shop/.htpasswd; }`, 16),
			custom("location ~ ^/legacy/.+\\.php$", `location ~ ^/legacy/.+\.php$ { fastcgi_pass 127.0.0.1:12000; fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name; }`, 17),
			custom("location /other/", `location /other/ { alias /home/other/htdocs/; }`, 18),
			custom("server: access_log", `access_log /home/shop/logs/nginx/access.log main;`, 19),
			custom("server (redirect)", `301 https://shop.example.com$request_uri`, 20),
			custom("server: map", `map $a $b { default 1; }`, 21),
			custom("server: try_files", `try_files $uri $uri/ /index.php?$args;`, 22),
		},
	}

	r := Draft(s, target)

	if len(r.Items) != len(s.Custom) {
		t.Fatalf("%d items for %d entries", len(r.Items), len(s.Custom))
	}

	want := map[int]Action{
		10: Kept, 40: Dropped, 11: Kept, 12: Dropped, 13: Dropped, 14: Kept, 15: Rewritten, 16: Rewritten,
		17: Rewritten, 18: Dropped, 19: Dropped, 20: Dropped, 21: Dropped, 22: Dropped,
	}
	for _, it := range r.Items {
		if it.Action != want[it.Line] {
			t.Errorf("line %d (%s): %s %q, want %s", it.Line, it.Context, it.Action, it.Reason, want[it.Line])
		}

		if it.Action == Dropped && it.Reason == "" {
			t.Errorf("line %d dropped without a reason", it.Line)
		}
	}

	for _, wantText := range []string{
		`add_header X-Frame-Options SAMEORIGIN;`,
		"alias /srv/pool/shop/htdocs/media/;",
		"auth_basic_user_file /srv/pool/shop/.htpasswd;",
		"fastcgi_pass unix:/run/hostashi-php/shop/php-fpm.sock;",
	} {
		if !strings.Contains(r.Server, wantText) {
			t.Errorf("server draft lacks %q:\n%s", wantText, r.Server)
		}
	}

	if strings.Count(r.Server, "X-Frame-Options") != 1 {
		t.Errorf("duplicate kept:\n%s", r.Server)
	}

	if strings.TrimSpace(r.Root) != "add_header X-Root yes;" {
		t.Errorf("root draft:\n%s", r.Root)
	}

	for _, bad := range []string{"pagespeed", "acme", "/home/other", "access_log", "map ", "127.0.0.1"} {
		if strings.Contains(r.Server+r.Root, bad) {
			t.Errorf("draft contains %q:\n%s\n%s", bad, r.Server, r.Root)
		}
	}

	// The draft itself passes the check as a whole.
	ss := snippet.Site{Home: target.Home, PHPSocket: target.PHPSocket}
	if _, err := snippet.Check(r.Server, snippet.SlotServer, ss); err != nil {
		t.Errorf("server draft fails the check: %v", err)
	}

	if _, err := snippet.Check(r.Root, snippet.SlotRoot, ss); err != nil {
		t.Errorf("root draft fails the check: %v", err)
	}
}

func TestDraftWithoutPHP(t *testing.T) {
	s := scan.Site{User: "web", DocRoot: "/home/web/htdocs/web.example.com", Custom: []scan.Custom{
		custom("location ~ \\.php$", `location ~ \.php$ { fastcgi_pass 127.0.0.1:12000; }`, 3),
	}}

	r := Draft(s, Target{Home: "/home/web", WebRoot: "/home/web/htdocs"})
	if len(r.Items) != 1 || r.Items[0].Action != Dropped || !strings.Contains(r.Items[0].Reason, "no PHP") || r.Server != "" {
		t.Fatalf("%+v", r)
	}
}

func TestDraftEmpty(t *testing.T) {
	r := Draft(scan.Site{User: "a"}, Target{Home: "/home/a", WebRoot: "/home/a/htdocs"})
	if r.Server != "" || r.Root != "" || r.Items == nil || len(r.Items) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestDraftKeepsUnparsable(t *testing.T) {
	r := Draft(scan.Site{User: "a", Custom: []scan.Custom{custom("server: x", `x "unterminated`, 1), custom("location /a", `location /a { return 200; } return 201;`, 2)}},
		Target{Home: "/home/a", WebRoot: "/home/a/htdocs"})
	if len(r.Items) != 2 || r.Items[0].Action != Dropped || r.Items[1].Action != Dropped {
		t.Fatalf("%+v", r.Items)
	}
}
