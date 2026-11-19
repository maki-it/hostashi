// SPDX-License-Identifier: AGPL-3.0-or-later

// Package snippet checks custom nginx snippets of a site against an
// allowlist of directives and renders the checked result (ADR-0029).
//
// A snippet is nginx text that an admin writes, or that the CloudPanel
// importer carries over. Free nginx text in a server block would let the
// panel make the root nginx master write files anywhere and reach other
// sites, so every directive must be on the allowlist below and its arguments
// pass a typed check. The panel runs Check to show errors per line; the
// agent runs it again before rendering, because the panel is not trusted
// with root-level text (ADR-0004).
//
// Render never writes the source text: it writes the parsed directives
// again with its own quoting, so nginx sees exactly what was checked, even
// where this parser and nginx would read odd input differently.
package snippet

import (
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/nginxconf"
)

// Slot is the place in the site's server block where a snippet goes.
type Slot string

const (
	// SlotServer is inside the server block that serves the site, after
	// Hostashi's own locations.
	SlotServer Slot = "server"
	// SlotRoot is inside Hostashi's own "location /" of the site.
	SlotRoot Slot = "root"
)

// Limits of one snippet [E: ADR-0029].
const (
	MaxBytes      = 64 << 10
	MaxDirectives = 500
	// MaxDepth is the deepest nesting of blocks inside a snippet.
	MaxDepth = 4
)

// Site is what the check needs to know about the site.
type Site struct {
	// Home is the site's home directory; root, alias and
	// auth_basic_user_file must point below it.
	Home string
	// PHPSocket is the site's own PHP-FPM socket; empty means fastcgi_pass
	// is not allowed.
	PHPSocket string
}

// Problem is one rejected line.
type Problem struct {
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

func (p Problem) String() string { return fmt.Sprintf("line %d: %s", p.Line, p.Msg) }

// Error lists every problem of a snippet.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		parts = append(parts, p.String())
	}

	return "snippet: " + strings.Join(parts, "; ")
}

// ErrInvalidSite means the Site given to Check is not usable.
var ErrInvalidSite = errors.New("snippet: invalid site")

// cleanPathRe keeps nginx syntax out of the paths that the check compares.
var cleanPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

func cleanPath(p string) bool {
	return cleanPathRe.MatchString(p) && path.Clean(p) == p
}

// Check parses src and checks it for slot. An empty snippet is valid. The
// returned directives are what Render writes.
func Check(src string, slot Slot, site Site) ([]nginxconf.Directive, error) {
	if slot != SlotServer && slot != SlotRoot {
		return nil, fmt.Errorf("%w: unknown slot %q", ErrInvalidSite, slot)
	}

	if !cleanPath(site.Home) || site.Home == "/" {
		return nil, fmt.Errorf("%w: home %q", ErrInvalidSite, site.Home)
	}

	if site.PHPSocket != "" && !cleanPath(site.PHPSocket) {
		return nil, fmt.Errorf("%w: PHP socket %q", ErrInvalidSite, site.PHPSocket)
	}

	if len(src) > MaxBytes {
		return nil, &Error{[]Problem{{1, fmt.Sprintf("snippet is larger than %d bytes", MaxBytes)}}}
	}

	ds, err := nginxconf.Parse(src)
	if err != nil {
		var pe *nginxconf.Error
		if errors.As(err, &pe) {
			return nil, &Error{[]Problem{{pe.Line, pe.Msg}}}
		}

		return nil, &Error{[]Problem{{1, err.Error()}}}
	}

	c := &checker{site: site}
	c.list(ds, 0)

	if c.count > MaxDirectives {
		c.add(1, fmt.Sprintf("snippet has more than %d directives", MaxDirectives))
	}

	if len(c.problems) > 0 {
		sort.SliceStable(c.problems, func(i, j int) bool { return c.problems[i].Line < c.problems[j].Line })

		return nil, &Error{c.problems}
	}

	return ds, nil
}

type checker struct {
	site     Site
	problems []Problem
	count    int
}

func (c *checker) add(line int, msg string) {
	c.problems = append(c.problems, Problem{line, msg})
}

func (c *checker) list(ds []nginxconf.Directive, depth int) {
	for _, d := range ds {
		c.count++
		c.directive(d, depth)
	}
}

