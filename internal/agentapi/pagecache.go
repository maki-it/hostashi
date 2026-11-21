// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Page cache presets (ADR-0046). The preset decides which cookies and paths
// always skip the cache; package web holds the rules.
const (
	PageCacheWordPress   = "wordpress"
	PageCacheWooCommerce = "woocommerce"
	// PageCachePHP is the strict preset for any other PHP application: a
	// request with any cookie skips the cache.
	PageCachePHP = "php"
)

// PageCachePresets lists the presets in display order.
var PageCachePresets = []string{PageCacheWordPress, PageCacheWooCommerce, PageCachePHP}

// PageCacheMinutes are the cache times a site may choose (ADR-0046).
var PageCacheMinutes = []int{1, 5, 10, 30, 60}

// PageCacheSizesMB are the cache sizes a site may get (ADR-0046).
var PageCacheSizesMB = []int{64, 128, 256, 512, 1024, 2048, 4096}

// Defaults of a page cache that is switched on.
const (
	DefaultPageCacheMinutes = 10
	DefaultPageCacheSizeMB  = 256
)

// MaxPageCacheExcludes bounds the excluded paths of one site.
const MaxPageCacheExcludes = 50

// pageCacheExcludeRe is one excluded path: a path with an optional trailing
// "*" for a prefix. Quotes, ";", braces, "$", spaces, "?" and control
// characters cannot pass, so the entry can be quoted into an nginx map.
var pageCacheExcludeRe = regexp.MustCompile(`^/[A-Za-z0-9._~%/-]{0,200}\*?$`)

// WebPageCache is the page cache (nginx FastCGI cache) of a site with PHP
// (ADR-0046). The agent picks the cache directory itself.
type WebPageCache struct {
	Preset  string `json:"preset"`
	Minutes int    `json:"minutes"`
	SizeMB  int    `json:"sizeMB"`
	// Exclude are paths that never use the cache: "/path" matches that path
	// only, "/path*" every path that starts with "/path".
	Exclude []string `json:"exclude,omitempty"`
}

// Validate checks the fixed choices and every excluded path.
func (c WebPageCache) Validate() error {
	if !slices.Contains(PageCachePresets, c.Preset) {
		return fmt.Errorf("page cache: unknown preset %q", c.Preset)
	}

	if !slices.Contains(PageCacheMinutes, c.Minutes) {
		return fmt.Errorf("page cache: cache time must be one of %v minutes", PageCacheMinutes)
	}

	if !slices.Contains(PageCacheSizesMB, c.SizeMB) {
		return fmt.Errorf("page cache: size must be one of %v MB", PageCacheSizesMB)
	}

	return ValidatePageCacheExcludes(c.Exclude)
}

// ValidatePageCacheExcludes checks the excluded paths of a site: at most
// MaxPageCacheExcludes, each unique and of the form "/path" or "/path*",
// without "." or ".." segments and without empty segments.
func ValidatePageCacheExcludes(paths []string) error {
	if len(paths) > MaxPageCacheExcludes {
		return fmt.Errorf("page cache: at most %d excluded paths", MaxPageCacheExcludes)
	}

	seen := make(map[string]bool, len(paths))

	for _, p := range paths {
		if !pageCacheExcludeRe.MatchString(p) {
			return fmt.Errorf("page cache: invalid excluded path %q", p)
		}

		inner := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "*")
		for i, seg := range strings.Split(inner, "/") {
			last := i == strings.Count(inner, "/")
			if seg == "." || seg == ".." || (seg == "" && !last) {
				return fmt.Errorf("page cache: invalid excluded path %q", p)
			}
		}

		if seen[p] {
			return fmt.Errorf("page cache: excluded path %q listed twice", p)
		}

		seen[p] = true
	}

	return nil
}
