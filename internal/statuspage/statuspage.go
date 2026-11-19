// SPDX-License-Identifier: AGPL-3.0-or-later

// Package statuspage checks uploaded maintenance and parking pages on the
// panel side (ADR-0027) and holds the built-in pages. An upload is a single
// HTML file or a ZIP file; both become a set of files with index.html at the
// top. The agent checks names, types and sizes again.
package statuspage

import (
	"archive/zip"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// MaxUploadBytes bounds an uploaded file, ZIP or HTML.
const MaxUploadBytes = agentapi.MaxPageBytes

// Kinds of pages.
const (
	KindMaintenance = "maintenance"
	KindParking     = "parking"
)

// ErrInvalid wraps every reason an upload is refused; the message is meant
// for the person who uploaded it.
var ErrInvalid = errors.New("invalid page")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// Parse turns an upload into the files of a page. name is the uploaded
// file name; a ZIP is recognized by its content.
func Parse(name string, data []byte) (map[string][]byte, error) {
	if len(data) > MaxUploadBytes {
		return nil, invalid("the file is larger than %d MiB", MaxUploadBytes>>20)
	}

	if bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06")) {
		return parseZip(data)
	}

	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".html") && !strings.HasSuffix(lower, ".htm") {
		return nil, invalid("upload an .html file or a .zip file")
	}

	if len(data) == 0 {
		return nil, invalid("the file is empty")
	}

	return map[string][]byte{agentapi.PageIndex: data}, nil
}

func parseZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("the ZIP file cannot be read")
	}

	entries := make([]*zip.File, 0, len(zr.File))

	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") && f.Mode().IsDir() {
			continue
		}

		if !f.Mode().IsRegular() {
			return nil, invalid("%q is not a regular file (links are not allowed)", f.Name)
		}

		if f.Flags&0x1 != 0 {
			return nil, invalid("%q is encrypted", f.Name)
		}

		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, invalid("%q uses an unsupported compression", f.Name)
		}

		entries = append(entries, f)
	}

	if len(entries) > agentapi.MaxPageFiles {
		return nil, invalid("more than %d files", agentapi.MaxPageFiles)
	}

	prefix := commonFolder(entries)
	files := make(map[string][]byte, len(entries))
	total := 0

	for _, f := range entries {
		rel := strings.TrimPrefix(f.Name, prefix)
		if err := agentapi.ValidatePagePath(rel); err != nil {
			return nil, invalid("%q: use names of letters, digits, '.', '_' and '-', at most two folders, and only HTML, CSS, JavaScript, images and woff2 fonts", f.Name)
		}

		if _, dup := files[rel]; dup {
			return nil, invalid("%q is in the ZIP file twice", f.Name)
		}

		b, err := readLimited(f, agentapi.MaxPageBytes-total)
		if err != nil {
			return nil, err
		}

		total += len(b)
		files[rel] = b
	}

	if _, ok := files[agentapi.PageIndex]; !ok {
		return nil, invalid("the ZIP file has no %s at the top", agentapi.PageIndex)
	}

	return files, nil
}

// readLimited reads at most limit bytes of f, counting what it really
// unpacks instead of trusting the header (zip bombs).
func readLimited(f *zip.File, limit int) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("%q cannot be read", f.Name)
	}

	defer rc.Close()

	b, err := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
	if err != nil {
		return nil, invalid("%q cannot be read", f.Name)
	}

	if len(b) > limit {
		return nil, invalid("the unpacked page is larger than %d MiB", agentapi.MaxPageBytes>>20)
	}

	return b, nil
}

// commonFolder returns "dir/" when every entry is below one folder and
// there is no index.html at the top, so a zipped folder works as well.
func commonFolder(entries []*zip.File) string {
	if len(entries) == 0 {
		return ""
	}

	first, _, ok := strings.Cut(entries[0].Name, "/")
	if !ok {
		return ""
	}

	prefix := first + "/"
	for _, f := range entries {
		if !strings.HasPrefix(f.Name, prefix) {
			return ""
		}
	}

	return prefix
}

//go:embed builtin/*.html
var builtinFS embed.FS

// Built-in page ids. They are agent page ids too and cannot collide with
// uploaded pages ("p<n>").
const (
	BuiltinMaintenance = "builtin-maintenance"
	BuiltinParking     = "builtin-parking"
)

// Builtin returns the files of a built-in page and its kind.
func Builtin(id string) (files map[string][]byte, kind string, ok bool) {
	var name string

	switch id {
	case BuiltinMaintenance:
		name, kind = "builtin/maintenance.html", KindMaintenance
	case BuiltinParking:
		name, kind = "builtin/parking.html", KindParking
	default:
		return nil, "", false
	}

	b, err := fs.ReadFile(builtinFS, name)
	if err != nil {
		panic(err) // embedded at build time
	}

	return map[string][]byte{agentapi.PageIndex: b}, kind, true
}