func (c *checker) directive(d nginxconf.Directive, depth int) {
	for _, a := range d.Args {
		if strings.ContainsFunc(a, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			c.add(d.Line, d.Name+": control characters are not allowed")

			return
		}
	}

	r, ok := rules[d.Name]
	if !ok {
		r, ok = prefixRule(d.Name)
	}

	if !ok {
		c.add(d.Line, fmt.Sprintf("%q is not allowed in custom snippets", d.Name))

		return
	}

	if r.block != d.IsBlock() {
		if r.block {
			c.add(d.Line, d.Name+" needs a block")
		} else {
			c.add(d.Line, d.Name+" takes no block")
		}

		return
	}

	if msg := r.check(d.Args, c.site); msg != "" {
		c.add(d.Line, d.Name+": "+msg)

		return
	}

	if d.IsBlock() {
		if depth+1 > MaxDepth {
			c.add(d.Line, fmt.Sprintf("blocks are nested deeper than %d", MaxDepth))

			return
		}

		c.list(d.Block, depth+1)
	}
}

// rule is the check of one directive. check returns "" when the arguments
// are fine, otherwise the reason.
type rule struct {
	block bool
	check func(args []string, site Site) string
}

// anyArgs accepts min..max arguments (max < 0: unlimited) with any value.
func anyArgs(minN, maxN int) func([]string, Site) string {
	return func(args []string, _ Site) string {
		if len(args) < minN || (maxN >= 0 && len(args) > maxN) {
			return "wrong number of arguments"
		}

		return ""
	}
}

var rules = map[string]rule{
	// Structure.
	"location":     {block: true, check: checkLocation},
	"if":           {block: true, check: anyArgs(1, -1)},
	"limit_except": {block: true, check: checkMethods},

	// Responses and routing. Variables are fine, no file paths.
	"return":       {check: anyArgs(1, 2)},
	"rewrite":      {check: anyArgs(2, 3)},
	"try_files":    {check: anyArgs(2, -1)},
	"error_page":   {check: anyArgs(2, -1)},
	"set":          {check: anyArgs(2, 2)},
	"break":        {check: anyArgs(0, 0)},
	"index":        {check: anyArgs(1, -1)},
	"default_type": {check: anyArgs(1, 1)},
	"charset":      {check: anyArgs(1, 1)},
	"autoindex":    {check: onOff},

	// Headers and caching.
	"add_header":        {check: anyArgs(2, 3)},
	"expires":           {check: anyArgs(1, 2)},
	"etag":              {check: onOff},
	"if_modified_since": {check: anyArgs(1, 1)},

	// Access.
	"allow":                {check: anyArgs(1, 1)},
	"deny":                 {check: anyArgs(1, 1)},
	"auth_basic":           {check: anyArgs(1, 1)},
	"auth_basic_user_file": {check: checkHomePath},
	"satisfy":              {check: anyArgs(1, 1)},

	// Files.
	"root":  {check: checkHomePath},
	"alias": {check: checkHomePath},

	// Limits.
	"client_max_body_size": {check: anyArgs(1, 1)},
	"client_body_timeout":  {check: anyArgs(1, 1)},

	// Upstreams.
	"proxy_pass":            {check: checkProxyPass},
	"proxy_set_header":      {check: anyArgs(2, 2)},
	"proxy_hide_header":     {check: anyArgs(1, 1)},
	"proxy_redirect":        {check: anyArgs(1, 2)},
	"proxy_connect_timeout": {check: anyArgs(1, 1)},
	"proxy_read_timeout":    {check: anyArgs(1, 1)},
	"proxy_send_timeout":    {check: anyArgs(1, 1)},
	"proxy_buffering":       {check: onOff},
	"proxy_http_version":    {check: anyArgs(1, 1)},

	// PHP.
	"fastcgi_pass":            {check: checkFastCGIPass},
	"fastcgi_param":           {check: anyArgs(2, 3)},
	"fastcgi_index":           {check: anyArgs(1, 1)},
	"fastcgi_split_path_info": {check: anyArgs(1, 1)},
	"fastcgi_buffers":         {check: anyArgs(2, 2)},
	"fastcgi_buffer_size":     {check: anyArgs(1, 1)},
	"fastcgi_connect_timeout": {check: anyArgs(1, 1)},
	"fastcgi_read_timeout":    {check: anyArgs(1, 1)},
	"fastcgi_send_timeout":    {check: anyArgs(1, 1)},

	// Logging: no paths.
	"access_log":    {check: checkAccessLog},
	"log_not_found": {check: onOff},
}

// compression directives are allowed by prefix; their values are sizes,
// levels, types and on/off, none of them a path.
var compressionPrefixes = []string{"gzip", "brotli"}

