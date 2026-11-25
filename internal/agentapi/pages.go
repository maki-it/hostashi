// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Maintenance and parking pages (ADR-0027). The panel checks an uploaded
// page and sends its files in chunks; the agent stores them below its pages
// directory, where nginx serves them under PagePathPrefix.

const (
	// MaxPageBytes bounds the unpacked size of one page.
	MaxPageBytes = 2 << 20
	// MaxPageFiles bounds the number of files of one page.
	MaxPageFiles = 100
	// MaxPageChunk bounds the data of one page.write; with base64 and the
	// envelope it stays below agentrpc.MaxFrame.
	MaxPageChunk = 40 << 10
	// MaxPageDepth is the number of directory levels below the page root.
	MaxPageDepth = 2
	// PageIndex is the file served as the page itself.
	PageIndex = "index.html"
	// PagePathPrefix is where a site serves page files: PagePathPrefix +
	// "<id>/<file>". The health check already reserves "/.hostashi/".
	PagePathPrefix = "/.hostashi/page/"
	// MaxAllowIPs bounds the maintenance allowlist of a site.
	MaxAllowIPs = 20
)

var (
	pageIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	pagePartRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)
	digestRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// PageTypes maps the allowed file extensions to the Content-Type nginx
// sends. Nothing else can be part of a page.
var PageTypes = map[string]string{
	"html":  "text/html; charset=utf-8",
	"css":   "text/css; charset=utf-8",
	"js":    "text/javascript; charset=utf-8",
	"png":   "image/png",
	"jpg":   "image/jpeg",
	"jpeg":  "image/jpeg",
	"webp":  "image/webp",
	"gif":   "image/gif",
	"svg":   "image/svg+xml",
	"ico":   "image/x-icon",
	"woff2": "font/woff2",
}

// PageDomainPlaceholder in a page's HTML files is replaced with the visited
// domain when nginx serves the page (empty if the Host header is not a plain
// host name); the panel's preview shows PagePreviewDomain instead.
const (
	PageDomainPlaceholder = "%%DOMAIN%%"
	PagePreviewDomain     = "example.com"
)

// ValidatePageID accepts the id of a stored page.
func ValidatePageID(id string) error {
	if !pageIDRe.MatchString(id) {
		return fmt.Errorf("page id: must match %s", pageIDRe)
	}

	return nil
}

// ValidatePageDigest accepts a lower-case hex SHA-256.
func ValidatePageDigest(d string) error {
	if !digestRe.MatchString(d) {
		return errors.New("page digest: 64 lower-case hex digits required")
	}

	return nil
}

// ValidatePagePath accepts a relative file path of a page: at most
// MaxPageDepth directories, every part from a fixed alphabet that cannot
// start with a dot or dash, and an extension from PageTypes.
func ValidatePagePath(p string) error {
	parts := strings.Split(p, "/")
	if len(parts) > MaxPageDepth+1 {
		return fmt.Errorf("page file %q: more than %d directory levels", p, MaxPageDepth)
	}

	for _, part := range parts {
		if !pagePartRe.MatchString(part) {
			return fmt.Errorf("page file %q: invalid name", p)
		}
	}

	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	if _, ok := PageTypes[ext]; !ok {
		return fmt.Errorf("page file %q: type not allowed", p)
	}

	return nil
}

// PageDigest is the SHA-256 of a page's files, independent of their order:
// for each path in sorted order its length, the path, the content length
// and the content. Panel and agent compute it the same way.
func PageDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}

	slices.Sort(names)

	h := sha256.New()

	var n [8]byte

	for _, name := range names {
		binary.BigEndian.PutUint64(n[:], uint64(len(name)))
		h.Write(n[:])
		h.Write([]byte(name))
		binary.BigEndian.PutUint64(n[:], uint64(len(files[name])))
		h.Write(n[:])
		h.Write(files[name])
	}

	return hex.EncodeToString(h.Sum(nil))
}

// PageBeginRequest starts a new upload of page ID; an unfinished earlier
// upload of the same id is dropped.
type PageBeginRequest struct {
	ID string `json:"id"`
}

// Validate checks the request.
func (r PageBeginRequest) Validate() error { return ValidatePageID(r.ID) }

// PageWriteRequest writes Data at Offset of file Path of the upload of ID.
type PageWriteRequest struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
}

// Validate checks the request.
func (r PageWriteRequest) Validate() error {
	if err := ValidatePageID(r.ID); err != nil {
		return err
	}

	if err := ValidatePagePath(r.Path); err != nil {
		return err
	}

	if len(r.Data) > MaxPageChunk {
		return fmt.Errorf("page chunk larger than %d bytes", MaxPageChunk)
	}

	if r.Offset < 0 || r.Offset+int64(len(r.Data)) > MaxPageBytes {
		return errors.New("page chunk outside the page size limit")
	}

	return nil
}

// PageCommitRequest finishes the upload of ID. The agent compares the
// digest of what it received with Digest and stores the page under it.
type PageCommitRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Validate checks the request.
func (r PageCommitRequest) Validate() error {
	if err := ValidatePageID(r.ID); err != nil {
		return err
	}

	return ValidatePageDigest(r.Digest)
}

// PageDeleteRequest removes every stored version of page ID.
type PageDeleteRequest struct {
	ID string `json:"id"`
}

// Validate checks the request.
func (r PageDeleteRequest) Validate() error { return ValidatePageID(r.ID) }

// PageListRequest has no parameters.
type PageListRequest struct{}

// Validate accepts the empty request.
func (PageListRequest) Validate() error { return nil }

// PageVersion is one stored page.
type PageVersion struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// PageListResponse lists the stored pages.
type PageListResponse struct {
	Pages []PageVersion `json:"pages"`
}

// PageResponse is the answer of the page operations without data.
type PageResponse struct{}

// The page operations.
var (
	PageBegin  = agentrpc.Op[PageBeginRequest, PageResponse]{Name: "page.begin"}
	PageWrite  = agentrpc.Op[PageWriteRequest, PageResponse]{Name: "page.write"}
	PageCommit = agentrpc.Op[PageCommitRequest, PageResponse]{Name: "page.commit"}
	PageDelete = agentrpc.Op[PageDeleteRequest, PageResponse]{Name: "page.delete"}
	PageList   = agentrpc.Op[PageListRequest, PageListResponse]{Name: "page.list"}
)
