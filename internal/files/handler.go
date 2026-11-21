// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The helper's HTTP interface. Only the panel calls it, through the
// instance's socket; it is not the public API (that is /api/v1 in the
// panel, which checks roles and grants and writes the audit log).
//
//	GET  /v1/list?path=P          folder listing (Listing)
//	GET  /v1/stat?path=P          one entry (Entry)
//	GET  /v1/file?path=P          file content (Range supported)
//	PUT  /v1/file?path=P&version=V  replace (V from Entry.Version) or create (no V), body is the content
//	POST /v1/mkdir   {"path"}
//	POST /v1/touch   {"path"}
//	POST /v1/rename  {"from","to"}
//	POST /v1/move    {"paths","to"}
//	POST /v1/copy    {"paths","to"}
//	POST /v1/delete  {"paths"}
//	POST /v1/chmod   {"paths","mode"}
//
// Errors are JSON {"error": code, "message": text} with the codes of
// ErrorCode.

// maxJSONBody caps a request body of the POST calls.
const maxJSONBody = 1 << 20

// Request is the body of the POST calls; each call reads its own fields.
type Request struct {
	Path  string   `json:"path,omitempty"`
	From  string   `json:"from,omitempty"`
	To    string   `json:"to,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Mode  string   `json:"mode,omitempty"`
}

// ErrorBody is the JSON of a failed call.
type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ErrorCode returns the code and HTTP status of err.
func ErrorCode(err error) (string, int) {
	switch {
	case errors.Is(err, ErrInvalidPath), errors.Is(err, ErrInvalidMode):
		return "invalid", http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return "not_found", http.StatusNotFound
	case errors.Is(err, ErrExists):
		return "exists", http.StatusConflict
	case errors.Is(err, ErrChanged):
		return "changed", http.StatusConflict
	case errors.Is(err, ErrNotDir):
		return "not_dir", http.StatusBadRequest
	case errors.Is(err, ErrNotRegular):
		return "not_file", http.StatusBadRequest
	case errors.Is(err, ErrSymlink):
		return "symlink", http.StatusBadRequest
	case errors.Is(err, ErrIntoItself):
		return "into_itself", http.StatusBadRequest
	case errors.Is(err, ErrHome):
		return "home", http.StatusBadRequest
	case errors.Is(err, ErrTooLarge):
		return "too_large", http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrDenied):
		return "denied", http.StatusForbidden
	case errors.Is(err, ErrNoSpace):
		return "no_space", http.StatusInsufficientStorage
	default:
		return "internal", http.StatusInternalServerError
	}
}

// Handler serves the helper's interface for one home.
type Handler struct {
	Root *Root
	Log  *slog.Logger
}

func (h *Handler) log() *slog.Logger {
	if h.Log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return h.Log
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/list":
		l, err := h.Root.List(r.URL.Query().Get("path"))
		h.reply(w, r, l, err)
	case "GET /v1/stat":
		e, err := h.Root.Stat(r.URL.Query().Get("path"))
		h.reply(w, r, e, err)
	case "GET /v1/file", "HEAD /v1/file":
		h.download(w, r)
	case "PUT /v1/file":
		q := r.URL.Query()
		e, err := h.Root.WriteFile(q.Get("path"), r.Body, WriteOptions{Version: q.Get("version")})
		h.reply(w, r, e, err)
	case "POST /v1/mkdir", "POST /v1/touch", "POST /v1/rename", "POST /v1/move",
		"POST /v1/copy", "POST /v1/delete", "POST /v1/chmod":
		h.post(w, r)
	default:
		h.fail(w, r, http.StatusNotFound, "not_found", "no such call")
	}
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	var req Request

	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		h.fail(w, r, http.StatusBadRequest, "invalid", "request body: "+err.Error())

		return
	}

	var (
		out any = struct {
			OK bool `json:"ok"`
		}{true}
		err error
	)

	switch strings.TrimPrefix(r.URL.Path, "/v1/") {
	case "mkdir":
		err = h.Root.Mkdir(req.Path)
	case "touch":
		err = h.Root.CreateFile(req.Path)
	case "rename":
		err = h.Root.Rename(req.From, req.To)
	case "move":
		err = h.Root.Move(req.Paths, req.To)
	case "copy":
		out, err = h.Root.Copy(req.Paths, req.To)
	case "delete":
		err = h.Root.Delete(req.Paths)
	case "chmod":
		var m uint32
		if m, err = ParseMode(req.Mode); err == nil {
			err = h.Root.Chmod(req.Paths, m)
		}
	}

	h.reply(w, r, out, err)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	f, e, err := h.Root.OpenFile(r.URL.Query().Get("path"))
	if err != nil {
		h.reply(w, r, nil, err)

		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(e.Name)}))
	w.Header().Set("X-Hostashi-Version", e.Version)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", e.Modified, f)
}

func (h *Handler) reply(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		code, status := ErrorCode(err)
		if status == http.StatusInternalServerError {
			h.log().ErrorContext(r.Context(), "files call failed", "call", r.URL.Path, "err", err)
		}

		h.fail(w, r, status, code, err.Error())

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log().InfoContext(r.Context(), "files reply not sent", "err", err)
	}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(ErrorBody{Error: code, Message: msg}); err != nil {
		h.log().InfoContext(r.Context(), "files error not sent", "err", fmt.Sprint(err))
	}
}
