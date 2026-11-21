// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// PageCache is the page cache of a site (ADR-0046): nginx's FastCGI cache
// in Dir, a directory that the agent prepares for nginx's workers.
type PageCache struct {
	Dir     string   `json:"dir"`
	Preset  string   `json:"preset"`
	Minutes int      `json:"minutes"`
	SizeMB  int      `json:"sizeMB"`
	Exclude []string `json:"exclude,omitempty"`
}

func (c PageCache) validate() error {
	if !rootRe.MatchString(c.Dir) || path.Clean(c.Dir) != c.Dir {
		return fmt.Errorf("invalid page cache directory %q", c.Dir)
	}

	return agentapi.WebPageCache{Preset: c.Preset, Minutes: c.Minutes, SizeMB: c.SizeMB, Exclude: c.Exclude}.Validate()
}

// cachePreset holds the rules that always skip the cache for a preset.
type cachePreset struct {
	// AnyCookie skips on every request with a Cookie header.
	AnyCookie bool
	// Cookies are cookie name prefixes.
	Cookies []string
	// Paths are path prefixes of $request_uri.
	Paths []string
	// Queries are query parameters ("name=" or "name=value").
	Queries []string
}

var wordPressPreset = cachePreset{
	Cookies: []string{"wordpress_logged_in_", "wp-postpass_", "comment_author_", "wordpress_sec_"},
	Paths:   []string{"/wp-admin/", "/wp-login.php", "/wp-cron.php", "/xmlrpc.php", "/wp-json/"},
	Queries: []string{"preview=true"},
}

var cachePresets = map[string]cachePreset{
	agentapi.PageCacheWordPress: wordPressPreset,
	agentapi.PageCacheWooCommerce: {
		Cookies: append(append([]string{}, wordPressPreset.Cookies...),
			"woocommerce_items_in_cart", "woocommerce_cart_hash", "wp_woocommerce_session_"),
		Paths:   append(append([]string{}, wordPressPreset.Paths...), "/cart/", "/checkout/", "/my-account/"),
		Queries: append(append([]string{}, wordPressPreset.Queries...), "add-to-cart=", "wc-ajax="),
	},
	agentapi.PageCachePHP: {AnyCookie: true},
}

// cacheTmpl is the site's cache zone and its skip variables, rendered at
// the http level before the site's server blocks. Every pattern comes from
// Go: preset data or an excluded path that passed the allowlist and
// regexp.QuoteMeta. The patterns never contain a backslash followed by t, r
// or n, which nginx would turn into control characters inside quotes.
const cacheSrc = `{{define "cachezone"}}

# Page cache (ADR-0046).
fastcgi_cache_path {{.Dir}} levels=1:2 keys_zone={{.Zone}}:2m max_size={{.SizeMB}}m inactive=1d use_temp_path=off;
map $http_cookie ${{.Cookie}} {
    {{- if .AnyCookie}}
    "" 0;
    default 1;
    {{- else}}
    default 0;
    {{- range .Cookies}}
    "{{.}}" 1;
    {{- end}}
    {{- end}}
}
map $request_uri ${{.Path}} {
    default 0;
    {{- range .Paths}}
    "{{.}}" 1;
    {{- end}}
}
{{- end}}
{{define "cacheloc"}}
        fastcgi_cache {{.Zone}};
        fastcgi_cache_key "$scheme$request_method$host$request_uri";
        fastcgi_cache_methods GET HEAD;
        fastcgi_cache_valid 200 301 302 {{.Minutes}}m;
        fastcgi_cache_valid 404 1m;
        fastcgi_cache_lock on;
        fastcgi_cache_use_stale error timeout updating http_500 http_503;
        fastcgi_cache_background_update on;
        fastcgi_cache_bypass $hostashi_cache_method $hostashi_cache_auth ${{.Cookie}} ${{.Path}};
        fastcgi_no_cache $hostashi_cache_method $hostashi_cache_auth ${{.Cookie}} ${{.Path}};
{{- end}}`

// cacheView is what the cache templates render for one site.
type cacheView struct {
	Dir, Zone, Cookie, Path string
	SizeMB, Minutes         int
	AnyCookie               bool
	Cookies, Paths          []string
}

func newCacheView(user string, c PageCache) (cacheView, error) {
	p, ok := cachePresets[c.Preset]
	if !ok {
		return cacheView{}, fmt.Errorf("unknown page cache preset %q", c.Preset)
	}

	// Site users may hold "-", which nginx variable names cannot.
	id := hex.EncodeToString([]byte(user))
	v := cacheView{
		Dir: c.Dir, Zone: "hostashi_cache_" + id, Cookie: "hostashi_cache_cookie_" + id, Path: "hostashi_cache_path_" + id,
		SizeMB: c.SizeMB, Minutes: c.Minutes, AnyCookie: p.AnyCookie,
	}

	for _, name := range p.Cookies {
		// Cookie names start the header or follow "; ".
		v.Cookies = append(v.Cookies, "~(^|;[ ]*)"+regexp.QuoteMeta(name))
	}

	for _, pre := range p.Paths {
		v.Paths = append(v.Paths, "~^"+regexp.QuoteMeta(pre))
	}

	for _, q := range p.Queries {
		v.Paths = append(v.Paths, "~[?&]"+regexp.QuoteMeta(q))
	}

	for _, e := range c.Exclude {
		if pre, ok := strings.CutSuffix(e, "*"); ok {
			v.Paths = append(v.Paths, "~^"+regexp.QuoteMeta(pre))
		} else {
			// The path itself, with or without a query string.
			v.Paths = append(v.Paths, "~^"+regexp.QuoteMeta(e)+"([?]|$)")
		}
	}

	for _, s := range append(append([]string{}, v.Cookies...), v.Paths...) {
		if !safeQuoted(s) {
			return cacheView{}, fmt.Errorf("page cache pattern %s cannot be quoted", strconv.Quote(s))
		}
	}

	return v, nil
}

// safeQuoted reports whether s can stand between double quotes in an nginx
// config: no quote, no control character, and every backslash escapes a
// character that nginx keeps as it is.
func safeQuoted(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c < 0x20 || c == 0x7f:
			return false
		case c == '\\':
			if i+1 >= len(s) || strings.ContainsRune(`"'\trn`, rune(s[i+1])) {
				return false
			}

			i++
		}
	}

	return true
}
