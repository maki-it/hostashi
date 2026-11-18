// Package helper implements the unprivileged file helper of the agent.
//
// The agent (root) starts this program as the site user with the site's root
// directory already opened as fd 3. The helper reads one request from stdin,
// performs it through package confine and writes one response to stdout, then
// exits. It never needs root, so a bug in it can do no more than the site user
// could do anyway.
//
// Wire format, both directions: one JSON header on a single line, terminated
// by "\n", followed by an optional raw body until EOF. Write requests carry the
// file content as body, read responses carry it too, so large files never go
// through JSON.
package helper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/maki-it/hostashi/spike/s4-site-helper/confine"
)

// Arg0 selects helper mode when the agent binary re-executes itself.
const Arg0 = "__site-helper"

const (
	OpStat   = "stat"
	OpList   = "list"
	OpRead   = "read"
	OpWrite  = "write"
	OpMkdir  = "mkdir"
	OpRemove = "remove"
	OpRename = "rename"
	OpChmod  = "chmod"

	MaxHeader   = 16 << 10
	MaxRespHdr  = 1 << 20
	MaxBody     = 64 << 20 // per read or write, spike value
	MaxListSize = 500
)

// Request is flat on the wire but validated per operation: fields that do not
// belong to the operation must be zero.
type Request struct {
	Op        string `json:"op"`
	Path      string `json:"path,omitempty"`
	To        string `json:"to,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
	Size      int64  `json:"size,omitempty"` // write: exact body length
	Overwrite bool   `json:"overwrite,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Parents   bool   `json:"parents,omitempty"`
	After     string `json:"after,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// Parsed holds the validated paths.
type Parsed struct {
	Path, To confine.Rel
}

func (r Request) Validate() (Parsed, error) {
	var p Parsed
	var err error
	if p.Path, err = confine.ParseRel(r.Path); err != nil {
		return p, err
	}
	if p.To, err = confine.ParseRel(r.To); err != nil {
		return p, err
	}
	bad := func(field string) error {
		return fmt.Errorf("%w: field %s not allowed for %s", confine.ErrInvalidPath, field, r.Op)
	}
	has := map[string]bool{
		"path": r.Path != "", "to": r.To != "", "mode": r.Mode != 0, "size": r.Size != 0,
		"overwrite": r.Overwrite, "recursive": r.Recursive, "parents": r.Parents,
		"after": r.After != "", "limit": r.Limit != 0,
	}
	allowed := map[string][]string{
		OpStat:   {"path"},
		OpList:   {"path", "after", "limit"},
		OpRead:   {"path"},
		OpWrite:  {"path", "mode", "size", "overwrite"},
		OpMkdir:  {"path", "mode", "parents"},
		OpRemove: {"path", "recursive"},
		OpRename: {"path", "to"},
		OpChmod:  {"path", "mode"},
	}
	list, ok := allowed[r.Op]
	if !ok {
		return p, fmt.Errorf("%w: unknown operation %q", confine.ErrInvalidPath, r.Op)
	}
	for f, set := range has {
		if !set {
			continue
		}
		found := false
		for _, a := range list {
			found = found || a == f
		}
		if !found {
			return p, bad(f)
		}
	}
	switch r.Op {
	case OpWrite:
		if r.Size < 0 || r.Size > MaxBody {
			return p, fmt.Errorf("%w: size out of range", confine.ErrInvalidPath)
		}
		if p.Path.IsRoot() {
			return p, fmt.Errorf("%w: path required", confine.ErrInvalidPath)
		}
	case OpRename:
		if p.Path.IsRoot() || p.To.IsRoot() {
			return p, fmt.Errorf("%w: path and to required", confine.ErrInvalidPath)
		}
	case OpList:
		if r.Limit < 0 || r.Limit > MaxListSize {
			return p, fmt.Errorf("%w: limit out of range", confine.ErrInvalidPath)
		}
		if _, err := confine.ParseRel(r.After); err != nil || bytes.ContainsRune([]byte(r.After), '/') {
			return p, fmt.Errorf("%w: bad cursor", confine.ErrInvalidPath)
		}
	}
	return p, nil
}

// Response header. On failure only Code and Message are set.
type Response struct {
	OK        bool           `json:"ok"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Info      *confine.Info  `json:"info,omitempty"`
	Entries   []confine.Info `json:"entries,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	BodySize  int64          `json:"bodySize,omitempty"` // read: body length that follows
}

func failure(err error) Response {
	code := confine.CodeOf(err)
	msg := "operation failed"
	var ce *confine.Error
	if errors.As(err, &ce) {
		msg = ce.Msg
	} else if errors.Is(err, confine.ErrInvalidPath) {
		msg = err.Error()
	}
	return Response{Code: code, Message: msg}
}

// writeHeader writes v as one JSON line.
func writeHeader(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// readHeader reads one line of at most max bytes and decodes it strictly.
func readHeader(br *bufio.Reader, max int, v any) error {
	var line []byte
	for {
		part, isPrefix, err := br.ReadLine()
		if err != nil {
			return err
		}
		line = append(line, part...)
		if len(line) > max {
			return errors.New("header too large")
		}
		if !isPrefix {
			break
		}
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data in header")
	}
	return nil
}