func prefixRule(name string) (rule, bool) {
	for _, p := range compressionPrefixes {
		if name == p || strings.HasPrefix(name, p+"_") {
			return rule{check: anyArgs(1, -1)}, true
		}
	}

	return rule{}, false
}

func onOff(args []string, _ Site) string {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return "must be on or off"
	}

	return ""
}

var methodRe = regexp.MustCompile(`^[A-Z]+$`)

func checkMethods(args []string, _ Site) string {
	if len(args) == 0 {
		return "wrong number of arguments"
	}

	for _, a := range args {
		if !methodRe.MatchString(a) {
			return fmt.Sprintf("invalid method %q", a)
		}
	}

	return ""
}

// ownedPrefixes are paths whose locations Hostashi renders itself.
var ownedPrefixes = []string{"/.hostashi/", "/.well-known/acme-challenge/"}

func checkLocation(args []string, _ Site) string {
	var mod, uri string

	switch len(args) {
	case 1:
		uri = args[0]
	case 2:
		mod, uri = args[0], args[1]
	default:
		return "wrong number of arguments"
	}

	switch mod {
	case "", "=", "^~":
		if uri == "/" && mod != "=" {
			return `"location /" belongs to Hostashi; use the "location /" snippet instead`
		}

		for _, p := range ownedPrefixes {
			if strings.HasPrefix(uri, p) {
				return strings.TrimSuffix(p, "/") + " belongs to Hostashi"
			}
		}
	case "~", "~*":
		// Hostashi's own locations are exact or ^~ matches, which win over
		// every regex location in nginx.
	default:
		return fmt.Sprintf("unknown modifier %q", mod)
	}

	if uri == "" {
		return "empty location"
	}

	return ""
}

func checkHomePath(args []string, site Site) string {
	if len(args) != 1 {
		return "wrong number of arguments"
	}

	p := args[0]
	if strings.HasSuffix(p, "/") && len(p) > 1 {
		// alias of a directory location ends in a slash.
		p = strings.TrimSuffix(p, "/")
	}

	if !cleanPath(p) {
		return "must be a plain absolute path without variables or .."
	}

	if p != site.Home && !strings.HasPrefix(p, site.Home+"/") {
		return "must be inside the site's home directory"
	}

	return ""
}

func checkAccessLog(args []string, _ Site) string {
	if len(args) != 1 || args[0] != "off" {
		return "only \"access_log off\" is allowed"
	}

	return ""
}

func checkFastCGIPass(args []string, site Site) string {
	if site.PHPSocket == "" {
		return "the site has no PHP"
	}

	if len(args) != 1 || args[0] != "unix:"+site.PHPSocket {
		return "only the site's own PHP socket is allowed"
	}

	return ""
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

var uriPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./~%:@!&'()*+,;=-]*$`)

// checkProxyPass allows http(s) to a literal host or IP, and loopback only
// with an explicit port (ADR-0029, Kim 2026-11-19). Unix sockets and
// variables are never allowed: they would reach the panel or another site.
func checkProxyPass(args []string, _ Site) string {
	if len(args) != 1 {
		return "wrong number of arguments"
	}

	u := args[0]

	var rest string

	switch {
	case strings.HasPrefix(u, "http://"):
		rest = strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "https://"):
		rest = strings.TrimPrefix(u, "https://")
	default:
		return "must start with http:// or https://"
	}

	if strings.Contains(u, "$") {
		return "variables are not allowed"
	}

	if strings.HasPrefix(strings.ToLower(rest), "unix:") {
		return "unix sockets are not allowed"
	}

	hostport, p, hasPath := strings.Cut(rest, "/")
	if hasPath && !uriPathRe.MatchString("/"+p) {
		return "invalid path"
	}

	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, ""
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}

	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return fmt.Sprintf("invalid port %q", port)
		}
	}

	ip := net.ParseIP(host)

	switch {
	case ip != nil:
		if (ip.IsLoopback() || ip.IsUnspecified()) && port == "" {
			return "a loopback address needs an explicit port"
		}
	case !hostRe.MatchString(host):
		return fmt.Sprintf("invalid host %q", host)
	case strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost"):
		if port == "" {
			return "a loopback address needs an explicit port"
		}
	case !strings.Contains(host, "."):
		// A single label would name an upstream block, which snippets
		// cannot define.
		return "host must be a full domain name or an IP address"
	}

	return ""
}
